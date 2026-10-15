-- Admisión y matrícula.
--
--   application   postulación / preinscripción: persona + antecedentes
--   placement     EBA: evaluación de ubicación o convalidación (RVM N.° 125-2020-MINEDU);
--                 EBR: evaluación de traslado o de ubicación cuando aplica
--   enrollment    matrícula del estudiante en periodo, grado y sección (FUM, SIAGIE)
--   document      expediente digital del estudiante o del postulante

CREATE SCHEMA IF NOT EXISTS enrollment;

CREATE TABLE enrollment.application (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id       uuid NOT NULL,
    person_id            uuid NOT NULL,
    period_id            uuid NOT NULL,
    requested_grade_code text REFERENCES curriculum.grade (code),
    -- Antecedente educativo declarado: "2.° Sec. EBR incompleta".
    prior_system         text NOT NULL DEFAULT 'none' CHECK (prior_system IN ('ebr', 'eba', 'none', 'other')),
    last_grade_completed text,
    last_institution     text,
    last_study_year      smallint CHECK (last_study_year BETWEEN 1950 AND 2100),
    -- Ficha socioeconómica y perfil laboral: estructura flexible mientras se
    -- define el formulario definitivo.
    socioeconomic        jsonb NOT NULL DEFAULT '{}',
    status               text NOT NULL DEFAULT 'submitted'
                         CHECK (status IN ('submitted', 'in_review', 'evaluated', 'approved', 'rejected', 'enrolled', 'cancelled')),
    notes                text,
    reviewed_by          uuid REFERENCES iam.account (id),
    submitted_at         timestamptz NOT NULL DEFAULT now(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, person_id) REFERENCES core.person (institution_id, id),
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id)
);

-- Una postulación viva por persona y periodo.
CREATE UNIQUE INDEX application_open_unique ON enrollment.application (person_id, period_id)
    WHERE status NOT IN ('rejected', 'cancelled');
CREATE INDEX application_period_status_idx ON enrollment.application (institution_id, period_id, status);

CREATE TABLE enrollment.placement (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id      uuid NOT NULL,
    application_id      uuid NOT NULL,
    kind                text NOT NULL CHECK (kind IN ('diagnostic_test', 'validation')),
    score               numeric(4, 2) CHECK (score BETWEEN 0 AND 20),
    level               curriculum.achievement_level,
    proposed_grade_code text REFERENCES curriculum.grade (code),
    approved_grade_code text REFERENCES curriculum.grade (code),
    -- Número del acta de evaluación de ubicación.
    act_number          text,
    notes               text,
    evaluated_by        uuid,
    evaluated_at        timestamptz,
    approved_by         uuid,
    approved_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, application_id) REFERENCES enrollment.application (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, evaluated_by) REFERENCES core.staff (institution_id, id),
    FOREIGN KEY (institution_id, approved_by) REFERENCES core.staff (institution_id, id),
    CHECK ((approved_grade_code IS NULL) = (approved_at IS NULL))
);

CREATE INDEX placement_application_idx ON enrollment.placement (application_id);

CREATE TABLE enrollment.enrollment (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    student_id        uuid NOT NULL,
    period_id         uuid NOT NULL,
    grade_code        text NOT NULL REFERENCES curriculum.grade (code),
    -- Null mientras no se asigna sección.
    section_id        uuid,
    application_id    uuid,
    -- Código institucional visible: EBA-2026-0842.
    code              text NOT NULL,
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'enrolled', 'withdrawn', 'transferred', 'completed')),
    -- Resultado al cierre del periodo.
    outcome           text CHECK (outcome IN ('promoted', 'retained', 'recovery')),
    enrolled_at       timestamptz,
    withdrawn_at      timestamptz,
    withdrawal_reason text,
    -- Ficha Única de Matrícula.
    fum_number        text,
    fum_issued_at     timestamptz,
    siagie_synced_at  timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, code),
    UNIQUE (institution_id, fum_number),
    UNIQUE (student_id, period_id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id),
    -- La sección asignada debe ser del mismo periodo y grado.
    FOREIGN KEY (institution_id, section_id, period_id, grade_code)
        REFERENCES academic.section (institution_id, id, period_id, grade_code),
    FOREIGN KEY (institution_id, application_id) REFERENCES enrollment.application (institution_id, id),
    CHECK (status <> 'enrolled' OR enrolled_at IS NOT NULL),
    CHECK (status NOT IN ('withdrawn', 'transferred') OR withdrawn_at IS NOT NULL)
);

CREATE INDEX enrollment_section_idx ON enrollment.enrollment (section_id);
CREATE INDEX enrollment_period_status_idx ON enrollment.enrollment (institution_id, period_id, status);

CREATE TABLE enrollment.document (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    student_id     uuid,
    application_id uuid,
    kind           text NOT NULL CHECK (kind IN (
                       'identity', 'study_certificate', 'application_form', 'sworn_statement',
                       'fum', 'placement_act', 'resolution', 'photo', 'transfer', 'other')),
    title          text NOT NULL,
    -- Clave del objeto en el almacenamiento (S3/R2); el binario no va a la base.
    storage_key    text NOT NULL UNIQUE,
    mime_type      text NOT NULL,
    size_bytes     bigint NOT NULL CHECK (size_bytes > 0),
    sha256         bytea NOT NULL,
    status         text NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'validated', 'observed', 'rejected')),
    observation    text,
    uploaded_by    uuid REFERENCES iam.account (id),
    reviewed_by    uuid REFERENCES iam.account (id),
    reviewed_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    FOREIGN KEY (institution_id, application_id) REFERENCES enrollment.application (institution_id, id),
    CHECK (student_id IS NOT NULL OR application_id IS NOT NULL)
);

CREATE INDEX document_student_idx ON enrollment.document (student_id);
CREATE INDEX document_application_idx ON enrollment.document (application_id);
CREATE INDEX document_pending_idx ON enrollment.document (institution_id, created_at) WHERE status IN ('pending', 'observed');

SELECT util.attach_updated_at('enrollment');

SELECT audit.attach('enrollment.placement');
SELECT audit.attach('enrollment.enrollment');
SELECT audit.attach('enrollment.document', 'UPDATE OR DELETE');
