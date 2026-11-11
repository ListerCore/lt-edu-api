-- Assessment by term (phase 6 of docs/plan-academico.md): achievement level AD/A/B/C per competency with its
-- descriptive conclusion (Educación Básica), graded evaluations 0–20 per unit or course (IEST, CEPRE practices), the
-- conduct mark and the teacher's "sent" of each sheet. Periods without terms (IEST semesters, CEPRE cycles) use
-- term_id NULL: the whole period is one term
CREATE SCHEMA IF NOT EXISTS assessment;

-- One level per student, competency and term in a section (a competency belongs to one area, taught once per
-- section; transversal competencies have no area and the tutor marks them)
CREATE TABLE assessment.competency_marks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    term_id uuid REFERENCES academic.terms (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    competency_id uuid NOT NULL REFERENCES academic.competencies (id) ON DELETE CASCADE,
    -- AD | A | B | C (empty: only a conclusion so far)
    level varchar(2) NOT NULL DEFAULT '',
    conclusion varchar(1000) NOT NULL DEFAULT '',
    updated_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT competency_marks_key UNIQUE NULLS NOT DISTINCT (section_id, term_id, student_id, competency_id),
    CONSTRAINT competency_marks_level_check CHECK (level IN ('', 'AD', 'A', 'B', 'C')),
    CONSTRAINT competency_marks_something_check CHECK (level <> '' OR btrim(conclusion) <> '')
);

CREATE INDEX idx_assessment_competency_marks_student ON assessment.competency_marks (student_id, term_id);

-- An evaluation of a unit or course (practice, indicator, exam) with its weight in the average
CREATE TABLE assessment.evaluations (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL REFERENCES academic.assignments (id) ON DELETE CASCADE,
    term_id uuid REFERENCES academic.terms (id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    weight smallint NOT NULL DEFAULT 1,
    held_on date,
    position smallint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT evaluations_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT evaluations_weight_check CHECK (weight BETWEEN 1 AND 100)
);

CREATE INDEX idx_assessment_evaluations_assignment ON assessment.evaluations (assignment_id, term_id, position);

CREATE TABLE assessment.scores (
    evaluation_id uuid NOT NULL REFERENCES assessment.evaluations (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    score smallint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (evaluation_id, student_id),
    CONSTRAINT scores_range_check CHECK (score BETWEEN 0 AND 20)
);

CREATE INDEX idx_assessment_scores_student ON assessment.scores (student_id);

-- Conduct 0–20 per student and term (DS 021-2026-MINEDU), set by the tutor when the institution uses it
CREATE TABLE assessment.conduct_marks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    term_id uuid REFERENCES academic.terms (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    score smallint NOT NULL,
    note varchar(500) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT conduct_marks_key UNIQUE NULLS NOT DISTINCT (section_id, term_id, student_id),
    CONSTRAINT conduct_marks_range_check CHECK (score BETWEEN 0 AND 20)
);

-- The teacher sends a sheet (an area of a section, or the tutor's sheet when assignment_id is NULL) to coordinación
CREATE TABLE assessment.submissions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    assignment_id uuid REFERENCES academic.assignments (id) ON DELETE CASCADE,
    term_id uuid REFERENCES academic.terms (id) ON DELETE CASCADE,
    submitted_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    submitted_name varchar(160) NOT NULL DEFAULT '',
    submitted_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT submissions_key UNIQUE NULLS NOT DISTINCT (section_id, assignment_id, term_id)
);

-- What the tutor writes to the family on each report card
CREATE TABLE assessment.tutor_comments (
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    term_id uuid REFERENCES academic.terms (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    comment varchar(1000) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tutor_comments_key UNIQUE NULLS NOT DISTINCT (section_id, term_id, student_id)
);
