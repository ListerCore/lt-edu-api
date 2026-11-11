-- Year and semester closing (phase 7 of docs/plan-academico.md): the final situation of each student (PRO / PER / RR)
-- with the evaluation team's decisions, pedagogical recovery, the final result of each IEST unit and the records
-- (actas) issued from them

-- One per student and period (Educación Básica). computed is what the rules say; situation is the one in force
-- (the team may change it, with its reason); after_recovery is the definitive one of students who went to recovery
CREATE TABLE assessment.final_results (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    computed varchar(3) NOT NULL DEFAULT '',
    reason varchar(300) NOT NULL DEFAULT '',
    -- Decision of the evaluation team ('' = follows the rules)
    decided varchar(3) NOT NULL DEFAULT '',
    decision_note varchar(500) NOT NULL DEFAULT '',
    decided_name varchar(160) NOT NULL DEFAULT '',
    decided_at timestamptz,
    confirmed_at timestamptz,
    after_recovery varchar(3) NOT NULL DEFAULT '',
    recovery_at timestamptz,
    CONSTRAINT final_results_key UNIQUE (period_id, student_id),
    CONSTRAINT final_results_computed_check CHECK (computed IN ('', 'PRO', 'PER', 'RR', 'PEN')),
    CONSTRAINT final_results_decided_check CHECK (decided IN ('', 'PRO', 'PER', 'RR')),
    CONSTRAINT final_results_after_check CHECK (after_recovery IN ('', 'PRO', 'PER')),
    CONSTRAINT final_results_decision_note_check CHECK (decided = '' OR btrim(decision_note) <> '')
);

CREATE INDEX idx_assessment_final_results_section ON assessment.final_results (section_id);

-- Levels reached in the summer recovery, for the competencies that ended the year in C
CREATE TABLE assessment.recovery_levels (
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    competency_id uuid NOT NULL REFERENCES academic.competencies (id) ON DELETE CASCADE,
    level varchar(2) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (period_id, student_id, competency_id),
    CONSTRAINT recovery_levels_level_check CHECK (level IN ('AD', 'A', 'B', 'C'))
);

-- The result of a student in an IEST unit (or a course with scores): average, whole grade, absences and recovery
CREATE TABLE assessment.unit_results (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL REFERENCES academic.assignments (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    average numeric(4, 1),
    grade smallint,
    absent_percent numeric(5, 1) NOT NULL DEFAULT 0,
    -- approved | recovery | failed | barred (too many absences)
    status varchar(20) NOT NULL,
    recovery_score smallint,
    final_grade smallint,
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unit_results_key UNIQUE (assignment_id, student_id),
    CONSTRAINT unit_results_status_check CHECK (status IN ('approved', 'recovery', 'failed', 'barred')),
    CONSTRAINT unit_results_grades_check CHECK ((grade IS NULL OR grade BETWEEN 0 AND 20) AND (recovery_score IS NULL OR recovery_score BETWEEN 0 AND 20)
        AND (final_grade IS NULL OR final_grade BETWEEN 0 AND 20))
);

-- An issued record (acta). Its content is frozen when issued; issuing again replaces the previous one
CREATE TABLE assessment.records (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    section_id uuid NOT NULL REFERENCES academic.sections (id) ON DELETE CASCADE,
    assignment_id uuid REFERENCES academic.assignments (id) ON DELETE CASCADE,
    -- final | complementary | unit
    kind varchar(20) NOT NULL,
    number integer NOT NULL,
    code varchar(40) NOT NULL,
    -- issued | replaced
    status varchar(20) NOT NULL DEFAULT 'issued',
    content jsonb NOT NULL,
    issued_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    issued_name varchar(160) NOT NULL DEFAULT '',
    issued_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT records_kind_check CHECK (kind IN ('final', 'complementary', 'unit')),
    CONSTRAINT records_status_check CHECK (status IN ('issued', 'replaced')),
    CONSTRAINT records_number_key UNIQUE (institution_id, period_id, kind, number)
);

CREATE INDEX idx_assessment_records_section ON assessment.records (section_id, kind, status);
