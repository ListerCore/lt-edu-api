-- Online assessments of the virtual classroom (docs/ceba/PLAN.md, phase E3; "evaluaciones en el aula virtual" of the
-- Anexo 2): quizzes and tasks are material of a unit (items of kind quiz / task) with their settings, questions,
-- the students' attempts and the teacher's grading by competency (AD/A/B/C), which can go to the official record
-- (assessment.competency_marks). Files a student hands in hang on the submission (owner_kind submission)
ALTER TABLE classroom.items DROP CONSTRAINT items_kind_check;
ALTER TABLE classroom.items ADD CONSTRAINT items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link', 'live', 'activity', 'quiz', 'task'));

-- Settings of a quiz or task: when it opens and closes, how many attempts, and the competencies it assesses
CREATE TABLE classroom.assessments (
    item_id uuid PRIMARY KEY REFERENCES classroom.items (id) ON DELETE CASCADE,
    opens_at timestamptz,
    closes_at timestamptz,
    max_attempts smallint NOT NULL DEFAULT 1,
    competency_ids uuid[] NOT NULL DEFAULT '{}',
    -- Whether the student sees the correct answers once graded
    show_answers boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assessments_attempts_check CHECK (max_attempts BETWEEN 1 AND 10),
    CONSTRAINT assessments_window_check CHECK (closes_at IS NULL OR opens_at IS NULL OR closes_at > opens_at)
);

CREATE TRIGGER assessments_touch_updated_at BEFORE UPDATE ON classroom.assessments
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Questions of a quiz. single / multiple: options with the right ones (0-based indexes) · short: accepted answers
-- (compared without case, accents or extra spaces) · open: graded by the teacher
CREATE TABLE classroom.questions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    item_id uuid NOT NULL REFERENCES classroom.assessments (item_id) ON DELETE CASCADE,
    position smallint NOT NULL,
    kind varchar(20) NOT NULL,
    prompt varchar(3000) NOT NULL,
    options text[] NOT NULL DEFAULT '{}',
    correct smallint[] NOT NULL DEFAULT '{}',
    accepted text[] NOT NULL DEFAULT '{}',
    points smallint NOT NULL DEFAULT 1,
    CONSTRAINT questions_kind_check CHECK (kind IN ('single', 'multiple', 'short', 'open')),
    CONSTRAINT questions_prompt_required CHECK (btrim(prompt) <> ''),
    CONSTRAINT questions_points_check CHECK (points BETWEEN 1 AND 100),
    CONSTRAINT questions_options_check CHECK (
        (kind IN ('single', 'multiple') AND cardinality(options) BETWEEN 2 AND 8 AND cardinality(correct) >= 1)
        OR (kind = 'short' AND cardinality(accepted) >= 1)
        OR kind = 'open'
    )
);

CREATE INDEX idx_classroom_questions_item ON classroom.questions (item_id, position);

-- A student's attempt. in_progress: answering · submitted: handed in, waiting for the teacher · graded: levels set
CREATE TABLE classroom.submissions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    item_id uuid NOT NULL REFERENCES classroom.assessments (item_id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    attempt smallint NOT NULL,
    status varchar(20) NOT NULL DEFAULT 'in_progress',
    -- {question_id: {"choices": [..]} | {"text": ".."}}
    answers jsonb NOT NULL DEFAULT '{}',
    -- Points of the questions graded automatically, and their total
    auto_points smallint NOT NULL DEFAULT 0,
    auto_total smallint NOT NULL DEFAULT 0,
    -- Points the teacher gave the open questions, by question id
    open_points jsonb NOT NULL DEFAULT '{}',
    -- {competency_id: "AD" | "A" | "B" | "C"}
    levels jsonb NOT NULL DEFAULT '{}',
    feedback varchar(3000) NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    submitted_at timestamptz,
    late boolean NOT NULL DEFAULT false,
    graded_at timestamptz,
    graded_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    graded_name varchar(160) NOT NULL DEFAULT '',
    -- When the levels went to the official record
    recorded_at timestamptz,
    CONSTRAINT submissions_attempt_key UNIQUE (item_id, student_id, attempt),
    CONSTRAINT submissions_status_check CHECK (status IN ('in_progress', 'submitted', 'graded')),
    CONSTRAINT submissions_attempt_check CHECK (attempt BETWEEN 1 AND 10),
    CONSTRAINT submissions_submitted_check CHECK (status = 'in_progress' OR submitted_at IS NOT NULL)
);

CREATE INDEX idx_classroom_submissions_item ON classroom.submissions (item_id, student_id, attempt DESC);
CREATE INDEX idx_classroom_submissions_student ON classroom.submissions (student_id, submitted_at DESC);

-- More time for one student (estudiantes que trabajan)
CREATE TABLE classroom.extensions (
    item_id uuid NOT NULL REFERENCES classroom.assessments (item_id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    closes_at timestamptz NOT NULL,
    reason varchar(300) NOT NULL DEFAULT '',
    granted_name varchar(160) NOT NULL DEFAULT '',
    PRIMARY KEY (item_id, student_id)
);

ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet', 'classroom_item', 'library_item',
    'submission'
));
