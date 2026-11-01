-- +goose Up
-- =============================================================================
-- 00002_agent_runs.sql: execution history and outputs for Ryklin's Agents
-- =============================================================================

CREATE TABLE agent_runs (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        REFERENCES users (id) ON DELETE SET NULL,
    agent_slug     text        NOT NULL
                               CHECK (agent_slug IN (
                                   'mcdonalds-receipt-surveyer',
                                   'warcraftlogs-blame-me',
                                   'git-repo-code-reviewer'
                               )),
    status         text        NOT NULL DEFAULT 'queued'
                               CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    input_payload  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    output_payload jsonb       NOT NULL DEFAULT '{}'::jsonb,
    error_message  text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX agent_runs_user_id_idx ON agent_runs (user_id);
CREATE INDEX agent_runs_slug_created_at_idx ON agent_runs (agent_slug, created_at DESC);

CREATE TRIGGER agent_runs_set_updated_at
    BEFORE UPDATE ON agent_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS agent_runs;
