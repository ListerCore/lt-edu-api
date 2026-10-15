-- Aula virtual (RSG N.° 613-2014-MINEDU, Anexo 2 · sección de aula virtual) y
-- asistencia / permanencia (sección informativa).
--
--   classroom ─< class_session ─< attendance.record
--   classroom ─< resource         materiales, videos, enlaces, simuladores
--   classroom ─< assignment ─< submission   tareas y evidencias
--   classroom ─< forum ─< forum_post        foros moderables (protección estudiantil)
--
-- ON DELETE SET NULL (col) limpia solo la referencia y deja institution_id.

CREATE SCHEMA IF NOT EXISTS lms;
CREATE SCHEMA IF NOT EXISTS attendance;

CREATE TABLE lms.class_session (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    classroom_id   uuid NOT NULL,
    term_id        uuid,
    unit_number    smallint CHECK (unit_number > 0),
    number         smallint CHECK (number > 0),
    title          text NOT NULL,
    purpose        text,
    mode           text NOT NULL DEFAULT 'synchronous' CHECK (mode IN ('synchronous', 'asynchronous')),
    starts_at      timestamptz,
    ends_at        timestamptz,
    meeting_url    text,
    recording_url  text,
    status         text NOT NULL DEFAULT 'draft'
                   CHECK (status IN ('draft', 'scheduled', 'live', 'finished', 'cancelled')),
    created_by     uuid REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, term_id) REFERENCES academic.term (institution_id, id),
    CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at),
    CHECK (mode = 'asynchronous' OR status = 'draft' OR starts_at IS NOT NULL)
);

CREATE INDEX class_session_classroom_idx ON lms.class_session (classroom_id, starts_at);

CREATE TABLE lms.resource (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id   uuid NOT NULL,
    classroom_id     uuid NOT NULL,
    class_session_id uuid,
    kind             text NOT NULL
                     CHECK (kind IN ('document', 'video', 'audio', 'link', 'simulator', 'spreadsheet', 'other')),
    title            text NOT NULL,
    description      text,
    url              text,
    storage_key      text,
    status           text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    published_at     timestamptz,
    created_by       uuid REFERENCES iam.account (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, class_session_id) REFERENCES lms.class_session (institution_id, id)
        ON DELETE SET NULL (class_session_id),
    CHECK (url IS NOT NULL OR storage_key IS NOT NULL),
    CHECK (status <> 'published' OR published_at IS NOT NULL)
);

CREATE INDEX resource_classroom_idx ON lms.resource (classroom_id, status);

CREATE TABLE lms.assignment (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    classroom_id      uuid NOT NULL,
    class_session_id  uuid,
    competency_number smallint REFERENCES curriculum.competency (number),
    kind              text NOT NULL DEFAULT 'task' CHECK (kind IN ('task', 'quiz', 'project', 'oral')),
    title             text NOT NULL,
    instructions      text,
    opens_at          timestamptz,
    due_at            timestamptz,
    -- Adecuaciones EBA: muchos estudiantes trabajan, se aceptan entregas tardías.
    accepts_late      boolean NOT NULL DEFAULT true,
    status            text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'closed')),
    created_by        uuid REFERENCES iam.account (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, class_session_id) REFERENCES lms.class_session (institution_id, id)
        ON DELETE SET NULL (class_session_id),
    CHECK (due_at IS NULL OR opens_at IS NULL OR due_at > opens_at)
);

CREATE INDEX assignment_classroom_idx ON lms.assignment (classroom_id, due_at);

CREATE TABLE lms.submission (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    assignment_id  uuid NOT NULL,
    student_id     uuid NOT NULL,
    attempt        smallint NOT NULL DEFAULT 1 CHECK (attempt > 0),
    body           text,
    -- Foto del cuaderno, nota de voz, PDF…
    storage_key    text,
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    is_late        boolean NOT NULL DEFAULT false,
    status         text NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted', 'returned', 'reviewed')),
    feedback       text,
    feedback_by    uuid REFERENCES iam.account (id),
    feedback_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (assignment_id, student_id, attempt),
    FOREIGN KEY (institution_id, assignment_id) REFERENCES lms.assignment (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    CHECK (body IS NOT NULL OR storage_key IS NOT NULL)
);

CREATE INDEX submission_student_idx ON lms.submission (student_id, submitted_at DESC);
-- Bandeja "por retroalimentar".
CREATE INDEX submission_pending_feedback_idx ON lms.submission (assignment_id) WHERE status = 'submitted';

CREATE TABLE lms.forum (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id   uuid NOT NULL,
    classroom_id     uuid NOT NULL,
    class_session_id uuid,
    title            text NOT NULL,
    prompt           text,
    status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_by       uuid REFERENCES iam.account (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, class_session_id) REFERENCES lms.class_session (institution_id, id)
        ON DELETE SET NULL (class_session_id)
);

CREATE INDEX forum_classroom_idx ON lms.forum (classroom_id);

-- Las publicaciones no se borran: se ocultan, para conservar evidencia.
CREATE TABLE lms.forum_post (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    forum_id       uuid NOT NULL,
    parent_id      uuid,
    author_id      uuid NOT NULL REFERENCES iam.account (id),
    body           text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    edited_at      timestamptz,
    hidden_at      timestamptz,
    hidden_by      uuid REFERENCES iam.account (id),
    hidden_reason  text,
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, forum_id) REFERENCES lms.forum (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, parent_id) REFERENCES lms.forum_post (institution_id, id),
    CHECK ((hidden_at IS NULL) = (hidden_by IS NULL))
);

CREATE INDEX forum_post_forum_idx ON lms.forum_post (forum_id, created_at);

-- ---------------------------------------------------------------------------
-- Asistencia
-- ---------------------------------------------------------------------------

CREATE TABLE attendance.record (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    class_session_id  uuid NOT NULL,
    student_id        uuid NOT NULL,
    status            text NOT NULL CHECK (status IN ('present', 'late', 'absent', 'justified')),
    joined_at         timestamptz,
    connected_minutes smallint CHECK (connected_minutes >= 0),
    participation     text CHECK (participation IN ('high', 'medium', 'low', 'none')),
    note              text,
    justification     text,
    recorded_by       uuid REFERENCES iam.account (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (class_session_id, student_id),
    FOREIGN KEY (institution_id, class_session_id) REFERENCES lms.class_session (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    CHECK (status <> 'justified' OR justification IS NOT NULL)
);

CREATE INDEX attendance_record_student_idx ON attendance.record (student_id, created_at DESC);

-- Seguimiento virtual agregado por día: ingresos, tiempo conectado y entregas.
-- Lo alimenta la API; sirve a la matriz de permanencia y a las alertas.
CREATE TABLE attendance.daily_activity (
    institution_id uuid NOT NULL,
    student_id     uuid NOT NULL,
    activity_date  date NOT NULL,
    logins         integer NOT NULL DEFAULT 0 CHECK (logins >= 0),
    minutes_online integer NOT NULL DEFAULT 0 CHECK (minutes_online >= 0),
    submissions    integer NOT NULL DEFAULT 0 CHECK (submissions >= 0),
    last_seen_at   timestamptz,
    PRIMARY KEY (student_id, activity_date),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE
);

CREATE INDEX daily_activity_date_idx ON attendance.daily_activity (institution_id, activity_date);

SELECT util.attach_updated_at('lms');
SELECT util.attach_updated_at('attendance');

SELECT audit.attach('attendance.record', 'UPDATE OR DELETE');
SELECT audit.attach('lms.forum_post', 'UPDATE OR DELETE');
