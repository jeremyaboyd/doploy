-- Schema for the microblog example.
--
-- Applied on every deploy, so everything here is idempotent. Adding a column
-- later means adding another guarded ALTER at the bottom rather than editing
-- the CREATE above it.

BEGIN;

CREATE TABLE IF NOT EXISTS users (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email           TEXT        NOT NULL UNIQUE,
    password_hash   TEXT        NOT NULL,
    display_name    TEXT        NOT NULL,
    is_admin        BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Posts are written by admins. visibility 'users' hides a post, and its
-- attachments, from anyone who is not signed in.
CREATE TABLE IF NOT EXISTS posts (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug         TEXT        NOT NULL UNIQUE,
    title        TEXT        NOT NULL,
    body         TEXT        NOT NULL DEFAULT '',
    visibility   TEXT        NOT NULL DEFAULT 'public'
                 CHECK (visibility IN ('public', 'users')),
    author_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS posts_published_idx
    ON posts (published_at DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS posts_visibility_idx
    ON posts (visibility);

-- Uploaded files. The bytes live on the block volume mounted at /mnt/uploads;
-- only metadata is stored here. stored_name is a generated identifier, never
-- anything derived from user input.
CREATE TABLE IF NOT EXISTS attachments (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id      BIGINT      NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    kind         TEXT        NOT NULL
                 CHECK (kind IN ('image', 'video', 'file')),
    original_name TEXT       NOT NULL,
    stored_name  TEXT        NOT NULL UNIQUE,
    mime_type    TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS attachments_post_idx
    ON attachments (post_id);

CREATE TABLE IF NOT EXISTS comments (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id    BIGINT      NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS comments_post_idx
    ON comments (post_id, created_at);

COMMIT;
