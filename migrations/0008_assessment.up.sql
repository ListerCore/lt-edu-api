-- Evaluación por competencias (CNEB, escala AD · A · B · C), igual para EBR y EBA.
--
--   evaluation ─< criterion            instrumento del aula con sus criterios
--   evaluation ─< result               nivel por estudiante (registro auxiliar)
--   term_grade                         nivel por competencia y bimestre (libreta)
--   final_grade                        nivel final por área (acta → SIAGIE)
--
-- Las competencias evaluadas deben corresponder al ciclo y área del aula
-- (curriculum.is_competency_of).

CREATE SCHEMA IF NOT EXISTS assessment;

CREATE TABLE assessment.evaluation (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id    uuid NOT NULL,
    classroom_id      uuid NOT NULL,
    term_id           uuid NOT NULL,
    competency_number smallint NOT NULL REFERENCES curriculum.competency (number),
    -- Tarea del aula cuya entrega es la evidencia, si aplica.
    assignment_id     uuid,
    title             text NOT NULL,
    instrument        text NOT NULL CHECK (instrument IN ('rubric', 'checklist', 'test', 'observation', 'oral')),
    weight            numeric(5, 2) NOT NULL DEFAULT 1 CHECK (weight > 0),
    evaluated_on      date,
    status            text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'closed')),
    created_by        uuid REFERENCES iam.account (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, term_id) REFERENCES academic.term (institution_id, id),
    FOREIGN KEY (institution_id, assignment_id) REFERENCES lms.assignment (institution_id, id)
        ON DELETE SET NULL (assignment_id)
);

CREATE INDEX evaluation_classroom_term_idx ON assessment.evaluation (classroom_id, term_id);

-- Valida que la competencia sea del ciclo y área del aula. Se reutiliza en
-- evaluation y term_grade.
CREATE FUNCTION assessment.check_classroom_competency() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    room record;
BEGIN
    SELECT cycle_code, area_code INTO room
    FROM academic.classroom WHERE id = NEW.classroom_id;

    IF NOT curriculum.is_competency_of(room.cycle_code, room.area_code, NEW.competency_number) THEN
        RAISE EXCEPTION 'la competencia % no corresponde al área % del ciclo %',
            NEW.competency_number, room.area_code, room.cycle_code;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER check_classroom_competency BEFORE INSERT OR UPDATE OF classroom_id, competency_number ON assessment.evaluation
    FOR EACH ROW EXECUTE FUNCTION assessment.check_classroom_competency();

CREATE TABLE assessment.criterion (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    evaluation_id  uuid NOT NULL,
    capacity_code  text REFERENCES curriculum.capacity (code),
    description    text NOT NULL,
    weight         numeric(5, 2) NOT NULL DEFAULT 1 CHECK (weight > 0),
    position       smallint NOT NULL DEFAULT 0,
    FOREIGN KEY (institution_id, evaluation_id) REFERENCES assessment.evaluation (institution_id, id) ON DELETE CASCADE
);

CREATE INDEX criterion_evaluation_idx ON assessment.criterion (evaluation_id);

CREATE TABLE assessment.result (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    evaluation_id  uuid NOT NULL,
    student_id     uuid NOT NULL,
    level          curriculum.achievement_level NOT NULL,
    -- Nota vigesimal opcional (ej. 16.5) cuando el instrumento la produce.
    score          numeric(4, 2) CHECK (score BETWEEN 0 AND 20),
    feedback       text,
    submission_id  uuid,
    graded_by      uuid REFERENCES iam.account (id),
    graded_at      timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (evaluation_id, student_id),
    FOREIGN KEY (institution_id, evaluation_id) REFERENCES assessment.evaluation (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    FOREIGN KEY (institution_id, submission_id) REFERENCES lms.submission (institution_id, id)
        ON DELETE SET NULL (submission_id)
);

CREATE INDEX result_student_idx ON assessment.result (student_id);

-- Libreta: un nivel por competencia y bimestre. Al cerrar el bimestre
-- (status = 'closed') solo se corrige con trazabilidad en audit.change_log.
CREATE TABLE assessment.term_grade (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id         uuid NOT NULL,
    enrollment_id          uuid NOT NULL,
    classroom_id           uuid NOT NULL,
    competency_number      smallint NOT NULL REFERENCES curriculum.competency (number),
    term_id                uuid NOT NULL,
    level                  curriculum.achievement_level NOT NULL,
    descriptive_conclusion text,
    status                 text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'closed')),
    closed_by              uuid REFERENCES iam.account (id),
    closed_at              timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    -- Una competencia transversal se califica una vez, no por cada aula.
    UNIQUE (enrollment_id, competency_number, term_id),
    FOREIGN KEY (institution_id, enrollment_id) REFERENCES enrollment.enrollment (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, classroom_id) REFERENCES academic.classroom (institution_id, id),
    FOREIGN KEY (institution_id, term_id) REFERENCES academic.term (institution_id, id),
    CHECK (status <> 'closed' OR closed_at IS NOT NULL)
);

CREATE TRIGGER check_classroom_competency BEFORE INSERT OR UPDATE OF classroom_id, competency_number ON assessment.term_grade
    FOR EACH ROW EXECUTE FUNCTION assessment.check_classroom_competency();

CREATE INDEX term_grade_classroom_term_idx ON assessment.term_grade (classroom_id, term_id);

-- Acta final por área: lo que se firma y se transmite a SIAGIE.
CREATE TABLE assessment.final_grade (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    enrollment_id  uuid NOT NULL,
    area_code      text NOT NULL REFERENCES curriculum.area (code),
    level          curriculum.achievement_level,
    -- Exonerado (ej. Educación Religiosa, Ley N.° 29635): sin nivel.
    exempted       boolean NOT NULL DEFAULT false,
    status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'signed', 'sent')),
    signed_by      uuid REFERENCES iam.account (id),
    signed_at      timestamptz,
    siagie_sent_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (enrollment_id, area_code),
    FOREIGN KEY (institution_id, enrollment_id) REFERENCES enrollment.enrollment (institution_id, id) ON DELETE CASCADE,
    CHECK (exempted = (level IS NULL)),
    CHECK (status = 'draft' OR signed_at IS NOT NULL),
    CHECK (status <> 'sent' OR siagie_sent_at IS NOT NULL)
);

SELECT util.attach_updated_at('assessment');

SELECT audit.attach('assessment.result');
SELECT audit.attach('assessment.term_grade');
SELECT audit.attach('assessment.final_grade');
