-- Tutoría, bienestar y protección estudiantil + comunicación institucional.
--
--   wellbeing.student_profile     contexto del estudiante (trabajo, conectividad, adecuaciones)
--   wellbeing.alert               bandeja de alertas (inasistencia, riesgo, expediente…)
--   wellbeing.intervention        seguimiento del tutor
--   wellbeing.support_action      plan de apoyo individual
--   wellbeing.protection_report   reportes confidenciales (acceso restringido)
--   comms.*                       comunicados, mensajería auditada y notificaciones

CREATE SCHEMA IF NOT EXISTS wellbeing;
CREATE SCHEMA IF NOT EXISTS comms;

CREATE TABLE wellbeing.student_profile (
    institution_id uuid NOT NULL,
    student_id     uuid PRIMARY KEY,
    motivation     text,
    family_context text,
    learning_style text,
    adaptations    text,
    connectivity   text,
    device         text,
    updated_by     uuid REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE
);

-- Una alerta puede apuntar a un estudiante, a un aula (ej. retroalimentación
-- pendiente en Matemática) o a ninguno si es institucional.
CREATE TABLE wellbeing.alert (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    student_id     uuid,
    classroom_id   uuid,
    kind           text NOT NULL CHECK (kind IN (
                       'absence', 'inactivity', 'dropout_risk', 'academic', 'enrollment_document',
                       'feedback_pending', 'payment', 'other')),
    severity       text NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    source         text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'rule', 'ai')),
    title          text NOT NULL,
    detail         text,
    status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'resolved', 'dismissed')),
    assigned_to    uuid REFERENCES iam.account (id),
    created_by     uuid REFERENCES iam.account (id),
    resolved_by    uuid REFERENCES iam.account (id),
    resolved_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    CHECK (status NOT IN ('resolved', 'dismissed') OR resolved_at IS NOT NULL)
);

CREATE INDEX alert_open_idx ON wellbeing.alert (institution_id, severity, created_at DESC) WHERE status IN ('open', 'in_progress');
CREATE INDEX alert_student_idx ON wellbeing.alert (student_id);
CREATE INDEX alert_assigned_idx ON wellbeing.alert (assigned_to) WHERE status IN ('open', 'in_progress');

CREATE TABLE wellbeing.intervention (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    student_id        uuid NOT NULL,
    alert_id          uuid,
    tutor_id          uuid NOT NULL REFERENCES iam.account (id),
    kind              text NOT NULL CHECK (kind IN (
                          'call', 'meeting', 'message', 'home_visit', 'group_tutoring', 'diagnostic')),
    channel           text NOT NULL CHECK (channel IN ('phone', 'meet', 'whatsapp', 'sms', 'email', 'in_person')),
    occurred_at       timestamptz NOT NULL,
    summary           text NOT NULL,
    agreements        text,
    outcome           text NOT NULL DEFAULT 'registered'
                      CHECK (outcome IN ('registered', 'follow_up_pending', 'commitment_signed', 'closed', 'unreachable')),
    next_follow_up_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, alert_id) REFERENCES wellbeing.alert (institution_id, id) ON DELETE SET NULL (alert_id)
);

CREATE INDEX intervention_student_idx ON wellbeing.intervention (student_id, occurred_at DESC);

CREATE TABLE wellbeing.support_action (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    student_id     uuid NOT NULL,
    period_id      uuid NOT NULL,
    description    text NOT NULL,
    due_on         date,
    status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'cancelled')),
    owner_id       uuid REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id)
);

CREATE INDEX support_action_student_idx ON wellbeing.support_action (student_id, period_id);

-- Reportes confidenciales: acoso, contacto inapropiado, etc. Solo los leen
-- cuentas con wellbeing.cases; cada cambio queda en la bitácora.
CREATE TABLE wellbeing.protection_report (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id      uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    code                text NOT NULL,
    -- Null cuando el reporte es anónimo.
    reporter_id         uuid REFERENCES iam.account (id),
    affected_student_id uuid,
    reported_account_id uuid REFERENCES iam.account (id),
    category            text NOT NULL CHECK (category IN (
                            'bullying', 'harassment', 'inappropriate_contact', 'discrimination',
                            'self_harm', 'violence', 'other')),
    description         text NOT NULL,
    status              text NOT NULL DEFAULT 'received'
                        CHECK (status IN ('received', 'investigating', 'referred', 'resolved', 'dismissed')),
    -- Registro en el portal SíseVe del MINEDU, cuando corresponde.
    siseve_code         text,
    assigned_to         uuid REFERENCES iam.account (id),
    resolution          text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, code),
    FOREIGN KEY (institution_id, affected_student_id) REFERENCES core.student (institution_id, id)
);

CREATE INDEX protection_report_status_idx ON wellbeing.protection_report (institution_id, status, created_at DESC);

-- ---------------------------------------------------------------------------
-- Comunicación
-- ---------------------------------------------------------------------------

CREATE TABLE comms.announcement (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    title          text NOT NULL,
    body           text NOT NULL,
    audience       text NOT NULL DEFAULT 'all' CHECK (audience IN ('all', 'role', 'section', 'classroom')),
    role_code      text REFERENCES iam.role (code),
    section_id     uuid,
    classroom_id   uuid,
    pinned         boolean NOT NULL DEFAULT false,
    published_at   timestamptz,
    expires_at     timestamptz,
    author_id      uuid NOT NULL REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (institution_id, section_id) REFERENCES academic.section (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    CHECK (audience <> 'role' OR role_code IS NOT NULL),
    CHECK (audience <> 'section' OR section_id IS NOT NULL),
    CHECK (audience <> 'classroom' OR classroom_id IS NOT NULL)
);

CREATE INDEX announcement_published_idx ON comms.announcement (institution_id, published_at DESC) WHERE published_at IS NOT NULL;

CREATE TABLE comms.conversation (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('direct', 'classroom', 'support')),
    classroom_id   uuid,
    subject        text,
    created_by     uuid NOT NULL REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id)
        ON DELETE SET NULL (classroom_id)
);

CREATE TABLE comms.participant (
    institution_id  uuid NOT NULL,
    conversation_id uuid NOT NULL,
    account_id      uuid NOT NULL REFERENCES iam.account (id),
    joined_at       timestamptz NOT NULL DEFAULT now(),
    last_read_at    timestamptz,
    PRIMARY KEY (conversation_id, account_id),
    FOREIGN KEY (institution_id, conversation_id) REFERENCES comms.conversation (institution_id, id) ON DELETE CASCADE
);

CREATE INDEX participant_account_idx ON comms.participant (account_id);

-- Mensajería auditada: el contenido nunca se borra físicamente, solo se marca;
-- ediciones y marcados quedan en audit.change_log.
CREATE TABLE comms.message (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id  uuid NOT NULL,
    conversation_id uuid NOT NULL,
    sender_id       uuid NOT NULL REFERENCES iam.account (id),
    body            text NOT NULL,
    sent_at         timestamptz NOT NULL DEFAULT now(),
    edited_at       timestamptz,
    deleted_at      timestamptz,
    flagged_at      timestamptz,
    flag_reason     text,
    moderated_by    uuid REFERENCES iam.account (id),
    FOREIGN KEY (institution_id, conversation_id) REFERENCES comms.conversation (institution_id, id) ON DELETE CASCADE
);

CREATE INDEX message_conversation_idx ON comms.message (conversation_id, sent_at);
CREATE INDEX message_flagged_idx ON comms.message (institution_id, flagged_at) WHERE flagged_at IS NOT NULL;

CREATE TABLE comms.notification (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES iam.account (id) ON DELETE CASCADE,
    kind           text NOT NULL,
    title          text NOT NULL,
    body           text,
    link           text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    read_at        timestamptz
);

CREATE INDEX notification_unread_idx ON comms.notification (account_id, created_at DESC) WHERE read_at IS NULL;

SELECT util.attach_updated_at('wellbeing');
SELECT util.attach_updated_at('comms');

SELECT audit.attach('wellbeing.intervention', 'UPDATE OR DELETE');
SELECT audit.attach('wellbeing.protection_report');
SELECT audit.attach('comms.message', 'UPDATE OR DELETE');
