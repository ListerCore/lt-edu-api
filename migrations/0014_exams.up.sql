-- Academies (phase 8 of docs/plan-academico.md): the question bank per course, mock admission exams with the weight
-- of each course per area, the answer sheets (typed or from an optical reader), scores, rankings and the students
-- who got into university
CREATE SCHEMA IF NOT EXISTS exams;

-- A question of the bank. Courses are identified by the code they share across the areas' plans ("aritmetica")
CREATE TABLE exams.questions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    course_code varchar(40) NOT NULL,
    topic varchar(160) NOT NULL DEFAULT '',
    stem varchar(3000) NOT NULL,
    -- Five options A–E
    options text[] NOT NULL,
    answer varchar(1) NOT NULL,
    -- easy | medium | hard
    difficulty varchar(10) NOT NULL DEFAULT 'medium',
    explanation varchar(3000) NOT NULL DEFAULT '',
    created_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    author_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT questions_stem_required CHECK (btrim(stem) <> ''),
    CONSTRAINT questions_options_check CHECK (cardinality(options) = 5),
    CONSTRAINT questions_answer_check CHECK (answer IN ('A', 'B', 'C', 'D', 'E')),
    CONSTRAINT questions_difficulty_check CHECK (difficulty IN ('easy', 'medium', 'hard'))
);

CREATE INDEX idx_exams_questions_course ON exams.questions (institution_id, course_code, topic);

-- A mock exam of a cycle. draft: being built | published: results visible to students and families
CREATE TABLE exams.mock_exams (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    held_on date NOT NULL,
    duration_minutes smallint NOT NULL DEFAULT 180,
    -- Points for a right answer and the ones taken off for a wrong one (blank: 0)
    points_right numeric(6, 3) NOT NULL DEFAULT 20,
    points_wrong numeric(6, 3) NOT NULL DEFAULT 1.125,
    status varchar(20) NOT NULL DEFAULT 'draft',
    notes varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mock_exams_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT mock_exams_status_check CHECK (status IN ('draft', 'published')),
    CONSTRAINT mock_exams_points_check CHECK (points_right > 0 AND points_wrong >= 0),
    CONSTRAINT mock_exams_duration_check CHECK (duration_minutes BETWEEN 10 AND 600)
);

CREATE INDEX idx_exams_mock_exams_period ON exams.mock_exams (institution_id, period_id, held_on DESC);

-- The courses of a mock exam in order, with how many questions each has
CREATE TABLE exams.mock_courses (
    mock_exam_id uuid NOT NULL REFERENCES exams.mock_exams (id) ON DELETE CASCADE,
    course_code varchar(40) NOT NULL,
    course_name varchar(160) NOT NULL,
    questions smallint NOT NULL,
    position smallint NOT NULL,
    PRIMARY KEY (mock_exam_id, course_code),
    CONSTRAINT mock_courses_questions_check CHECK (questions BETWEEN 1 AND 100)
);

-- How much a course weighs for each area (level) taking the exam
CREATE TABLE exams.mock_weights (
    mock_exam_id uuid NOT NULL REFERENCES exams.mock_exams (id) ON DELETE CASCADE,
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE CASCADE,
    course_code varchar(40) NOT NULL,
    weight numeric(5, 2) NOT NULL DEFAULT 1,
    PRIMARY KEY (mock_exam_id, level_id, course_code),
    CONSTRAINT mock_weights_weight_check CHECK (weight >= 0 AND weight <= 10)
);

-- The answer key: one row per question number, from the bank or typed
CREATE TABLE exams.mock_questions (
    mock_exam_id uuid NOT NULL REFERENCES exams.mock_exams (id) ON DELETE CASCADE,
    number smallint NOT NULL,
    course_code varchar(40) NOT NULL,
    question_id uuid REFERENCES exams.questions (id) ON DELETE SET NULL,
    answer varchar(1) NOT NULL DEFAULT '',
    PRIMARY KEY (mock_exam_id, number),
    CONSTRAINT mock_questions_answer_check CHECK (answer IN ('', 'A', 'B', 'C', 'D', 'E'))
);

-- What a student marked: one character per question (A–E, or '-' blank), and the score it gives
CREATE TABLE exams.answer_sheets (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    mock_exam_id uuid NOT NULL REFERENCES exams.mock_exams (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    section_id uuid REFERENCES academic.sections (id) ON DELETE SET NULL,
    level_id uuid REFERENCES academic.levels (id) ON DELETE SET NULL,
    answers varchar(300) NOT NULL,
    -- manual | reader
    source varchar(20) NOT NULL DEFAULT 'manual',
    score numeric(9, 3) NOT NULL DEFAULT 0,
    right_count smallint NOT NULL DEFAULT 0,
    wrong_count smallint NOT NULL DEFAULT 0,
    blank_count smallint NOT NULL DEFAULT 0,
    -- course code → {right, wrong, blank, score}
    by_course jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT answer_sheets_key UNIQUE (mock_exam_id, student_id),
    CONSTRAINT answer_sheets_answers_check CHECK (answers ~ '^[A-E-]*$'),
    CONSTRAINT answer_sheets_source_check CHECK (source IN ('manual', 'reader'))
);

CREATE INDEX idx_exams_answer_sheets_student ON exams.answer_sheets (student_id);

-- A student who got into university: the academy's results
CREATE TABLE exams.admissions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    university varchar(120) NOT NULL,
    career varchar(160) NOT NULL,
    exam varchar(120) NOT NULL,
    admitted_on date NOT NULL,
    place integer,
    score numeric(9, 3),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT admissions_key UNIQUE (student_id, university, exam),
    CONSTRAINT admissions_text_required CHECK (btrim(university) <> '' AND btrim(career) <> '' AND btrim(exam) <> ''),
    CONSTRAINT admissions_place_check CHECK (place IS NULL OR place >= 1)
);

CREATE INDEX idx_exams_admissions_institution ON exams.admissions (institution_id, admitted_on DESC);
