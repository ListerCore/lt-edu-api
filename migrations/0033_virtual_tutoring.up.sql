-- Phase E6 (docs/ceba/PLAN.md): virtual tutoring. The tutor of a student is the tutor of their section unless the
-- student has one of their own; tutoring sessions (individual or group) are scheduled, held and leave an interview
-- per student who attended (which counts as activity for the permanence); tutor and student write to each other
-- inside the platform; and a student can be referred to the SAANEE with its follow-up

-- A tutor of their own for a student in a period, over the tutor of the section
CREATE TABLE wellbeing.tutor_assignments (
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    staff_id uuid NOT NULL REFERENCES people.staff (id) ON DELETE CASCADE,
    assigned_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (period_id, student_id)
);

CREATE INDEX idx_wellbeing_tutor_assignments_staff ON wellbeing.tutor_assignments (staff_id, period_id);

-- A tutoring session: individual (one student) or group (several, or a whole section)
CREATE TABLE wellbeing.tutoring_sessions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    staff_id uuid NOT NULL REFERENCES people.staff (id) ON DELETE CASCADE,
    -- individual | group
    kind varchar(20) NOT NULL,
    -- virtual | in_person | phone
    mode varchar(20) NOT NULL DEFAULT 'virtual',
    scheduled_at timestamptz NOT NULL,
    duration_min smallint NOT NULL DEFAULT 30,
    -- Video call of a virtual session
    link varchar(500) NOT NULL DEFAULT '',
    reason varchar(300) NOT NULL,
    -- The dropout alert it answers (inactive | overdue | low_permanence), when it came from one
    alert_kind varchar(20) NOT NULL DEFAULT '',
    -- scheduled | held | missed | cancelled
    status varchar(20) NOT NULL DEFAULT 'scheduled',
    -- What was talked about (staff only) and what was agreed (the student reads it)
    notes varchar(4000) NOT NULL DEFAULT '',
    agreements varchar(2000) NOT NULL DEFAULT '',
    held_at timestamptz,
    created_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tutoring_sessions_kind_check CHECK (kind IN ('individual', 'group')),
    CONSTRAINT tutoring_sessions_mode_check CHECK (mode IN ('virtual', 'in_person', 'phone')),
    CONSTRAINT tutoring_sessions_status_check CHECK (status IN ('scheduled', 'held', 'missed', 'cancelled')),
    CONSTRAINT tutoring_sessions_alert_check CHECK (alert_kind IN ('', 'inactive', 'overdue', 'low_permanence')),
    CONSTRAINT tutoring_sessions_duration_check CHECK (duration_min BETWEEN 5 AND 240),
    CONSTRAINT tutoring_sessions_held_check CHECK ((status = 'held') = (held_at IS NOT NULL))
);

CREATE INDEX idx_wellbeing_tutoring_sessions_staff ON wellbeing.tutoring_sessions (staff_id, scheduled_at);
CREATE INDEX idx_wellbeing_tutoring_sessions_date ON wellbeing.tutoring_sessions (institution_id, scheduled_at);

CREATE TRIGGER tutoring_sessions_touch_updated_at BEFORE UPDATE ON wellbeing.tutoring_sessions
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Who is called to a session; once held, whether they came and the interview it left in their file
CREATE TABLE wellbeing.tutoring_attendees (
    session_id uuid NOT NULL REFERENCES wellbeing.tutoring_sessions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    attended boolean,
    interview_id uuid REFERENCES wellbeing.interviews (id) ON DELETE SET NULL,
    PRIMARY KEY (session_id, student_id)
);

CREATE INDEX idx_wellbeing_tutoring_attendees_student ON wellbeing.tutoring_attendees (student_id);

-- A conversation between a student and a staff member (their tutor), with when each side last read it
CREATE TABLE wellbeing.threads (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    staff_id uuid NOT NULL REFERENCES people.staff (id) ON DELETE CASCADE,
    last_message_at timestamptz,
    staff_read_at timestamptz,
    student_read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT threads_key UNIQUE (student_id, staff_id)
);

CREATE INDEX idx_wellbeing_threads_staff ON wellbeing.threads (staff_id, last_message_at DESC);

-- Messages are kept as written: no edits, no deletes (they are the record of the tutoring)
CREATE TABLE wellbeing.messages (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    thread_id uuid NOT NULL REFERENCES wellbeing.threads (id) ON DELETE CASCADE,
    -- staff | student
    side varchar(10) NOT NULL,
    author_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    author_name varchar(160) NOT NULL DEFAULT '',
    body varchar(2000) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT messages_side_check CHECK (side IN ('staff', 'student')),
    CONSTRAINT messages_body_check CHECK (btrim(body) <> '')
);

CREATE INDEX idx_wellbeing_messages_thread ON wellbeing.messages (thread_id, created_at);

-- A student's message counts as activity in the platform (no room)
CREATE FUNCTION attendance.on_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_student uuid;
BEGIN
    IF NEW.side = 'student' THEN
        SELECT student_id INTO v_student FROM wellbeing.threads WHERE id = NEW.thread_id;
        PERFORM attendance.record_activity(v_student, NULL, NULL, 'post', NEW.created_at);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER messages_activity AFTER INSERT ON wellbeing.messages
    FOR EACH ROW EXECUTE FUNCTION attendance.on_message();

-- Referral to the SAANEE (Servicio de Apoyo y Asesoramiento para la Atención de las Necesidades Educativas
-- Especiales) of the jurisdiction, and its follow-up
CREATE TABLE wellbeing.saanee_referrals (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- What the tutor observed
    reason varchar(2000) NOT NULL,
    -- sent | received | attending | closed
    status varchar(20) NOT NULL DEFAULT 'sent',
    sent_on date NOT NULL,
    -- Oficio or file number
    reference varchar(120) NOT NULL DEFAULT '',
    -- What the SAANEE answered or recommends
    response varchar(2000) NOT NULL DEFAULT '',
    referred_name varchar(160) NOT NULL DEFAULT '',
    updated_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saanee_referrals_status_check CHECK (status IN ('sent', 'received', 'attending', 'closed'))
);

CREATE INDEX idx_wellbeing_saanee_referrals_student ON wellbeing.saanee_referrals (student_id, sent_on DESC);

CREATE TRIGGER saanee_referrals_touch_updated_at BEFORE UPDATE ON wellbeing.saanee_referrals
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();
