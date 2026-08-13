# doploy

`docker compose up`, but it provisions the DigitalOcean droplets first.

You describe your infrastructure and your containers in one compose-style file.
`doploy deploy` creates any droplets that do not exist yet, then connects over
SSH and brings the stack up with `docker compose`. Run it again and it reuses
what is already there and just updates the containers.

```bash
doploy deploy
```

## Why the binary is called `doploy`, not `do`

`do` is a reserved word in bash and zsh — it is the loop keyword. Typing
`do deploy` at a POSIX prompt gets you:

```
bash: syntax error near unexpected token `deploy'
```

So the binary is `doploy`. In PowerShell, fish, or any shell where it does not
collide, alias it:

```powershell
Set-Alias do doploy
```

## Install

```bash
go install github.com/jeremyaboyd/doploy/cmd/doploy@latest
```

Or build from a clone:

```bash
go build -o doploy ./cmd/doploy
```

## Authenticate

Create a token with read and write scope at
<https://cloud.digitalocean.com/account/api/tokens>, then:

```bash
doploy auth init
```

The token is validated against the API before it is saved, and written with
owner-only permissions to your user config directory
(`%AppData%\doploy\credentials.json` on Windows, `~/.config/doploy/` elsewhere).

`DIGITALOCEAN_ACCESS_TOKEN` or `DO_TOKEN` in the environment takes precedence,
which is what you want in CI.

```bash
doploy auth status
doploy auth logout
```

**On OAuth:** `auth init` currently stores a personal access token. Real OAuth
needs a registered DigitalOcean OAuth application (client ID, secret, redirect
URL) that has to exist before any code can use it. The credential store already
carries the `method`, `refresh_token`, and `expiry` fields, so adding the
browser flow later will not invalidate anything saved today.

## The spec file

`doploy.yml` in the working directory, or `--file`.

```yaml
version: "1"
project: myapp          # namespaces the tags doploy uses to find your resources

defaults:
  region: nyc3
  size: s-1vcpu-1gb
  image: docker-20-04   # the Docker 1-Click: no bootstrap needed
  ssh_keys: [my-laptop] # names, fingerprints, or IDs already on your account

droplets:
  web:
    default: true       # services that do not name a droplet land here
    size: s-2vcpu-4gb
    firewall:
      inbound: ["80", "443"]
      ssh_sources: ["203.0.113.4/32"]   # lock SSH to your IP
    volumes:
      - name: data
        size_gb: 50     # mounted at /mnt/data

  worker:
    size: s-1vcpu-2gb

services:
  api:
    image: ghcr.io/example/api:${TAG:-latest}
    ports: ["80:8080"]
    environment:
      DATABASE_URL: ${DATABASE_URL:?set this before deploying}
    depends_on: [redis]
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 10s
      retries: 3

  redis:
    image: redis:7-alpine
    volumes:
      - redisdata:/data          # a docker named volume

  cruncher:
    droplet: worker
    image: ghcr.io/example/cruncher:${TAG:-latest}
    volumes:
      - /mnt/data/jobs:/jobs     # block storage bind mount

registries:
  ghcr.io:
    username: ${GITHUB_USER}
    password: ${GITHUB_TOKEN}
```

### Topology

Services map onto droplets, many-to-one:

- Omit the `droplets:` block entirely and doploy synthesizes a single droplet
  named `default` from your `defaults:`. Everything co-locates. This is the
  literal `docker compose up on one VM` case.
- With exactly one droplet defined, services fall onto it automatically.
- With several, either mark one `default: true` or set `droplet:` on each
  service. doploy refuses to guess.

Each droplet gets its own generated compose file containing only its services.

### Variables

`${VAR}`, `${VAR:-default}`, `${VAR:?message}`, `${VAR:+alternate}`, and `$$`
for a literal dollar sign — the same forms docker compose uses. Values come from
the process environment first, then a `.env` file beside the spec.
Single-quoted YAML scalars are left alone.

Interpolation happens on parsed scalar values, not on the raw file text, so a
value containing a colon or a newline can never corrupt the document structure.

### Volumes

Two different things share the word:

- **Block storage** — declared under a droplet's `volumes:`. doploy creates the
  DigitalOcean volume, attaches it, formats it *only if it has no filesystem*,
  and mounts it at `/mnt/<name>` with an `fstab` entry. Services reach it as a
  bind mount. Validation rejects a service mounting a `/mnt/` path with no
  volume behind it.
- **Docker named volumes** — any service mount whose source is not a path.
  These are declared in the generated compose file and managed by Docker.

Volumes are namespaced by project in your account (`myapp-data`), so two
projects can both call a volume `data`.

doploy never resizes or deletes a volume. Shrinking is impossible and deleting
destroys data; both are left to a deliberate manual action.

### Host setup, for things that should not be containers

A droplet can run host-level provisioning before any container starts. This is
how you get a database installed from apt rather than run as a container:

```yaml
droplets:
  db:
    setup:
      packages: [postgresql, postgresql-contrib]
      env:
        APP_DB_PASSWORD: ${POSTGRES_PASSWORD:?}   # written 0600, sourced per command
      files:
        - source: db/init.sql
          dest: /opt/app/init.sql
      run:
        - db/setup-postgres.sh                    # uploaded and executed
        - systemctl enable --now postgresql       # or run inline
```

Order is packages, then files, then commands. A `run` entry naming a `.sh` file
on disk is uploaded and executed; anything else runs inline.

**Setup runs on every deploy**, not just the first, so a changed init script
actually takes effect. That puts the burden of idempotency on your scripts.

Secrets belong in `setup.env` rather than inline in `run`. Values passed on a
command line appear in the droplet's process list, and doploy deliberately does
not echo inline setup commands for the same reason.

A droplet with a `setup` block and no services runs no containers at all, and
skips Docker entirely.

### Cross-droplet addresses

A droplet's address does not exist until it is created, so a spec that wires one
host to another names it instead:

```yaml
services:
  api:
    environment:
      DATABASE_URL: postgres://app:pw@${droplet.db.private_ip}:5432/app
```

Available fields are `private_ip`, `public_ip`, and `name`. These references
survive the initial parse untouched. doploy provisions everything first, then
substitutes real addresses, then renders compose files and runs setup — so they
are correct on the first deploy and *rewritten* on every subsequent one.

Substitution is surgical: only `${droplet.*}` is touched, which is what makes it
safe to run over shell scripts full of `$VAR` and `$$`.

An unresolvable reference is an error, never an empty string. A silently empty
database host fails much later, on the droplet, where it is far harder to
diagnose.

### Building on the droplet

A service can build from source instead of pulling:

```yaml
services:
  api:
    build:
      context: backend
      dockerfile: Dockerfile   # optional, relative to context
      args:
        NODE_ENV: production
```

doploy packs the context (honouring `.dockerignore`, always skipping `.git` and
`node_modules`), uploads it, and lets the remote engine build. That makes a
project runnable with no registry at all, at the cost of building once per host.
Contexts are capped at 64 MiB — a forgotten `node_modules` fails loudly instead
of turning a deploy into a slow mystery.

### Deploy order

Droplets can declare dependencies, which orders the whole deploy:

```yaml
droplets:
  web:
    depends_on: [db]
```

`db` is fully set up before `web` is touched. Cycles are rejected at load time.
This is distinct from a service's `depends_on`, which only orders containers
within one host.

## Commands

```bash
doploy auth init                  # store and validate an API token
doploy auth status                # show the authenticated account
doploy auth logout                # remove stored credentials

doploy list droplets [--tag T]    # your droplets, with monthly cost
doploy list oses                  # bootable distribution images
doploy list images                # 1-Click marketplace images
doploy list sizes [region]        # droplet sizes, cheapest first
doploy list regions               # deployment regions

doploy calculate                  # estimated monthly cost of the spec
doploy deploy                     # provision, then deploy
```

Every command takes `--output json` for scripting.

### deploy

```bash
doploy deploy --dry-run                 # plan only, change nothing
doploy deploy --droplet web             # limit to one droplet (repeatable)
doploy deploy --wait                    # block until healthchecks pass
doploy deploy --no-bootstrap            # skip the Docker install step
doploy deploy --skip-setup              # containers only, leave host setup alone
doploy deploy --ssh-key ~/.ssh/deploy   # specific private key
doploy deploy --yes                     # skip the confirmation prompt
```

The first run for a spec shows what it will create and what it will cost, then
asks. Redeploys onto existing droplets add no recurring charge, so they proceed
without a prompt.

### calculate

Droplet prices come from the live API. Block storage ($0.10/GiB/month) and
backups (20% of the droplet price) use published list rates, declared as
constants in `internal/pricing`. Bandwidth overages and snapshots are not
included, and the output says so.

An unpriceable size produces a visible warning rather than a silently low total.

## How state works

There is no local state file. Ownership lives entirely in DigitalOcean tags:

```
doploy
doploy:project:myapp
doploy:droplet:myapp:web
```

`deploy` finds what it owns by listing droplets carrying the project tag and
matching the per-droplet tag. Consequences worth knowing:

- Any machine with a token can reconcile the same deployment. Losing a laptop
  never orphans infrastructure.
- Renaming a droplet in the DigitalOcean console does not confuse doploy.
- Removing those tags *does* orphan the droplet — doploy will build a new one
  alongside it.

Drift (a droplet whose live size or region no longer matches the spec) is
**reported, not corrected**. Resizing needs a power cycle and changing region
means rebuilding; both should be your decision, not a side effect of a deploy.

## What a deploy actually does

Per droplet, in order:

1. Reconcile the droplet — reuse if tagged, create and wait for `active` if not.
2. Create and attach block volumes; create or update the cloud firewall.
3. SSH in, retrying until `sshd` answers (a new droplet reports `active` before
   it accepts connections).
4. Install Docker if missing (skip with `--no-bootstrap`).
5. Format and mount any attached volumes.
6. Upload the generated compose file to `/opt/doploy/<project>/`, mode `0600`.
7. `docker login` to each configured registry, password over stdin.
8. `docker compose pull` then `up -d --remove-orphans`.

Droplets are handled one at a time. Deployments are usually one to three hosts,
and serial output is far easier to read when something breaks than interleaved
parallel logs.

### Two things to know about the bootstrap

**It runs Docker's install script.** Step 4 fetches `get.docker.com` and runs it
as root on the droplet. That is Docker's documented install path and what
DigitalOcean's own tutorials use, but it is a remote script executed with full
privileges. Two ways to avoid it: use the `docker-20-04` 1-Click image, which
ships with Docker already, or prepare hosts yourself and pass `--no-bootstrap`.

**Host keys are trust-on-first-use.** A droplet that did not exist a minute ago
has no key you could have verified in advance, so the first sighting is recorded
to doploy's own `known_hosts` (never your `~/.ssh/known_hosts`) and accepted. A
key that *changes* afterward is rejected — that is the case where the warning
means something. If you genuinely rebuilt the droplet, delete its line from the
file. `--insecure-host-key` skips verification entirely and says so when it does.

## Secrets

Environment values are inlined into the generated compose file, which is written
`0600` on the droplet. Registry passwords go over stdin so they never appear in
the droplet's process list. Neither is a substitute for a real secrets manager —
keep credentials in `.env` or the environment, not committed in `doploy.yml`.

## Not built

Deliberately out of scope for this pass, in rough order of how much they would
be missed:

- **`doploy destroy`** — no teardown command. Delete droplets in the console or
  with `doctl`. The tags make them easy to find:
  `doctl compute droplet list --tag-name doploy:project:myapp`.
- **`doploy logs` / `doploy status`** — SSH in and use `docker compose` directly;
  everything is under `/opt/doploy/<project>/`.
- **Building images locally.** `build:` builds on the *droplet*. doploy does not
  build on your machine or push to a registry, so a large context is uploaded
  and compiled once per host. For anything beyond a small app, build in CI and
  reference the pushed image instead.
- **Load balancers, managed databases, DNS records, reserved IPs.**
- **Rollback.** A failed deploy leaves the previous containers running if the
  pull fails, but there is no version history to roll back to.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/doploy
```

The layers, roughly outside-in:

| Package | Does |
|---|---|
| `internal/cli` | cobra command tree, flags, output formatting |
| `internal/spec` | the `doploy.yml` schema, interpolation, validation, compose generation |
| `internal/provision` | tag-based reconcile of droplets, volumes, firewalls |
| `internal/deploy` | SSH orchestration: bootstrap, mount, upload, compose up |
| `internal/sshx` | key discovery, TOFU host keys, file and archive upload over exec |
| `internal/archive` | packing build contexts, with `.dockerignore` handling |
| `internal/pricing` | cost estimation |
| `internal/doclient` | godo client and pagination |
| `internal/config` | credential storage |

Everything that does not need the API is unit tested — spec parsing,
interpolation, validation, compose generation, runtime address resolution,
deploy ordering, context packing, pricing, and tag round-tripping. Both shipped
examples are loaded by the test suite, so the documentation cannot silently rot.
The API and SSH layers are not tested, which is the main gap.

## Examples

- [`examples/doploy.yml`](examples/doploy.yml) — a two-droplet spec covering the
  common options.
- [`examples/microblog/`](examples/microblog/) — a working React + Node blog with
  native Postgres on its own droplet, showing host setup, cross-droplet
  addresses, and building on the droplet.
