-- Live sessions (clases en vivo): every live class, tutoring and psychology appointment says where the call happens.
-- platform: video inside ListerCore Edu (LiveKit), the API hands each allowed person a short-lived token · meet: an
-- external link (Google Meet or another service) the host pastes
ALTER TABLE classroom.items ADD COLUMN provider varchar(10) NOT NULL DEFAULT 'meet',
    ADD CONSTRAINT items_provider_check CHECK (provider IN ('platform', 'meet'));

ALTER TABLE wellbeing.tutoring_sessions ADD COLUMN provider varchar(10) NOT NULL DEFAULT 'meet',
    ADD CONSTRAINT tutoring_sessions_provider_check CHECK (provider IN ('platform', 'meet'));

-- A psychology appointment with the student of a case (confidential: only psychology lists them; the student sees
-- the date, the time and how to join, never the case)
CREATE TABLE wellbeing.psych_appointments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    case_id uuid NOT NULL REFERENCES wellbeing.psych_cases (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    scheduled_at timestamptz NOT NULL,
    duration_min smallint NOT NULL DEFAULT 45,
    -- virtual | in_person
    mode varchar(20) NOT NULL DEFAULT 'virtual',
    provider varchar(10) NOT NULL DEFAULT 'platform',
    link varchar(500) NOT NULL DEFAULT '',
    -- scheduled | held | missed | cancelled
    status varchar(20) NOT NULL DEFAULT 'scheduled',
    host_user_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT psych_appointments_mode_check CHECK (mode IN ('virtual', 'in_person')),
    CONSTRAINT psych_appointments_provider_check CHECK (provider IN ('platform', 'meet')),
    CONSTRAINT psych_appointments_status_check CHECK (status IN ('scheduled', 'held', 'missed', 'cancelled')),
    CONSTRAINT psych_appointments_duration_check CHECK (duration_min BETWEEN 5 AND 240),
    CONSTRAINT psych_appointments_link_check CHECK (link = '' OR link ~ '^https://[^\s<>"]+$')
);

CREATE INDEX idx_wellbeing_psych_appointments_case ON wellbeing.psych_appointments (case_id, scheduled_at);
CREATE INDEX idx_wellbeing_psych_appointments_student ON wellbeing.psych_appointments (student_id, scheduled_at);
CREATE INDEX idx_wellbeing_psych_appointments_date ON wellbeing.psych_appointments (institution_id, scheduled_at);

CREATE TRIGGER psych_appointments_touch_updated_at BEFORE UPDATE ON wellbeing.psych_appointments
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Who joined each call, from the platform (both providers: joining a Meet goes through the platform first)
CREATE SCHEMA live;

CREATE TABLE live.joins (
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- class (classroom.items) | tutoring (wellbeing.tutoring_sessions) | psychology (wellbeing.psych_appointments)
    source varchar(20) NOT NULL,
    source_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    student_id uuid REFERENCES people.students (id) ON DELETE SET NULL,
    display_name varchar(160) NOT NULL DEFAULT '',
    first_at timestamptz NOT NULL DEFAULT now(),
    last_at timestamptz NOT NULL DEFAULT now(),
    joins integer NOT NULL DEFAULT 1,
    PRIMARY KEY (source, source_id, user_id),
    CONSTRAINT joins_source_check CHECK (source IN ('class', 'tutoring', 'psychology'))
);

CREATE INDEX idx_live_joins_institution ON live.joins (institution_id, first_at);
