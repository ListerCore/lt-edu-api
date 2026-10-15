-- Organización académica de cada institución.
--
--   study_plan_area   plan de estudios propio: áreas oficiales del ciclo +
--                     tutoría + talleres (ej. TIC, Certificación Técnica Modular)
--   period ─< term    periodo lectivo / promocional y sus cortes de evaluación
--   section           grado + periodo (+ sede)
--   classroom         sección × área del plan × docente = aula virtual
--
-- Las FKs compuestas garantizan que una sección use un grado del servicio que
-- la institución tiene autorizado y que un aula dicte un área de su plan.

CREATE SCHEMA IF NOT EXISTS academic;

CREATE TABLE academic.study_plan_area (
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    cycle_code     text NOT NULL REFERENCES curriculum.cycle (code),
    area_code      text NOT NULL REFERENCES curriculum.area (code),
    -- Horas pedagógicas semanales (45 min); varían según forma de atención.
    weekly_hours   numeric(4, 1) CHECK (weekly_hours >= 0),
    is_active      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, cycle_code, area_code)
);

-- Un área curricular solo entra al plan si es oficial para ese ciclo; tutoría
-- y talleres se pueden agregar a cualquier ciclo.
CREATE FUNCTION academic.check_study_plan_area() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM curriculum.area a
        WHERE a.code = NEW.area_code
          AND (a.kind = 'workshop'
               OR EXISTS (SELECT 1 FROM curriculum.cycle_area ca
                          WHERE ca.cycle_code = NEW.cycle_code AND ca.area_code = NEW.area_code))
    ) THEN
        RAISE EXCEPTION 'el área % no pertenece al plan oficial del ciclo %', NEW.area_code, NEW.cycle_code;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER check_study_plan_area BEFORE INSERT OR UPDATE ON academic.study_plan_area
    FOR EACH ROW EXECUTE FUNCTION academic.check_study_plan_area();

CREATE TABLE academic.period (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id       uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    modality_code        text NOT NULL REFERENCES curriculum.modality (code),
    -- EBR anual: 2026. EBA por periodos promocionales: 2026-I, 2026-II.
    code                 text NOT NULL CHECK (code ~ '^\d{4}(-(I|II|III))?$'),
    starts_on            date NOT NULL,
    ends_on              date NOT NULL,
    enrollment_opens_on  date,
    enrollment_closes_on date,
    status               text NOT NULL DEFAULT 'planned'
                         CHECK (status IN ('planned', 'active', 'closed')),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, modality_code, code),
    -- Destino de FKs que exigen periodo de la misma modalidad.
    UNIQUE (institution_id, id, modality_code),
    CHECK (ends_on > starts_on)
);

-- Un periodo activo por institución y modalidad (un colegio con EBR y EBA
-- puede tener el año 2026 y el periodo 2026-II abiertos a la vez).
CREATE UNIQUE INDEX period_single_active ON academic.period (institution_id, modality_code) WHERE status = 'active';

-- Cortes de evaluación (bimestres / trimestres): cierran la carga de notas.
CREATE TABLE academic.term (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    period_id         uuid NOT NULL,
    number            smallint NOT NULL CHECK (number > 0),
    name              text NOT NULL,
    starts_on         date NOT NULL,
    ends_on           date NOT NULL,
    grading_closes_at timestamptz,
    status            text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (period_id, number),
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id) ON DELETE CASCADE,
    CHECK (ends_on > starts_on)
);

CREATE TABLE academic.section (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    period_id      uuid NOT NULL,
    modality_code  text NOT NULL,
    cycle_code     text NOT NULL,
    grade_code     text NOT NULL,
    campus_id      uuid,
    -- 'A', 'B'… o 'Única' cuando el grado tiene una sola sección.
    name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 20),
    -- Forma de atención: EBA presencial, semipresencial o a distancia (virtual).
    attention_form text NOT NULL DEFAULT 'in_person' CHECK (attention_form IN ('in_person', 'blended', 'distance')),
    -- Programa EBA: PEBAJA (jóvenes y adultos) o alfabetización; null en EBR.
    eba_program    text CHECK (eba_program IN ('pebaja', 'literacy')),
    shift          text CHECK (shift IN ('morning', 'afternoon', 'evening')),
    capacity       smallint CHECK (capacity > 0),
    -- Tutor de la sección (tutoría integral y bienestar).
    tutor_id       uuid,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (period_id, grade_code, name),
    -- Destinos de FKs compuestas (matrícula y aula).
    UNIQUE (institution_id, id, period_id, grade_code),
    UNIQUE (institution_id, id, cycle_code),
    FOREIGN KEY (institution_id, period_id, modality_code) REFERENCES academic.period (institution_id, id, modality_code),
    FOREIGN KEY (grade_code, cycle_code) REFERENCES curriculum.grade (code, cycle_code),
    FOREIGN KEY (institution_id, campus_id) REFERENCES tenant.campus (institution_id, id),
    FOREIGN KEY (institution_id, tutor_id) REFERENCES core.staff (institution_id, id),
    CHECK ((modality_code = 'eba') = (eba_program IS NOT NULL))
);

-- La sección debe ser de un ciclo de la modalidad del periodo y de un nivel
-- que la institución tenga autorizado (tenant.service).
CREATE FUNCTION academic.check_section_service() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM curriculum.cycle c
        JOIN tenant.service s
          ON s.modality_code = c.modality_code AND s.level = c.level AND s.status = 'active'
        WHERE c.code = NEW.cycle_code
          AND c.modality_code = NEW.modality_code
          AND s.institution_id = NEW.institution_id
    ) THEN
        RAISE EXCEPTION 'la institución no tiene un servicio activo para el ciclo % (%)', NEW.cycle_code, NEW.modality_code;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER check_section_service BEFORE INSERT OR UPDATE OF institution_id, modality_code, cycle_code ON academic.section
    FOR EACH ROW EXECUTE FUNCTION academic.check_section_service();

CREATE INDEX section_period_idx ON academic.section (institution_id, period_id);

-- Aula virtual: un área del plan de estudios dictada a una sección.
CREATE TABLE academic.classroom (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    section_id     uuid NOT NULL,
    cycle_code     text NOT NULL,
    area_code      text NOT NULL,
    teacher_id     uuid,
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (section_id, area_code),
    FOREIGN KEY (institution_id, section_id, cycle_code) REFERENCES academic.section (institution_id, id, cycle_code),
    FOREIGN KEY (institution_id, cycle_code, area_code) REFERENCES academic.study_plan_area (institution_id, cycle_code, area_code),
    FOREIGN KEY (institution_id, teacher_id) REFERENCES core.staff (institution_id, id)
);

CREATE INDEX classroom_teacher_idx ON academic.classroom (institution_id, teacher_id);

-- Horario síncrono semanal del aula (ISO: 1 = lunes).
CREATE TABLE academic.classroom_schedule (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    classroom_id   uuid NOT NULL,
    weekday        smallint NOT NULL CHECK (weekday BETWEEN 1 AND 7),
    starts_at      time NOT NULL,
    ends_at        time NOT NULL,
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    CHECK (ends_at > starts_at)
);

CREATE INDEX classroom_schedule_classroom_idx ON academic.classroom_schedule (classroom_id);

SELECT util.attach_updated_at('academic');

SELECT audit.attach('academic.study_plan_area');
