-- Phase E7 (docs/ceba/PLAN.md): evaluation and closing of the EBA promotional period. The promotion rule of each
-- cycle is set by the CEBA (RVM 094-2020-MINEDU and its amendments, decision D4), the consolidated acta and the
-- certificates are signed through documents, and the grades go to the SIAGIE with a loading state per student

-- How a cycle (level) decides the final situation. No row: the rule of the code (four areas, three in Avanzado)
CREATE TABLE assessment.promotion_rules (
    level_id uuid PRIMARY KEY REFERENCES academic.levels (id) ON DELETE CASCADE,
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- Promotion without conditions
    automatic boolean NOT NULL DEFAULT false,
    -- Areas with A or AD in half or more of their competencies needed to be promoted (C in more than half of them
    -- in that many areas: the student stays in the grade)
    areas smallint NOT NULL,
    note varchar(300) NOT NULL DEFAULT '',
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT promotion_rules_areas_check CHECK (areas BETWEEN 1 AND 12)
);

CREATE TRIGGER promotion_rules_touch_updated_at BEFORE UPDATE ON assessment.promotion_rules
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- The consolidated acta (and the complementary one) signed by its signers
ALTER TABLE assessment.records ADD COLUMN document_id uuid REFERENCES documents.issued (id) ON DELETE SET NULL;

-- Certificates (constancias) of enrollment, studies and conclusion of a cycle, numbered per year
CREATE TABLE enrollment.certificates (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- enrollment (in period_id) | studies (every period with results) | cycle (concluded level_id)
    kind varchar(20) NOT NULL,
    period_id uuid REFERENCES academic.periods (id) ON DELETE CASCADE,
    level_id uuid REFERENCES academic.levels (id) ON DELETE CASCADE,
    -- What the student asks it for ("trámite laboral", "traslado")
    purpose varchar(160) NOT NULL DEFAULT '',
    year smallint NOT NULL,
    number integer NOT NULL,
    document_id uuid REFERENCES documents.issued (id) ON DELETE SET NULL,
    created_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificates_kind_check CHECK (kind IN ('enrollment', 'studies', 'cycle')),
    CONSTRAINT certificates_scope_check CHECK ((kind <> 'enrollment' OR period_id IS NOT NULL) AND (kind <> 'cycle' OR level_id IS NOT NULL)),
    CONSTRAINT certificates_number_key UNIQUE (institution_id, year, number)
);

CREATE INDEX idx_enrollment_certificates_student ON enrollment.certificates (student_id, created_at DESC);

-- Loading state of the final grades of each enrollment in the SIAGIE
CREATE TABLE enrollment.siagie_grades (
    enrollment_id uuid PRIMARY KEY REFERENCES enrollment.enrollments (id) ON DELETE CASCADE,
    status varchar(20) NOT NULL DEFAULT 'pending',
    note varchar(300) NOT NULL DEFAULT '',
    exported_at timestamptz,
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT siagie_grades_status_check CHECK (status IN ('pending', 'exported', 'observed')),
    CONSTRAINT siagie_grades_note_check CHECK (status <> 'observed' OR btrim(note) <> '')
);

CREATE TRIGGER siagie_grades_touch_updated_at BEFORE UPDATE ON enrollment.siagie_grades
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

ALTER TABLE documents.signers DROP CONSTRAINT signers_kind_check;
ALTER TABLE documents.signers ADD CONSTRAINT signers_kind_check CHECK (kind IN ('schedules', 'placement', 'evaluation_record', 'certificate'));
ALTER TABLE documents.issued DROP CONSTRAINT issued_kind_check;
ALTER TABLE documents.issued ADD CONSTRAINT issued_kind_check CHECK (kind IN ('schedules', 'placement', 'evaluation_record', 'certificate'));
