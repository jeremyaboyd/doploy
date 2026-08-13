# Microblog

A personal microblog deployed across two droplets, and the example that drove
several of doploy's features.

- **web** — React frontend and a Node API, both in Docker, both built on the
  droplet. No registry needed.
- **db** — PostgreSQL installed from apt. No container.

The point of the example is the wiring between them.

## The interesting problem

The API needs the database's address. Postgres needs the API's address, to allow
it through host-based auth. Neither address exists until DigitalOcean creates
the droplets, and both change if a droplet is rebuilt.

So the spec names them instead of hardcoding them:

```yaml
services:
  api:
    environment:
      DATABASE_URL: postgres://microblog:${POSTGRES_PASSWORD}@${droplet.db.private_ip}:5432/microblog

droplets:
  db:
    setup:
      env:
        DB_LISTEN_IP: ${droplet.db.private_ip}
        ALLOWED_CLIENT_IP: ${droplet.web.private_ip}
```

`${droplet.*}` references survive the initial parse untouched. doploy provisions
every droplet first, then substitutes the real addresses, then runs setup and
renders compose files. Every deploy rewrites them, so rebuilding the web droplet
onto a new address fixes `pg_hba.conf` on the next run rather than breaking it.

A typo is caught before anything is created — referencing a droplet that does
not exist, or a field that is not `private_ip` / `public_ip` / `name`, fails at
load time.

## Running it

```bash
cd examples/microblog
cp .env.example .env
```

Fill in `.env` — at minimum `DO_SSH_KEY`, `POSTGRES_PASSWORD`, `JWT_SECRET`,
`ADMIN_EMAIL`, and `ADMIN_PASSWORD`. Generate the secrets rather than inventing
them:

```bash
openssl rand -base64 32
```

Then:

```bash
doploy calculate
```

```bash
doploy deploy --dry-run
```

```bash
doploy deploy
```

The first deploy takes a while: it creates two droplets, installs Postgres,
installs Docker, uploads both build contexts, and builds two images on the
droplet. Subsequent deploys only rebuild what changed.

When it finishes, the web droplet's public address serves the blog. Sign in with
the `ADMIN_EMAIL` / `ADMIN_PASSWORD` you set to write posts.

## What the app does

- **Admin** writes posts, with attachments, and can mark any post *members only*.
- **Anyone** can register an account, then comment.
- **Attachments** — images render inline, video gets a player, everything else
  becomes a download. They live on a 50 GB block volume, not in the container,
  so they survive a redeploy.
- **Members-only posts** hide their body, their comments, *and their
  attachments* from signed-out visitors. Attachments stream through the API
  rather than being served off disk by nginx, precisely so that check can run on
  every request.

## Layout

```
doploy.yml            the two droplets, their setup, and the services
db/
  setup-postgres.sh   moves the cluster to the volume, writes pg_hba, seeds the role
  init.sql            schema, idempotent, applied every deploy
backend/              Node + Express API (Dockerfile builds on the droplet)
frontend/             React + Vite, served by nginx (multi-stage Dockerfile)
```

## Notes on the pieces

**Deploy order.** `web` declares `depends_on: [db]`, so Postgres is accepting
connections before the API container starts. The API also retries its first
connection, so restarting just that container does not depend on the ordering.

**The data directory** is moved to the block volume on first setup and left
alone afterwards. Destroying and recreating the db droplet keeps the data, as
long as the volume survives.

**Postgres binds to the private interface only** — `localhost` plus the
droplet's VPC address, never the public one. That, not the firewall, is what
keeps it off the internet. The db droplet's firewall allows no public ingress at
all beyond SSH.

**Secrets** reach the setup script through `setup.env`, which doploy writes to a
mode-600 file on the droplet and sources before each command. They are not
passed on a command line, so they stay out of the process list, and doploy does
not echo inline setup commands for the same reason.

**Uploads need a chown.** The web droplet has a one-line `setup` block that
chowns `/mnt/uploads` to uid 1000. A block volume mounts root-owned, and a bind
mount's ownership comes from the host — it shadows whatever the Dockerfile
chowned — so without it every upload fails with `EACCES`. doploy mounts volumes
before setup runs, which is what makes the one-liner sufficient.

**Healthchecks use 127.0.0.1, not localhost.** Inside the container `localhost`
resolves to `::1` first. nginx listening only on IPv4 means the check gets
connection refused while the site serves perfectly well — a green site and a
red deploy. The config now binds `[::]:80` as well.

## Things this example is not

It is a demonstration of deployment wiring, not a production blog. Before
trusting it with anything real:

- **No TLS.** Port 80 only. Put a certificate in front of it — a load balancer,
  or Caddy/nginx with ACME on the droplet.
- **No rate limiting** on login or registration.
- **No email verification**, password reset, or account recovery.
- **No backups** beyond whatever you enable on the droplet and volume.
- **Post bodies render as plain text**, deliberately. Adding Markdown means
  adding sanitisation, or the admin account becomes an XSS vector.
- **`ADMIN_CIDR` defaults to `0.0.0.0/0`**, which leaves SSH open to the world.
  Set it to your own address.
