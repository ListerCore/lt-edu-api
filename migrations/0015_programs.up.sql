-- Institutes (phase 9 of docs/plan-academico.md): each student's record in their study program, the work placements
-- (EFSRT) with their hours and the supervisor's evaluation, modular certificates, the degree requirements and the
-- degree itself
CREATE SCHEMA IF NOT EXISTS programs;

-- The coordinator of a study program (IEST): manages its modules, placements and students
INSERT INTO iam.roles (code, name, description, scope)
VALUES ('program_coordinator', 'Coordinador de programa', 'Un programa de estudios del instituto: módulos, experiencias formativas y certificación de sus estudiantes.', 'institution')
ON CONFLICT (code) DO NOTHING;

ALTER TABLE academic.levels ADD COLUMN coordinator_id uuid REFERENCES people.staff (id) ON DELETE SET NULL;

-- Units passed or failed before the platform, or validated from another institution (the platform's own results
-- come from assessment.unit_results)
CREATE TABLE programs.unit_records (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    period_name varchar(80) NOT NULL,
    grade smallint,
    -- approved | failed
    status varchar(20) NOT NULL,
    -- history | validation
    source varchar(20) NOT NULL DEFAULT 'history',
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unit_records_key UNIQUE (student_id, subject_id),
    CONSTRAINT unit_records_status_check CHECK (status IN ('approved', 'failed')),
    CONSTRAINT unit_records_source_check CHECK (source IN ('history', 'validation')),
    CONSTRAINT unit_records_grade_check CHECK (grade IS NULL OR grade BETWEEN 0 AND 20)
);

-- A work placement (Experiencia formativa en situaciones reales de trabajo) of a module
CREATE TABLE programs.internships (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    company varchar(160) NOT NULL,
    company_ruc varchar(11) NOT NULL DEFAULT '',
    area varchar(120) NOT NULL DEFAULT '',
    supervisor varchar(160) NOT NULL DEFAULT '',
    supervisor_contact varchar(160) NOT NULL DEFAULT '',
    starts_on date NOT NULL,
    ends_on date,
    required_hours smallint NOT NULL,
    -- ongoing | approved | failed
    status varchar(20) NOT NULL DEFAULT 'ongoing',
    score smallint,
    evaluation varchar(1000) NOT NULL DEFAULT '',
    evaluated_name varchar(160) NOT NULL DEFAULT '',
    evaluated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT internships_company_required CHECK (btrim(company) <> ''),
    CONSTRAINT internships_ruc_check CHECK (company_ruc = '' OR company_ruc ~ '^[12][0-9]{10}$'),
    CONSTRAINT internships_status_check CHECK (status IN ('ongoing', 'approved', 'failed')),
    CONSTRAINT internships_score_check CHECK (score IS NULL OR score BETWEEN 0 AND 20),
    CONSTRAINT internships_dates_check CHECK (ends_on IS NULL OR ends_on >= starts_on),
    CONSTRAINT internships_hours_check CHECK (required_hours BETWEEN 1 AND 2000)
);

CREATE INDEX idx_programs_internships_student ON programs.internships (student_id, subject_id);
CREATE INDEX idx_programs_internships_status ON programs.internships (institution_id, status);

CREATE TABLE programs.internship_logs (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    internship_id uuid NOT NULL REFERENCES programs.internships (id) ON DELETE CASCADE,
    day date NOT NULL,
    hours numeric(4, 1) NOT NULL,
    activity varchar(300) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT internship_logs_hours_check CHECK (hours > 0 AND hours <= 12)
);

CREATE INDEX idx_programs_internship_logs ON programs.internship_logs (internship_id, day);

-- Modular certificates and degrees, frozen when issued
CREATE TABLE programs.certificates (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE CASCADE,
    -- modular | degree
    kind varchar(20) NOT NULL,
    module_id uuid REFERENCES academic.modules (id) ON DELETE SET NULL,
    number integer NOT NULL,
    code varchar(40) NOT NULL,
    -- issued | annulled
    status varchar(20) NOT NULL DEFAULT 'issued',
    content jsonb NOT NULL,
    issued_on date NOT NULL,
    issued_name varchar(160) NOT NULL DEFAULT '',
    annul_reason varchar(300) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificates_kind_check CHECK (kind IN ('modular', 'degree')),
    CONSTRAINT certificates_status_check CHECK (status IN ('issued', 'annulled')),
    CONSTRAINT certificates_module_check CHECK ((kind = 'modular') = (module_id IS NOT NULL)),
    CONSTRAINT certificates_number_key UNIQUE (institution_id, kind, number)
);

CREATE UNIQUE INDEX certificates_one_modular ON programs.certificates (student_id, module_id) WHERE status = 'issued' AND kind = 'modular';
CREATE UNIQUE INDEX certificates_one_degree ON programs.certificates (student_id, level_id) WHERE status = 'issued' AND kind = 'degree';

-- Degree requirements that are checked by hand (language, final project or exam, no debts)
CREATE TABLE programs.requirements (
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    code varchar(40) NOT NULL,
    done boolean NOT NULL DEFAULT false,
    note varchar(300) NOT NULL DEFAULT '',
    checked_name varchar(160) NOT NULL DEFAULT '',
    checked_at timestamptz,
    PRIMARY KEY (student_id, code)
);
