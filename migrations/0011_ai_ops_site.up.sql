-- IA educativa, operación (respaldos) y trámites del sitio público.
--
--   ai.generation          todo lo que produce la IA, con revisión humana antes de publicar
--   ai.risk_score          predicción de riesgo de deserción por estudiante
--   ops.backup_snapshot    historial de respaldos (de la plataforma o de una institución)
--   site.complaint         Libro de Reclamaciones virtual (D.S. N.° 011-2011-PCM), uno por institución
--   site.privacy_request   derechos ARCO (Ley N.° 29733)
--   site.contact_message   formulario de contacto

CREATE SCHEMA IF NOT EXISTS ai;
CREATE SCHEMA IF NOT EXISTS ops;
CREATE SCHEMA IF NOT EXISTS site;

CREATE TABLE ai.generation (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES iam.account (id),
    kind           text NOT NULL CHECK (kind IN (
                       'assessment', 'session_plan', 'feedback', 'tutor_chat',
                       'retention_message', 'placement_validation', 'other')),
    classroom_id   uuid,
    student_id     uuid,
    prompt         text NOT NULL,
    context        jsonb NOT NULL DEFAULT '{}',
    output         text,
    model          text NOT NULL,
    input_tokens   integer CHECK (input_tokens >= 0),
    output_tokens  integer CHECK (output_tokens >= 0),
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'discarded', 'published')),
    reviewed_by    uuid REFERENCES iam.account (id),
    reviewed_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id)
        ON DELETE SET NULL (classroom_id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id)
        ON DELETE SET NULL (student_id)
);

CREATE INDEX generation_account_idx ON ai.generation (account_id, created_at DESC);
CREATE INDEX generation_classroom_idx ON ai.generation (classroom_id);

-- Histórico: cada ejecución del modelo agrega una fila; la vigente es la más reciente.
CREATE TABLE ai.risk_score (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    student_id     uuid NOT NULL,
    period_id      uuid NOT NULL,
    score          numeric(5, 2) NOT NULL CHECK (score BETWEEN 0 AND 100),
    level          text NOT NULL CHECK (level IN ('low', 'medium', 'high')),
    -- Señales que explican el puntaje (asistencia, entregas, conexión…).
    factors        jsonb NOT NULL DEFAULT '{}',
    model_version  text NOT NULL,
    computed_at    timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id)
);

CREATE INDEX risk_score_student_idx ON ai.risk_score (student_id, computed_at DESC);
CREATE INDEX risk_score_period_level_idx ON ai.risk_score (period_id, level);

-- ---------------------------------------------------------------------------
-- Operación
-- ---------------------------------------------------------------------------

-- Sin RLS: lo gestiona la plataforma. institution_id identifica exportaciones
-- de una sola institución; null = respaldo completo de la base.
CREATE TABLE ops.backup_snapshot (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id  uuid REFERENCES tenant.institution (id) ON DELETE SET NULL,
    -- Identificador visible: SNP-20261024-0300.
    code            text NOT NULL UNIQUE,
    kind            text NOT NULL CHECK (kind IN ('scheduled', 'manual', 'period_close')),
    scope           text NOT NULL CHECK (scope IN ('full', 'incremental', 'database', 'documents', 'tenant_export')),
    size_bytes      bigint CHECK (size_bytes >= 0),
    sha256          bytea,
    storage_uri     text,
    status          text NOT NULL DEFAULT 'running'
                    CHECK (status IN ('running', 'verified', 'sealed', 'failed')),
    -- Copias WORM de cierre de periodo: no se pueden borrar antes de esta fecha.
    immutable_until timestamptz,
    triggered_by    uuid REFERENCES iam.account (id),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    error           text,
    CHECK (status = 'running' OR finished_at IS NOT NULL),
    CHECK (status NOT IN ('verified', 'sealed') OR sha256 IS NOT NULL),
    CHECK ((scope = 'tenant_export') = (institution_id IS NOT NULL))
);

CREATE INDEX backup_snapshot_started_idx ON ops.backup_snapshot (started_at DESC);

-- ---------------------------------------------------------------------------
-- Sitio público (sin cuenta: los datos del ciudadano van en la fila)
-- ---------------------------------------------------------------------------

CREATE TABLE site.complaint (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id      uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    -- Correlativo de la hoja: LR-2026-000123.
    code                text NOT NULL,
    kind                text NOT NULL CHECK (kind IN ('claim', 'complaint')),
    consumer_name       text NOT NULL,
    document_type       text NOT NULL CHECK (document_type IN ('dni', 'ce', 'passport', 'ruc', 'other')),
    document_number     text NOT NULL,
    address             text,
    phone               text,
    email               text NOT NULL,
    is_minor            boolean NOT NULL DEFAULT false,
    guardian_name       text,
    service_description text NOT NULL,
    claimed_amount      numeric(10, 2) CHECK (claimed_amount >= 0),
    detail              text NOT NULL,
    consumer_request    text NOT NULL,
    status              text NOT NULL DEFAULT 'received'
                        CHECK (status IN ('received', 'in_review', 'answered', 'closed')),
    -- Plazo legal de respuesta, calculado por la API en días hábiles.
    response_due_on     date NOT NULL,
    response            text,
    responded_by        uuid REFERENCES iam.account (id),
    responded_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, code),
    CHECK (NOT is_minor OR guardian_name IS NOT NULL),
    CHECK (status NOT IN ('answered', 'closed') OR responded_at IS NOT NULL)
);

-- Consulta pública de estado: código + documento.
CREATE INDEX complaint_lookup_idx ON site.complaint (institution_id, code, document_number);

CREATE TABLE site.privacy_request (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id  uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    code            text NOT NULL,
    request_right   text NOT NULL CHECK (request_right IN ('access', 'rectification', 'cancellation', 'opposition')),
    requester_name  text NOT NULL,
    document_type   text NOT NULL CHECK (document_type IN ('dni', 'ce', 'passport', 'other')),
    document_number text NOT NULL,
    email           text NOT NULL,
    phone           text,
    detail          text NOT NULL,
    status          text NOT NULL DEFAULT 'received'
                    CHECK (status IN ('received', 'in_review', 'answered', 'rejected')),
    response_due_on date NOT NULL,
    response        text,
    responded_by    uuid REFERENCES iam.account (id),
    responded_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, code)
);

CREATE TABLE site.contact_message (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    name           text NOT NULL,
    email          text NOT NULL,
    phone          text,
    subject        text,
    message        text NOT NULL,
    status         text NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'answered', 'archived')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX contact_message_new_idx ON site.contact_message (institution_id, created_at DESC) WHERE status = 'new';

SELECT util.attach_updated_at('ai');
SELECT util.attach_updated_at('site');

SELECT audit.attach('site.complaint', 'UPDATE OR DELETE');
SELECT audit.attach('site.privacy_request', 'UPDATE OR DELETE');
SELECT audit.attach('ops.backup_snapshot', 'UPDATE OR DELETE');
