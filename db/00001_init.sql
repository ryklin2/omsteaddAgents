-- +goose Up
-- =============================================================================
-- 00001_init.sql: starter schema
--
-- Conventions to keep as the schema grows:
--   * Primary keys are UUIDs from gen_random_uuid(), built into Postgres 13+
--     with no extension needed. They are safe to expose in URLs and don't
--     reveal how many rows exist. On Postgres 18+, uuidv7() gives
--     time-ordered IDs that index more efficiently.
--   * Timestamps are timestamptz (stored as UTC, converted on the way out).
--   * Tables whose rows get edited have updated_at, maintained by the
--     set_updated_at() trigger so application code can't forget to bump it.
--   * Table names are plural snake_case. Foreign keys are named <singular>_id.
--   * Every foreign key column is indexed. Postgres doesn't do this
--     automatically, and unindexed foreign keys make joins and deletes slow.
--   * Status-like columns use text with a CHECK constraint instead of a
--     Postgres enum, so values can be added or removed later with a simple
--     migration.
-- =============================================================================

-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- -----------------------------------------------------------------------------
-- users
-- -----------------------------------------------------------------------------
CREATE TABLE users (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email             text        NOT NULL,
    display_name      text,
    -- NULL for accounts that only sign in through an external provider.
    -- Store a bcrypt or argon2id hash here, never the password itself.
    password_hash     text,
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Case-insensitive uniqueness, so Ana@example.com and ana@example.com are the
-- same account. Query with WHERE lower(email) = lower($1) to use this index.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------------
-- sessions
-- Server-side sessions for cookie-based auth. The cookie holds a random token
-- (32 bytes from crypto/rand), and only its SHA-256 hash is stored here, so a
-- leaked copy of the database can't be used to hijack sessions. Look a
-- session up with WHERE token_hash = $1 AND expires_at > now(), passing the
-- hash computed in Go.
--
-- Using JWTs or an external auth provider (Clerk, Auth0, Firebase Auth)?
-- Delete this table.
-- -----------------------------------------------------------------------------
CREATE TABLE sessions (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    user_agent   text,
    ip_address   inet,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- Supports a periodic cleanup job: DELETE FROM sessions WHERE expires_at < now()
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- -----------------------------------------------------------------------------
-- projects: EXAMPLE domain table. Rename it or replace it with your own.
-- It shows the patterns worth copying: an owner foreign key, a status column
-- constrained with CHECK, a jsonb column for loosely structured data, a
-- composite index shaped like the most common query, and the updated_at
-- trigger.
-- -----------------------------------------------------------------------------
CREATE TABLE projects (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    description text,
    status      text        NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'archived')),
    metadata    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Serves "list my projects, newest first". Because owner_id is the leading
-- column, this also covers the owner_id foreign key.
CREATE INDEX projects_owner_id_created_at_idx ON projects (owner_id, created_at DESC);

CREATE TRIGGER projects_set_updated_at
    BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();
