-- Academic structure (phase 1 of docs/plan-academico.md): the profile of each institution, its levels / cycles /
-- areas / programs and their grades, the academic periods (school year, promotional period, cycle, semester) with
-- their terms, and the sections of every period. One generic model for the five institution kinds; the API applies
-- each kind's rules and names
CREATE SCHEMA IF NOT EXISTS academic;

-- Academic profile, one row per institution. Defaults come from the institution kind when the row is created
CREATE TABLE academic.settings (
    institution_id uuid PRIMARY KEY REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- How new periods split: bimester | trimester | semester | none (CEPRE cycles and IEST semesters have no terms)
    term_scheme varchar(20) NOT NULL,
    -- literal: AD/A/B/C per competency (Educación Básica) · vigesimal: 0 to 20 (institutes) · score: points (CEPRE)
    grading_scale varchar(20) NOT NULL,
    -- Lowest passing mark on the vigesimal scale (institutes usually 13); NULL on the other scales
    passing_grade smallint,
    -- Conduct mark from 0 to 20 (DS 021-2026-MINEDU), only meaningful in Educación Básica
    conduct_grade boolean NOT NULL DEFAULT false,
    -- EBA forms of attention the institution offers
    attention_modes text[] NOT NULL DEFAULT '{}',
    -- Shifts sections can run in
    shifts text[] NOT NULL DEFAULT '{morning}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT settings_term_scheme_check CHECK (term_scheme IN ('bimester', 'trimester', 'semester', 'none')),
    CONSTRAINT settings_grading_scale_check CHECK (grading_scale IN ('literal', 'vigesimal', 'score')),
    CONSTRAINT settings_passing_grade_check CHECK (
        (grading_scale = 'vigesimal' AND passing_grade BETWEEN 1 AND 20) OR (grading_scale <> 'vigesimal' AND passing_grade IS NULL)
    ),
    CONSTRAINT settings_attention_modes_check CHECK (attention_modes <@ ARRAY['in_person', 'blended', 'distance']::text[]),
    CONSTRAINT settings_shifts_check CHECK (cardinality(shifts) > 0 AND shifts <@ ARRAY['morning', 'afternoon', 'evening']::text[])
);

CREATE TRIGGER settings_touch_updated_at BEFORE UPDATE ON academic.settings
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- First level of the structure: EBR/EBE levels (Inicial, Primaria, Secundaria, PRITE), EBA cycles (Inicial,
-- Intermedio, Avanzado), CEPRE target areas (UNI, San Marcos área A…) and IEST study programs
CREATE TABLE academic.levels (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- Short stable code inside the institution (inicial, primaria, enfermeria-tecnica…)
    code varchar(40) NOT NULL,
    name varchar(120) NOT NULL,
    description varchar(500) NOT NULL DEFAULT '',
    position smallint NOT NULL,
    -- IEST only: formative level (technical | professional_technical | professional) and total credits of the plan
    formative_level varchar(30),
    credits smallint,
    -- EBA only: forms of attention this cycle may run in (a subset of the institution's)
    attention_modes text[] NOT NULL DEFAULT '{}',
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT levels_code_key UNIQUE (institution_id, code),
    CONSTRAINT levels_name_key UNIQUE (institution_id, name),
    CONSTRAINT levels_code_format CHECK (code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT levels_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT levels_position_check CHECK (position >= 0),
    CONSTRAINT levels_formative_level_check CHECK (formative_level IS NULL OR formative_level IN ('technical', 'professional_technical', 'professional')),
    CONSTRAINT levels_credits_check CHECK (credits IS NULL OR credits BETWEEN 1 AND 400),
    CONSTRAINT levels_attention_modes_check CHECK (attention_modes <@ ARRAY['in_person', 'blended', 'distance']::text[])
);

CREATE TRIGGER levels_touch_updated_at BEFORE UPDATE ON academic.levels
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_levels_institution ON academic.levels (institution_id, position);

-- Second level: grade or age (EBR, EBA, EBE) or semester of the plan (IEST). CEPRE areas have none
CREATE TABLE academic.grades (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE RESTRICT,
    code varchar(40) NOT NULL,
    -- Full name ("1.° grado", "3 años", "Semestre III") and the short form used in lists ("1.°", "3 a", "III")
    name varchar(80) NOT NULL,
    short_name varchar(20) NOT NULL,
    position smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT grades_code_key UNIQUE (level_id, code),
    CONSTRAINT grades_name_key UNIQUE (level_id, name),
    CONSTRAINT grades_code_format CHECK (code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT grades_name_required CHECK (btrim(name) <> '' AND btrim(short_name) <> ''),
    CONSTRAINT grades_position_check CHECK (position >= 0)
);

CREATE TRIGGER grades_touch_updated_at BEFORE UPDATE ON academic.grades
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_grades_level ON academic.grades (level_id, position);

-- Academic periods: school year (EBR, EBE), promotional period (EBA, up to two a year in blended or distance
-- forms), cycle (CEPRE: summer, annual, review… several may run at once) and academic semester (IEST)
CREATE TABLE academic.periods (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    kind varchar(20) NOT NULL,
    name varchar(80) NOT NULL,
    -- CEPRE cycles only: summer | annual | semester | intensive | review | school | other
    season varchar(20),
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    term_scheme varchar(20) NOT NULL,
    -- planning: being prepared, nobody enrolls yet · active: running · closed: read only
    status varchar(20) NOT NULL DEFAULT 'planning',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT periods_name_key UNIQUE (institution_id, name),
    CONSTRAINT periods_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT periods_kind_check CHECK (kind IN ('school_year', 'promotional', 'cycle', 'semester')),
    CONSTRAINT periods_season_check CHECK (
        (kind = 'cycle' AND season IN ('summer', 'annual', 'semester', 'intensive', 'review', 'school', 'other'))
        OR (kind <> 'cycle' AND season IS NULL)
    ),
    CONSTRAINT periods_dates_check CHECK (ends_on > starts_on AND ends_on - starts_on <= 400),
    CONSTRAINT periods_term_scheme_check CHECK (term_scheme IN ('bimester', 'trimester', 'semester', 'none')),
    CONSTRAINT periods_status_check CHECK (status IN ('planning', 'active', 'closed'))
);

CREATE TRIGGER periods_touch_updated_at BEFORE UPDATE ON academic.periods
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_periods_institution ON academic.periods (institution_id, starts_on DESC);

-- Terms inside a period (bimesters, trimesters or semesters), numbered from 1, back to back without overlapping
CREATE TABLE academic.terms (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    number smallint NOT NULL,
    name varchar(40) NOT NULL,
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    -- open: marks can be recorded · closed: the term's results are final
    status varchar(20) NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT terms_number_key UNIQUE (period_id, number),
    CONSTRAINT terms_number_check CHECK (number BETWEEN 1 AND 6),
    CONSTRAINT terms_dates_check CHECK (ends_on >= starts_on),
    CONSTRAINT terms_status_check CHECK (status IN ('open', 'closed'))
);

CREATE TRIGGER terms_touch_updated_at BEFORE UPDATE ON academic.terms
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Sections of a period: a group of students of one grade (or of one CEPRE area), with its shift and capacity
CREATE TABLE academic.sections (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    level_id uuid NOT NULL REFERENCES academic.levels (id) ON DELETE RESTRICT,
    -- Required except for CEPRE areas, which have no grades (the API checks it against the institution kind)
    grade_id uuid REFERENCES academic.grades (id) ON DELETE RESTRICT,
    name varchar(40) NOT NULL,
    shift varchar(20) NOT NULL DEFAULT 'morning',
    -- EBA only: in_person | blended | distance
    attention_mode varchar(20),
    capacity smallint NOT NULL,
    -- Physical room, free text ("Aula 204", "Laboratorio 2")
    room varchar(60) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sections_name_key UNIQUE NULLS NOT DISTINCT (period_id, level_id, grade_id, name),
    CONSTRAINT sections_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT sections_shift_check CHECK (shift IN ('morning', 'afternoon', 'evening')),
    CONSTRAINT sections_attention_mode_check CHECK (attention_mode IS NULL OR attention_mode IN ('in_person', 'blended', 'distance')),
    CONSTRAINT sections_capacity_check CHECK (capacity BETWEEN 1 AND 300)
);

CREATE TRIGGER sections_touch_updated_at BEFORE UPDATE ON academic.sections
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_sections_period ON academic.sections (period_id, level_id, grade_id);
CREATE INDEX idx_academic_sections_grade ON academic.sections (grade_id);
CREATE INDEX idx_academic_sections_level ON academic.sections (level_id);
