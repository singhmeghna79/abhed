-- Access records, applied by the edition that keeps them, beside the event
-- store's own tables and under the same tenant setting.
--
-- Row-level security works here for the same reason it works on events: every
-- pooled connection has already run set_config('app.tenant_id'), so a table
-- that enables and FORCEs RLS on tenant_id is isolated without this file doing
-- anything the event store did not.

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

-- Versioned apart from the event store's schema_version: that table is the
-- Community store's to write, and two writers of one version table would
-- collide the first time either edition changed its schema.
CREATE TABLE IF NOT EXISTS access_schema_version (
  version    INT         PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO access_schema_version (version) VALUES (1) ON CONFLICT DO NOTHING;

