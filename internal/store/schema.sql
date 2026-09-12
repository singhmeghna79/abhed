-- Titan event store schema.
--
-- Implements docs/architecture/10-data-model.md. Two properties matter above
-- all: events are append-only (no UPDATE, no DELETE), and tenant isolation is
-- enforced by row-level security rather than only by query construction. An
-- audit log you can edit is not an audit log, and a boundary that exists in one
-- place is not a boundary.

CREATE TABLE IF NOT EXISTS sessions (
  id              TEXT PRIMARY KEY,
  tenant_id       TEXT        NOT NULL,
  user_id         TEXT        NOT NULL,
  workspace       TEXT        NOT NULL,
  model           TEXT        NOT NULL,
  prompt_hash     TEXT        NOT NULL DEFAULT '',
  harness_version TEXT        NOT NULL DEFAULT '',
  mode            TEXT        NOT NULL DEFAULT 'default',
  -- The opening request, kept so a session list is readable at a glance.
  prompt          TEXT        NOT NULL DEFAULT '',
  parent_id       TEXT        REFERENCES sessions(id),
  started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at        TIMESTAMPTZ,
  terminal_reason TEXT,
  turns           INT         NOT NULL DEFAULT 0,
  tokens_in       BIGINT      NOT NULL DEFAULT 0,
  tokens_out      BIGINT      NOT NULL DEFAULT 0,
  tokens_cached   BIGINT      NOT NULL DEFAULT 0,
  compactions     INT         NOT NULL DEFAULT 0,
  gpu_seconds     NUMERIC     NOT NULL DEFAULT 0,
  cost_usd        NUMERIC     NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS sessions_tenant_started_idx
  ON sessions (tenant_id, started_at DESC);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_parent_idx ON sessions (parent_id)
  WHERE parent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS events (
  id         TEXT        PRIMARY KEY,
  session_id TEXT        NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
  tenant_id  TEXT        NOT NULL,
  parent_id  TEXT,
  seq        BIGINT      NOT NULL,
  type       TEXT        NOT NULL,
  payload    JSONB       NOT NULL,
  actor      TEXT        NOT NULL,
  -- Provenance travels with the event: content read from files, tool output,
  -- MCP responses and search results are data, never instructions.
  trust      TEXT        NOT NULL DEFAULT 'trusted',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT events_seq_unique UNIQUE (session_id, seq),
  CONSTRAINT events_trust_valid CHECK (trust IN ('trusted', 'untrusted'))
);

CREATE INDEX IF NOT EXISTS events_session_seq_idx ON events (session_id, seq);
CREATE INDEX IF NOT EXISTS events_type_time_idx   ON events (type, created_at DESC);
CREATE INDEX IF NOT EXISTS events_tenant_time_idx ON events (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS events_payload_idx     ON events USING GIN (payload);

-- Append-only enforcement. Retention is handled by dropping partitions or by a
-- privileged archival role, never by mutating rows in place.
CREATE OR REPLACE FUNCTION titan_events_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'events are append-only: % on events is not permitted', TG_OP
    USING HINT = 'Audit integrity depends on immutability. Use retention policy to expire old partitions.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS events_no_update ON events;
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
  FOR EACH ROW EXECUTE FUNCTION titan_events_immutable();

DROP TRIGGER IF EXISTS events_no_delete ON events;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
  FOR EACH ROW EXECUTE FUNCTION titan_events_immutable();

-- Checkpoints back /undo: the content of a file immediately before the agent
-- changed it. NULL `before` means the file did not previously exist.
CREATE TABLE IF NOT EXISTS checkpoints (
  id         TEXT        PRIMARY KEY,
  session_id TEXT        NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
  tenant_id  TEXT        NOT NULL,
  event_seq  BIGINT      NOT NULL,
  path       TEXT        NOT NULL,
  before     BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS checkpoints_session_idx
  ON checkpoints (session_id, event_seq DESC);

-- Model registry with the capability profile from the conformance suite
-- (docs/architecture/08-eval.md L2). A model that has not passed conformance
-- should not be enabled.
CREATE TABLE IF NOT EXISTS models (
  name            TEXT PRIMARY KEY,
  provider        TEXT        NOT NULL,
  endpoint        TEXT        NOT NULL,
  context_window  INT         NOT NULL DEFAULT 0,
  profile         JSONB       NOT NULL DEFAULT '{}'::jsonb,
  enabled         BOOLEAN     NOT NULL DEFAULT false,
  registered_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Row-level security. The application connects as a non-superuser role and sets
-- app.tenant_id per connection; even a query that forgets its WHERE clause
-- cannot cross a tenant boundary.
--
-- FORCE is not optional here. A table's OWNER bypasses ordinary RLS, and the
-- application role almost always owns the tables it created — so ENABLE alone
-- leaves the policy silently inert. This was caught by
-- TestRowLevelSecurityIsolatesTenants, which read another tenant's rows until
-- FORCE was added. Superusers still bypass RLS, which is why the application
-- must never connect as one.
ALTER TABLE sessions    ENABLE ROW LEVEL SECURITY;
ALTER TABLE events      ENABLE ROW LEVEL SECURITY;
ALTER TABLE checkpoints ENABLE ROW LEVEL SECURITY;

ALTER TABLE sessions    FORCE ROW LEVEL SECURITY;
ALTER TABLE events      FORCE ROW LEVEL SECURITY;
ALTER TABLE checkpoints FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS sessions_tenant_isolation ON sessions;
CREATE POLICY sessions_tenant_isolation ON sessions
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

DROP POLICY IF EXISTS events_tenant_isolation ON events;
CREATE POLICY events_tenant_isolation ON events
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

DROP POLICY IF EXISTS checkpoints_tenant_isolation ON checkpoints;
CREATE POLICY checkpoints_tenant_isolation ON checkpoints
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Added after v1: existing deployments get the column without a migration step.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS prompt TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS schema_version (
  version    INT         PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------- access ---
-- Who asked for access, who has it, who had it, and why it ended.
--
-- This exists because the invite store is in memory and the request form only
-- sends an email: after a restart nobody could answer "who has access" and
-- nobody could ever answer "who had it in the past". An access decision is
-- exactly the kind of thing that has to outlive the process that made it.
--
-- One row per PERSON, not per invite. An invite is a mechanism; the grant is
-- the fact, and it survives the code being reissued or expiring.
CREATE TABLE IF NOT EXISTS access_grants (
  id           TEXT        PRIMARY KEY,
  tenant_id    TEXT        NOT NULL,
  email        TEXT        NOT NULL,
  name         TEXT        NOT NULL DEFAULT '',
  company      TEXT        NOT NULL DEFAULT '',
  use_case     TEXT        NOT NULL DEFAULT '',
  -- requested -> granted -> revoked | expired. A row never leaves this table;
  -- ending someone's access is a status change plus a reason, so the history
  -- of who once had it stays answerable.
  status       TEXT        NOT NULL,
  username     TEXT        NOT NULL DEFAULT '',   -- set when they redeem
  invite_code  TEXT        NOT NULL DEFAULT '',
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  granted_at   TIMESTAMPTZ,
  granted_by   TEXT        NOT NULL DEFAULT '',
  expires_at   TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ,
  revoked_by   TEXT        NOT NULL DEFAULT '',
  revoked_note TEXT        NOT NULL DEFAULT '',   -- free text, shown to nobody
  revoked_code TEXT        NOT NULL DEFAULT '',   -- policy clause, emailed
  screen_score INT         NOT NULL DEFAULT 0,
  screen_note  TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS access_grants_email  ON access_grants (tenant_id, email);
CREATE INDEX IF NOT EXISTS access_grants_status ON access_grants (tenant_id, status);

-- Every change to a grant, kept separately and never rewritten. The grant row
-- says what is true now; this says how it got there, which is what an audit
-- actually asks for.
CREATE TABLE IF NOT EXISTS access_events (
  id        BIGSERIAL   PRIMARY KEY,
  tenant_id TEXT        NOT NULL,
  grant_id  TEXT        NOT NULL,
  at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor     TEXT        NOT NULL,
  action    TEXT        NOT NULL,
  detail    TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS access_events_grant ON access_events (tenant_id, grant_id, at);

-- Append-only, enforced by the database rather than by convention. The same
-- pattern as the events table: an audit trail an administrator can quietly
-- edit is not an audit trail.
CREATE OR REPLACE FUNCTION access_events_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'access_events is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS access_events_no_update ON access_events;
CREATE TRIGGER access_events_no_update BEFORE UPDATE ON access_events
  FOR EACH ROW EXECUTE FUNCTION access_events_immutable();

DROP TRIGGER IF EXISTS access_events_no_delete ON access_events;
CREATE TRIGGER access_events_no_delete BEFORE DELETE ON access_events
  FOR EACH ROW EXECUTE FUNCTION access_events_immutable();

ALTER TABLE access_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_grants FORCE  ROW LEVEL SECURITY;
ALTER TABLE access_events FORCE  ROW LEVEL SECURITY;

DROP POLICY IF EXISTS access_grants_tenant_isolation ON access_grants;
CREATE POLICY access_grants_tenant_isolation ON access_grants
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

DROP POLICY IF EXISTS access_events_tenant_isolation ON access_events;
CREATE POLICY access_events_tenant_isolation ON access_events
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

INSERT INTO schema_version (version) VALUES (2) ON CONFLICT DO NOTHING;
