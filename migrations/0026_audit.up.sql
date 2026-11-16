-- Audit log (docs/ceba/PLAN.md, phase E0; Configuración › Auditoría): who changed what, where and when. Append-only:
-- rows are never edited; they only go away with their institution. The services record best effort, so a failed
-- write here never undoes the change it describes
CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE audit.events (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    -- NULL for platform-wide actions of the superadmin
    institution_id uuid REFERENCES iam.institutions (id) ON DELETE CASCADE,
    user_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    -- Kept even when the account goes away
    user_name varchar(200) NOT NULL DEFAULT '',
    role varchar(40) NOT NULL DEFAULT '',
    -- Panel module the change belongs to (institution, structure, periods, users…), as joseph-app names them
    module varchar(40) NOT NULL,
    -- create | update | delete | status | grant | revoke | other
    action varchar(20) NOT NULL,
    -- What changed (authorization, platform_sheet, section, period…) and its id
    entity varchar(40) NOT NULL,
    entity_id varchar(64) NOT NULL DEFAULT '',
    -- One line for the list, in Spanish ("Registró la RD 0123-2026 · Forma a distancia")
    summary varchar(300) NOT NULL,
    -- Optional detail: changed fields, before and after
    changes jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT events_action_check CHECK (action IN ('create', 'update', 'delete', 'status', 'grant', 'revoke', 'other')),
    CONSTRAINT events_module_format CHECK (module ~ '^[a-z][a-z-]{1,39}$'),
    CONSTRAINT events_summary_required CHECK (btrim(summary) <> '')
);

CREATE INDEX idx_audit_events_institution ON audit.events (institution_id, created_at DESC, id DESC);
CREATE INDEX idx_audit_events_module ON audit.events (institution_id, module, created_at DESC);

-- Append-only: an event cannot be rewritten
CREATE FUNCTION audit.forbid_update() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit events are append-only' USING ERRCODE = 'check_violation';
END
$$;

CREATE TRIGGER events_append_only BEFORE UPDATE ON audit.events
    FOR EACH ROW EXECUTE FUNCTION audit.forbid_update();
