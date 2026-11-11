-- Teaching load and timetables (phase 4 of docs/plan-academico.md): the staff of each institution, the tutor of every
-- section, the time blocks of the school day per shift, who teaches each subject in each section (and how many
-- hours) and the weekly timetable of every section

-- Staff: teachers and everyone else who works at the institution. Panel access follows the email, as for students
CREATE TABLE people.staff (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    code varchar(20) NOT NULL,
    document_type varchar(20) NOT NULL,
    document_number varchar(20) NOT NULL,
    given_names varchar(80) NOT NULL,
    surnames varchar(120) NOT NULL,
    email varchar(254),
    phone varchar(20) NOT NULL DEFAULT '',
    -- Job title as the institution names it ("Docente de Matemática", "Secretaria", "Auxiliar de educación")
    position varchar(80) NOT NULL,
    -- Teaches: can be given subjects and sections
    is_teacher boolean NOT NULL DEFAULT false,
    specialty varchar(120) NOT NULL DEFAULT '',
    -- Weekly teaching hours the person is hired for (load above it is flagged)
    max_weekly_hours smallint,
    hired_on date,
    -- active | inactive
    status varchar(20) NOT NULL DEFAULT 'active',
    notes varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT staff_code_key UNIQUE (institution_id, code),
    CONSTRAINT staff_document_key UNIQUE (institution_id, document_type, document_number),
    CONSTRAINT staff_document_type_check CHECK (document_type IN ('dni', 'ce', 'passport', 'other')),
    CONSTRAINT staff_dni_format CHECK (document_type <> 'dni' OR document_number ~ '^[0-9]{8}$'),
    CONSTRAINT staff_names_required CHECK (btrim(given_names) <> '' AND btrim(surnames) <> '' AND btrim(position) <> ''),
    CONSTRAINT staff_email_format CHECK (email IS NULL OR email ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$'),
    CONSTRAINT staff_hours_check CHECK (max_weekly_hours IS NULL OR max_weekly_hours BETWEEN 1 AND 60),
    CONSTRAINT staff_status_check CHECK (status IN ('active', 'inactive'))
);

CREATE TRIGGER staff_touch_updated_at BEFORE UPDATE ON people.staff
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_people_staff_institution ON people.staff (institution_id, surnames, given_names);
CREATE INDEX idx_people_staff_email ON people.staff (lower(email)) WHERE email IS NOT NULL;

-- The tutor of each section
ALTER TABLE academic.sections ADD COLUMN tutor_id uuid REFERENCES people.staff (id) ON DELETE SET NULL;

-- Days the institution teaches (1 = Monday … 7 = Sunday)
ALTER TABLE academic.settings ADD COLUMN week_days smallint[] NOT NULL DEFAULT '{1,2,3,4,5}',
    ADD CONSTRAINT settings_week_days_check CHECK (cardinality(week_days) BETWEEN 1 AND 7 AND week_days <@ ARRAY[1, 2, 3, 4, 5, 6, 7]::smallint[]);

-- The school day of each shift: class hours (horas pedagógicas) and breaks, in order
CREATE TABLE academic.time_blocks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    shift varchar(20) NOT NULL,
    position smallint NOT NULL,
    starts_at time NOT NULL,
    ends_at time NOT NULL,
    -- class | break
    kind varchar(20) NOT NULL DEFAULT 'class',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT time_blocks_position_key UNIQUE (institution_id, shift, position),
    CONSTRAINT time_blocks_shift_check CHECK (shift IN ('morning', 'afternoon', 'evening')),
    CONSTRAINT time_blocks_kind_check CHECK (kind IN ('class', 'break')),
    CONSTRAINT time_blocks_times_check CHECK (ends_at > starts_at)
);

-- Teaching load: who teaches a subject of the plan in a section, and for how many weekly hours
CREATE TABLE academic.assignments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    -- Empty while nobody is assigned yet
    staff_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    weekly_hours smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assignments_section_subject_key UNIQUE (section_id, subject_id),
    CONSTRAINT assignments_hours_check CHECK (weekly_hours BETWEEN 0 AND 40)
);

CREATE TRIGGER assignments_touch_updated_at BEFORE UPDATE ON academic.assignments
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_assignments_period ON academic.assignments (period_id, section_id);
CREATE INDEX idx_academic_assignments_staff ON academic.assignments (staff_id) WHERE staff_id IS NOT NULL;

-- Weekly timetable: an assignment taught on a day in a time block (and room). One per section, day and block
CREATE TABLE academic.schedule_entries (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL REFERENCES academic.assignments (id) ON DELETE CASCADE,
    weekday smallint NOT NULL,
    block_id uuid NOT NULL REFERENCES academic.time_blocks (id) ON DELETE CASCADE,
    room varchar(60) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schedule_entries_slot_key UNIQUE (section_id, weekday, block_id),
    CONSTRAINT schedule_entries_weekday_check CHECK (weekday BETWEEN 1 AND 7)
);

CREATE INDEX idx_academic_schedule_entries_period ON academic.schedule_entries (period_id, weekday, block_id);
CREATE INDEX idx_academic_schedule_entries_assignment ON academic.schedule_entries (assignment_id);
