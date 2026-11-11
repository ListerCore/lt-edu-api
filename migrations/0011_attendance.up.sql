-- Attendance (phase 5 of docs/plan-academico.md): ID card scans at the gate, the daily attendance of every student
-- and of the staff, attendance per class taken by the teacher, and the justifications families send
CREATE SCHEMA IF NOT EXISTS attendance;

-- How attendance is judged: minutes after the first class that still count as on time, and the share of absences
-- that raises an alert (institutes: the unit cannot be passed above it, usually 30 %)
ALTER TABLE academic.settings
    ADD COLUMN late_after_minutes smallint NOT NULL DEFAULT 10,
    ADD COLUMN absence_limit_percent smallint NOT NULL DEFAULT 30,
    ADD CONSTRAINT settings_late_after_check CHECK (late_after_minutes BETWEEN 0 AND 120),
    ADD CONSTRAINT settings_absence_limit_check CHECK (absence_limit_percent BETWEEN 1 AND 100);

-- The secret printed in the QR of each ID card ("JSPH1-E-<token>" students, "JSPH1-P-<token>" staff): random, so a
-- card cannot be forged from the code or DNI, and replaceable when a card is lost
ALTER TABLE people.students ADD COLUMN card_token varchar(40) NOT NULL DEFAULT replace(gen_random_uuid()::text, '-', '');
ALTER TABLE people.students ADD CONSTRAINT students_card_token_key UNIQUE (card_token);
ALTER TABLE people.staff ADD COLUMN card_token varchar(40) NOT NULL DEFAULT replace(gen_random_uuid()::text, '-', '');
ALTER TABLE people.staff ADD CONSTRAINT staff_card_token_key UNIQUE (card_token);

-- Every read of an ID card at the gate (or a manual entry), student or staff
CREATE TABLE attendance.gate_scans (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid REFERENCES people.students (id) ON DELETE CASCADE,
    staff_id uuid REFERENCES people.staff (id) ON DELETE CASCADE,
    scanned_at timestamptz NOT NULL DEFAULT now(),
    -- qr | manual
    source varchar(20) NOT NULL DEFAULT 'qr',
    recorded_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    CONSTRAINT gate_scans_person_check CHECK ((student_id IS NULL) <> (staff_id IS NULL)),
    CONSTRAINT gate_scans_source_check CHECK (source IN ('qr', 'manual'))
);

CREATE INDEX idx_attendance_gate_scans_recent ON attendance.gate_scans (institution_id, scanned_at DESC);

-- The day of a student: present, late, absent or justified. One per student and date
CREATE TABLE attendance.daily_marks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    day date NOT NULL,
    status varchar(20) NOT NULL,
    arrived_at timestamptz,
    -- gate | manual | closing (absent when the day was closed) | justification
    source varchar(20) NOT NULL,
    note varchar(300) NOT NULL DEFAULT '',
    updated_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT daily_marks_student_day_key UNIQUE (student_id, day),
    CONSTRAINT daily_marks_status_check CHECK (status IN ('present', 'late', 'absent', 'justified')),
    CONSTRAINT daily_marks_source_check CHECK (source IN ('gate', 'manual', 'closing', 'justification'))
);

CREATE INDEX idx_attendance_daily_marks_day ON attendance.daily_marks (institution_id, day);

-- The day of a staff member
CREATE TABLE attendance.staff_marks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    staff_id uuid NOT NULL REFERENCES people.staff (id) ON DELETE CASCADE,
    day date NOT NULL,
    status varchar(20) NOT NULL,
    arrived_at timestamptz,
    source varchar(20) NOT NULL,
    note varchar(300) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT staff_marks_staff_day_key UNIQUE (staff_id, day),
    CONSTRAINT staff_marks_status_check CHECK (status IN ('present', 'late', 'absent', 'justified')),
    CONSTRAINT staff_marks_source_check CHECK (source IN ('gate', 'manual', 'closing'))
);

CREATE INDEX idx_attendance_staff_marks_day ON attendance.staff_marks (institution_id, day);

-- A class that took attendance: a subject of a section on a date and hour
CREATE TABLE attendance.class_sessions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL REFERENCES academic.assignments (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    day date NOT NULL,
    block_id uuid NOT NULL REFERENCES academic.time_blocks (id) ON DELETE CASCADE,
    topic varchar(200) NOT NULL DEFAULT '',
    taken_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    taken_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT class_sessions_slot_key UNIQUE (assignment_id, day, block_id)
);

CREATE INDEX idx_attendance_class_sessions_section ON attendance.class_sessions (section_id, day);

CREATE TABLE attendance.class_marks (
    session_id uuid NOT NULL REFERENCES attendance.class_sessions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    status varchar(20) NOT NULL,
    note varchar(200) NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, student_id),
    CONSTRAINT class_marks_status_check CHECK (status IN ('present', 'late', 'absent', 'justified'))
);

CREATE INDEX idx_attendance_class_marks_student ON attendance.class_marks (student_id);

-- A family (or the institution) explains absences between two dates; approving it turns them into justified
CREATE TABLE attendance.justifications (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    from_day date NOT NULL,
    to_day date NOT NULL,
    -- health | family | procedure | other
    reason varchar(20) NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    -- pending | approved | rejected
    status varchar(20) NOT NULL DEFAULT 'pending',
    submitted_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    submitted_name varchar(160) NOT NULL DEFAULT '',
    submitted_at timestamptz NOT NULL DEFAULT now(),
    reviewed_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    reviewed_name varchar(160) NOT NULL DEFAULT '',
    reviewed_at timestamptz,
    review_note varchar(500) NOT NULL DEFAULT '',
    CONSTRAINT justifications_dates_check CHECK (to_day >= from_day AND to_day - from_day <= 60),
    CONSTRAINT justifications_reason_check CHECK (reason IN ('health', 'family', 'procedure', 'other')),
    CONSTRAINT justifications_status_check CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT justifications_review_check CHECK ((status = 'pending') = (reviewed_at IS NULL))
);

CREATE INDEX idx_attendance_justifications_status ON attendance.justifications (institution_id, status, submitted_at DESC);
CREATE INDEX idx_attendance_justifications_student ON attendance.justifications (student_id, from_day DESC);
