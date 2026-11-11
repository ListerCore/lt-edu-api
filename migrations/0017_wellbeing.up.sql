-- Wellbeing (phase 11 of docs/plan-academico.md): tutoring interviews and commitments, the psychology cases
-- (confidential), school discipline under DS 021-2026-MINEDU (incidents, measures and reconsideration) and, in
-- special education (EBE), the individual plan (POI) and therapy sessions
CREATE SCHEMA IF NOT EXISTS wellbeing;

-- Therapist or SAANEE / PRITE professional of special education: writes the POI of the students assigned and
-- logs their therapies
INSERT INTO iam.roles (code, name, description, scope)
VALUES ('special_ed_specialist', 'Especialista de educación especial', 'Terapeuta o profesional SAANEE / PRITE: plan de orientación individual y terapias de sus estudiantes.', 'institution')
ON CONFLICT (code) DO NOTHING;

-- ---------- tutoring ----------

-- An interview of the tutor (or dirección) with the student or the family
CREATE TABLE wellbeing.interviews (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- student | family
    kind varchar(20) NOT NULL,
    held_on date NOT NULL,
    topic varchar(160) NOT NULL,
    -- What was talked about: only school staff read it
    notes varchar(4000) NOT NULL DEFAULT '',
    -- What was agreed: the family reads it
    agreements varchar(2000) NOT NULL DEFAULT '',
    author_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    author_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT interviews_kind_check CHECK (kind IN ('student', 'family'))
);

CREATE INDEX idx_wellbeing_interviews_student ON wellbeing.interviews (student_id, held_on DESC);

-- A commitment of the student, the family or the school, checked when it falls due
CREATE TABLE wellbeing.commitments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    interview_id uuid REFERENCES wellbeing.interviews (id) ON DELETE SET NULL,
    -- student | family | school
    party varchar(20) NOT NULL,
    description varchar(500) NOT NULL,
    due_on date NOT NULL,
    -- pending | met | unmet
    status varchar(20) NOT NULL DEFAULT 'pending',
    checked_on date,
    author_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT commitments_party_check CHECK (party IN ('student', 'family', 'school')),
    CONSTRAINT commitments_status_check CHECK (status IN ('pending', 'met', 'unmet'))
);

CREATE INDEX idx_wellbeing_commitments_student ON wellbeing.commitments (student_id);
CREATE INDEX idx_wellbeing_commitments_due ON wellbeing.commitments (institution_id, due_on) WHERE status = 'pending';

-- ---------- psychology (confidential) ----------

-- A case: opened by a referral (tutor, teacher, family, dirección) or by the psychologist. Whoever referred sees the
-- status and shared_note; the reason and the sessions are for psychologists only
CREATE TABLE wellbeing.psych_cases (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- tutor | teacher | family | direction | self | psychologist
    origin varchar(20) NOT NULL,
    referred_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    referred_name varchar(160) NOT NULL DEFAULT '',
    reason varchar(2000) NOT NULL,
    -- normal | urgent
    priority varchar(10) NOT NULL DEFAULT 'normal',
    -- referred | open | follow_up | closed
    status varchar(20) NOT NULL DEFAULT 'referred',
    opened_on date NOT NULL,
    closed_on date,
    psychologist_name varchar(160) NOT NULL DEFAULT '',
    -- Recommendations the psychologist chooses to share with whoever referred
    shared_note varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT psych_cases_origin_check CHECK (origin IN ('tutor', 'teacher', 'family', 'direction', 'self', 'psychologist')),
    CONSTRAINT psych_cases_priority_check CHECK (priority IN ('normal', 'urgent')),
    CONSTRAINT psych_cases_status_check CHECK (status IN ('referred', 'open', 'follow_up', 'closed'))
);

CREATE INDEX idx_wellbeing_psych_cases_student ON wellbeing.psych_cases (student_id);
CREATE INDEX idx_wellbeing_psych_cases_status ON wellbeing.psych_cases (institution_id, status);

CREATE TABLE wellbeing.psych_sessions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    case_id uuid NOT NULL REFERENCES wellbeing.psych_cases (id) ON DELETE CASCADE,
    held_on date NOT NULL,
    -- individual | family | evaluation | coordination
    kind varchar(20) NOT NULL,
    notes varchar(4000) NOT NULL DEFAULT '',
    next_steps varchar(1000) NOT NULL DEFAULT '',
    author_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT psych_sessions_kind_check CHECK (kind IN ('individual', 'family', 'evaluation', 'coordination'))
);

CREATE INDEX idx_wellbeing_psych_sessions_case ON wellbeing.psych_sessions (case_id, held_on);

-- ---------- discipline (DS 021-2026-MINEDU) ----------

-- A fault: minor ones are handled by the teacher, serious and very serious ones by the director
CREATE TABLE wellbeing.incidents (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    occurred_on date NOT NULL,
    -- minor | serious | very_serious
    severity varchar(20) NOT NULL,
    -- A code of the catalog (service.incidentCatalog)
    category varchar(40) NOT NULL,
    description varchar(2000) NOT NULL,
    reported_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    reported_name varchar(160) NOT NULL DEFAULT '',
    family_notified_on date,
    -- open | closed
    status varchar(10) NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT incidents_severity_check CHECK (severity IN ('minor', 'serious', 'very_serious')),
    CONSTRAINT incidents_status_check CHECK (status IN ('open', 'closed'))
);

CREATE INDEX idx_wellbeing_incidents_student ON wellbeing.incidents (student_id, occurred_on DESC);
CREATE INDEX idx_wellbeing_incidents_date ON wellbeing.incidents (institution_id, occurred_on DESC);

-- A measure: corrective (minor faults), suspension (up to three school days), conditional enrollment or expulsion.
-- The family may ask for reconsideration within three school days of being told; dirección answers within five
CREATE TABLE wellbeing.measures (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    incident_id uuid REFERENCES wellbeing.incidents (id) ON DELETE CASCADE,
    -- corrective | suspension | conditional | expulsion
    kind varchar(20) NOT NULL,
    -- Why the measure is allowed (service.measureGrounds)
    ground varchar(30) NOT NULL DEFAULT '',
    detail varchar(2000) NOT NULL,
    starts_on date NOT NULL,
    days smallint NOT NULL DEFAULT 0,
    decided_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    decided_name varchar(160) NOT NULL DEFAULT '',
    notified_on date,
    reconsideration_requested_on date,
    reconsideration_reason varchar(2000) NOT NULL DEFAULT '',
    reconsideration_by varchar(160) NOT NULL DEFAULT '',
    resolved_on date,
    -- upheld | revoked
    outcome varchar(10) NOT NULL DEFAULT '',
    resolution varchar(2000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT measures_kind_check CHECK (kind IN ('corrective', 'suspension', 'conditional', 'expulsion')),
    CONSTRAINT measures_days_check CHECK ((kind = 'suspension' AND days BETWEEN 1 AND 3) OR (kind <> 'suspension' AND days = 0)),
    CONSTRAINT measures_outcome_check CHECK (outcome IN ('', 'upheld', 'revoked'))
);

CREATE INDEX idx_wellbeing_measures_student ON wellbeing.measures (student_id);

-- ---------- special education (EBE) ----------

-- The individual plan (Plan de orientación individual) of a student for a period
CREATE TABLE wellbeing.individual_plans (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    specialist_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    needs varchar(2000) NOT NULL DEFAULT '',
    strengths varchar(2000) NOT NULL DEFAULT '',
    educational_response varchar(4000) NOT NULL DEFAULT '',
    curricular_adaptations varchar(4000) NOT NULL DEFAULT '',
    access_adaptations varchar(4000) NOT NULL DEFAULT '',
    evaluation_criteria varchar(2000) NOT NULL DEFAULT '',
    actors varchar(2000) NOT NULL DEFAULT '',
    family_commitments varchar(2000) NOT NULL DEFAULT '',
    -- draft | approved
    status varchar(10) NOT NULL DEFAULT 'draft',
    approved_name varchar(160) NOT NULL DEFAULT '',
    approved_on date,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT individual_plans_key UNIQUE (student_id, period_id),
    CONSTRAINT individual_plans_status_check CHECK (status IN ('draft', 'approved'))
);

CREATE INDEX idx_wellbeing_individual_plans_specialist ON wellbeing.individual_plans (specialist_id);

CREATE TABLE wellbeing.therapy_sessions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    therapist_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    therapist_name varchar(160) NOT NULL DEFAULT '',
    -- language | physical | occupational | psychomotor | behavioral | learning
    kind varchar(20) NOT NULL,
    held_on date NOT NULL,
    minutes smallint NOT NULL DEFAULT 45,
    attended boolean NOT NULL DEFAULT true,
    goal varchar(500) NOT NULL DEFAULT '',
    -- Professional notes: the family does not read them
    notes varchar(2000) NOT NULL DEFAULT '',
    -- What the family can practise at home
    home_tasks varchar(1000) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT therapy_sessions_kind_check CHECK (kind IN ('language', 'physical', 'occupational', 'psychomotor', 'behavioral', 'learning')),
    CONSTRAINT therapy_sessions_minutes_check CHECK (minutes BETWEEN 10 AND 240)
);

CREATE INDEX idx_wellbeing_therapy_sessions_student ON wellbeing.therapy_sessions (student_id, held_on DESC);
CREATE INDEX idx_wellbeing_therapy_sessions_date ON wellbeing.therapy_sessions (institution_id, held_on DESC);
