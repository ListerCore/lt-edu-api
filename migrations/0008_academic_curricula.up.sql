-- Study plans (phase 2 of docs/plan-academico.md). Each level / cycle / CEPRE area / IEST program has its plans,
-- versioned: one draft or several, at most one active, the old ones archived. A plan holds what is taught:
-- curricular areas with their competencies (Educación Básica), courses (CEPRE) or didactic units grouped in
-- formative modules with credits and the work-based training EFSRT (institutes)
CREATE TABLE academic.curricula (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE CASCADE,
    name varchar(80) NOT NULL,
    notes varchar(1000) NOT NULL DEFAULT '',
    -- draft: being prepared · active: the plan in force · archived: kept for history, read only
    status varchar(20) NOT NULL DEFAULT 'draft',
    activated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT curricula_name_key UNIQUE (level_id, name),
    CONSTRAINT curricula_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT curricula_status_check CHECK (status IN ('draft', 'active', 'archived'))
);

CREATE TRIGGER curricula_touch_updated_at BEFORE UPDATE ON academic.curricula
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- At most one plan in force per level
CREATE UNIQUE INDEX curricula_one_active ON academic.curricula (level_id) WHERE status = 'active';
CREATE INDEX idx_academic_curricula_institution ON academic.curricula (institution_id, level_id);

-- IEST formative modules: groups of didactic units that end in a modular certificate
CREATE TABLE academic.modules (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    curriculum_id uuid NOT NULL REFERENCES academic.curricula (id) ON DELETE CASCADE,
    code varchar(40) NOT NULL,
    name varchar(160) NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    position smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT modules_code_key UNIQUE (curriculum_id, code),
    CONSTRAINT modules_name_key UNIQUE (curriculum_id, name),
    CONSTRAINT modules_code_format CHECK (code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT modules_name_required CHECK (btrim(name) <> '')
);

CREATE TRIGGER modules_touch_updated_at BEFORE UPDATE ON academic.modules
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- What is taught: a curricular area, workshop or the tutoring hours (Educación Básica), a course (CEPRE), or a
-- didactic unit or EFSRT (institutes). Weekly hours are the plan's; institute units also carry theory and practice credits
CREATE TABLE academic.subjects (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    curriculum_id uuid NOT NULL REFERENCES academic.curricula (id) ON DELETE CASCADE,
    module_id uuid REFERENCES academic.modules (id) ON DELETE SET NULL,
    -- area | workshop | tutoring | course | unit | efsrt
    kind varchar(20) NOT NULL,
    -- Institutes: specific | employability | efsrt
    component varchar(20),
    code varchar(40) NOT NULL,
    name varchar(160) NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    weekly_hours smallint,
    theory_credits smallint,
    practice_credits smallint,
    position smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT subjects_code_key UNIQUE (curriculum_id, code),
    CONSTRAINT subjects_name_key UNIQUE (curriculum_id, name),
    CONSTRAINT subjects_code_format CHECK (code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT subjects_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT subjects_kind_check CHECK (kind IN ('area', 'workshop', 'tutoring', 'course', 'unit', 'efsrt')),
    CONSTRAINT subjects_component_check CHECK (
        (kind IN ('unit', 'efsrt') AND component IN ('specific', 'employability', 'efsrt'))
        OR (kind NOT IN ('unit', 'efsrt') AND component IS NULL)
    ),
    CONSTRAINT subjects_hours_check CHECK (weekly_hours IS NULL OR weekly_hours BETWEEN 0 AND 60),
    CONSTRAINT subjects_credits_check CHECK (
        (kind IN ('unit', 'efsrt') AND theory_credits BETWEEN 0 AND 40 AND practice_credits BETWEEN 0 AND 40 AND theory_credits + practice_credits > 0)
        OR (kind NOT IN ('unit', 'efsrt') AND theory_credits IS NULL AND practice_credits IS NULL)
    )
);

CREATE TRIGGER subjects_touch_updated_at BEFORE UPDATE ON academic.subjects
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_subjects_curriculum ON academic.subjects (curriculum_id, position);
CREATE INDEX idx_academic_subjects_module ON academic.subjects (module_id);

-- Grades (or plan semesters) in which a subject is taught
CREATE TABLE academic.subject_grades (
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    grade_id uuid NOT NULL REFERENCES academic.grades (id) ON DELETE CASCADE,
    PRIMARY KEY (subject_id, grade_id)
);

CREATE INDEX idx_academic_subject_grades_grade ON academic.subject_grades (grade_id);

-- What is assessed: CNEB competencies of an area (or transversal ones, with no area), capacities of a didactic
-- unit, or topics of a CEPRE course
CREATE TABLE academic.competencies (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    curriculum_id uuid NOT NULL REFERENCES academic.curricula (id) ON DELETE CASCADE,
    subject_id uuid REFERENCES academic.subjects (id) ON DELETE CASCADE,
    -- competency | capacity | topic
    kind varchar(20) NOT NULL,
    -- Short reference: "C25" for CNEB competency 25, free otherwise
    code varchar(20) NOT NULL DEFAULT '',
    name varchar(300) NOT NULL,
    position smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT competencies_name_key UNIQUE NULLS NOT DISTINCT (curriculum_id, subject_id, name),
    CONSTRAINT competencies_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT competencies_kind_check CHECK (kind IN ('competency', 'capacity', 'topic'))
);

CREATE TRIGGER competencies_touch_updated_at BEFORE UPDATE ON academic.competencies
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_competencies_subject ON academic.competencies (curriculum_id, subject_id, position);
