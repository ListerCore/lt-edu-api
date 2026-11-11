-- People and enrollment (phase 3 of docs/plan-academico.md): the students of each institution and their guardians,
-- who is linked to whom, the enrollment of a student in a period (grade and section, or CEPRE area and classroom,
-- or institute semester with its didactic units), and the EBA placement tests and validations
CREATE SCHEMA IF NOT EXISTS people;
CREATE SCHEMA IF NOT EXISTS enrollment;

CREATE TABLE people.students (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- Student code printed on the ID card ("2026-0412"), unique in the institution
    code varchar(20) NOT NULL,
    -- dni | ce | passport | other
    document_type varchar(20) NOT NULL,
    document_number varchar(20) NOT NULL,
    given_names varchar(80) NOT NULL,
    paternal_surname varchar(60) NOT NULL,
    maternal_surname varchar(60) NOT NULL DEFAULT '',
    birth_date date,
    -- f | m
    sex varchar(1),
    email varchar(254),
    phone varchar(20) NOT NULL DEFAULT '',
    address varchar(200) NOT NULL DEFAULT '',
    -- Special educational needs, with or without disability (EBE students, inclusion in EBR/EBA)
    special_needs boolean NOT NULL DEFAULT false,
    notes varchar(1000) NOT NULL DEFAULT '',
    -- active | inactive | graduated | withdrawn
    status varchar(20) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT students_code_key UNIQUE (institution_id, code),
    CONSTRAINT students_document_key UNIQUE (institution_id, document_type, document_number),
    CONSTRAINT students_document_type_check CHECK (document_type IN ('dni', 'ce', 'passport', 'other')),
    CONSTRAINT students_dni_format CHECK (document_type <> 'dni' OR document_number ~ '^[0-9]{8}$'),
    CONSTRAINT students_names_required CHECK (btrim(given_names) <> '' AND btrim(paternal_surname) <> ''),
    CONSTRAINT students_sex_check CHECK (sex IS NULL OR sex IN ('f', 'm')),
    CONSTRAINT students_email_format CHECK (email IS NULL OR email ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$'),
    CONSTRAINT students_status_check CHECK (status IN ('active', 'inactive', 'graduated', 'withdrawn'))
);

CREATE TRIGGER students_touch_updated_at BEFORE UPDATE ON people.students
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_people_students_institution ON people.students (institution_id, paternal_surname, maternal_surname, given_names);
CREATE INDEX idx_people_students_email ON people.students (lower(email)) WHERE email IS NOT NULL;

-- Parents, legal guardians or whoever answers for the student. One row per person and institution: siblings share it
CREATE TABLE people.guardians (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    document_type varchar(20) NOT NULL,
    document_number varchar(20) NOT NULL,
    given_names varchar(80) NOT NULL,
    surnames varchar(120) NOT NULL,
    email varchar(254),
    phone varchar(20) NOT NULL DEFAULT '',
    occupation varchar(80) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT guardians_document_key UNIQUE (institution_id, document_type, document_number),
    CONSTRAINT guardians_document_type_check CHECK (document_type IN ('dni', 'ce', 'passport', 'other')),
    CONSTRAINT guardians_dni_format CHECK (document_type <> 'dni' OR document_number ~ '^[0-9]{8}$'),
    CONSTRAINT guardians_names_required CHECK (btrim(given_names) <> '' AND btrim(surnames) <> ''),
    CONSTRAINT guardians_email_format CHECK (email IS NULL OR email ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$')
);

CREATE TRIGGER guardians_touch_updated_at BEFORE UPDATE ON people.guardians
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_people_guardians_email ON people.guardians (lower(email)) WHERE email IS NOT NULL;

-- Who answers for whom: the relationship, the main contact (one per student) and who may pick the student up
CREATE TABLE people.student_guardians (
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    guardian_id uuid NOT NULL REFERENCES people.guardians (id) ON DELETE CASCADE,
    -- mother | father | legal_guardian | grandparent | sibling | other
    relationship varchar(20) NOT NULL,
    is_primary boolean NOT NULL DEFAULT false,
    can_pick_up boolean NOT NULL DEFAULT true,
    lives_with boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (student_id, guardian_id),
    CONSTRAINT student_guardians_relationship_check CHECK (relationship IN ('mother', 'father', 'legal_guardian', 'grandparent', 'sibling', 'other'))
);

CREATE UNIQUE INDEX student_guardians_one_primary ON people.student_guardians (student_id) WHERE is_primary;
CREATE INDEX idx_people_student_guardians_guardian ON people.student_guardians (guardian_id);

-- A student in a period: grade and section (Educación Básica), area and classroom (CEPRE) or semester and section
-- (institutes). One per student and period
CREATE TABLE enrollment.enrollments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    grade_id uuid REFERENCES academic.grades (id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    -- The section may be assigned later; a section that is deleted leaves the enrollment unassigned
    section_id uuid REFERENCES academic.sections (id) ON DELETE SET NULL,
    -- new: first time in the institution · continuing: was here the previous period · transfer_in: comes from
    -- another institution · re_entry: comes back after leaving
    kind varchar(20) NOT NULL,
    -- active · withdrawn: left during the period · transferred: moved to another institution · completed: finished
    status varchar(20) NOT NULL DEFAULT 'active',
    enrolled_on date NOT NULL,
    ended_on date,
    -- Institution it comes from (transfer_in) or goes to (transferred)
    origin varchar(160) NOT NULL DEFAULT '',
    destination varchar(160) NOT NULL DEFAULT '',
    notes varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT enrollments_student_period_key UNIQUE (student_id, period_id),
    CONSTRAINT enrollments_kind_check CHECK (kind IN ('new', 'continuing', 'transfer_in', 're_entry')),
    CONSTRAINT enrollments_status_check CHECK (status IN ('active', 'withdrawn', 'transferred', 'completed')),
    CONSTRAINT enrollments_end_check CHECK ((status = 'active') = (ended_on IS NULL) OR status = 'completed'),
    CONSTRAINT enrollments_dates_check CHECK (ended_on IS NULL OR ended_on >= enrolled_on)
);

CREATE TRIGGER enrollments_touch_updated_at BEFORE UPDATE ON enrollment.enrollments
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_enrollment_enrollments_period ON enrollment.enrollments (period_id, level_id, grade_id, section_id);
CREATE INDEX idx_enrollment_enrollments_section ON enrollment.enrollments (section_id) WHERE status = 'active';
CREATE INDEX idx_enrollment_enrollments_student ON enrollment.enrollments (student_id);

-- Institutes enroll by didactic unit: the ones of the semester, plus pending ones from earlier semesters
CREATE TABLE enrollment.unit_enrollments (
    enrollment_id uuid NOT NULL REFERENCES enrollment.enrollments (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (enrollment_id, subject_id)
);

CREATE INDEX idx_enrollment_unit_enrollments_subject ON enrollment.unit_enrollments (subject_id);

-- EBA placement test (no certificates: a test decides the cycle and grade) and validation of grades passed elsewhere
CREATE TABLE enrollment.placements (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- placement_test | validation
    kind varchar(20) NOT NULL,
    -- Cycle and grade the student is placed in (or validated up to)
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE CASCADE,
    grade_id uuid NOT NULL REFERENCES academic.grades (id) ON DELETE CASCADE,
    decided_on date NOT NULL,
    -- Resolution, certificate or test reference
    reference varchar(120) NOT NULL DEFAULT '',
    notes varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT placements_kind_check CHECK (kind IN ('placement_test', 'validation'))
);

CREATE INDEX idx_enrollment_placements_student ON enrollment.placements (student_id, decided_on DESC);
CREATE INDEX idx_enrollment_placements_institution ON enrollment.placements (institution_id, decided_on DESC);
