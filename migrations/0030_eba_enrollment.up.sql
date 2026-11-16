-- EBA enrollment (docs/ceba/PLAN.md, phase E1): the ficha única, multigrade sections, the exemption from Educación
-- Religiosa, the SIAGIE upload state and the placement, validation and revalidation procedures with their acta

-- A multigrade section gathers students of several grades of its cycle; grade_id stays as the lowest one
ALTER TABLE academic.sections ADD COLUMN multigrade boolean NOT NULL DEFAULT false;

-- What the ficha única de matrícula asks beyond the student record. One per student, kept across periods
CREATE TABLE people.enrollment_files (
    student_id uuid PRIMARY KEY REFERENCES people.students (id) ON DELETE CASCADE,
    -- castellano | quechua | aimara | asháninka | other (free text in mother_tongue_other)
    mother_tongue varchar(20) NOT NULL DEFAULT 'castellano',
    mother_tongue_other varchar(60) NOT NULL DEFAULT '',
    occupation varchar(120) NOT NULL DEFAULT '',
    -- Kind of disability when special_needs is set (visual, auditiva, física, intelectual…), free text
    disability varchar(200) NOT NULL DEFAULT '',
    -- none | mobile | home | public (cabina, municipalidad) | unknown
    internet varchar(20) NOT NULL DEFAULT 'unknown',
    -- none | phone | tablet | computer | unknown
    device varchar(20) NOT NULL DEFAULT 'unknown',
    -- Form of attention the student asks for: in_person | blended | distance
    preferred_mode varchar(20),
    -- Last studies passed, as the student tells them ("6.° de primaria, EBR, 2009")
    last_studies varchar(160) NOT NULL DEFAULT '',
    -- Documents shown at enrollment: dni | study_certificate | birth_certificate | placement_act | other
    documents text[] NOT NULL DEFAULT '{}',
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT enrollment_files_tongue_check CHECK (mother_tongue IN ('castellano', 'quechua', 'aimara', 'ashaninka', 'other')),
    CONSTRAINT enrollment_files_internet_check CHECK (internet IN ('none', 'mobile', 'home', 'public', 'unknown')),
    CONSTRAINT enrollment_files_device_check CHECK (device IN ('none', 'phone', 'tablet', 'computer', 'unknown')),
    CONSTRAINT enrollment_files_mode_check CHECK (preferred_mode IS NULL OR preferred_mode IN ('in_person', 'blended', 'distance')),
    CONSTRAINT enrollment_files_documents_check CHECK (documents <@ ARRAY['dni', 'study_certificate', 'birth_certificate', 'placement_act', 'other']::text[])
);

CREATE TRIGGER enrollment_files_touch_updated_at BEFORE UPDATE ON people.enrollment_files
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Exemption from Educación Religiosa for the period (Ley 29635), asked by the student or their guardian. An exempt
-- student leaves the area's register and the report card reads "Exonerado"
CREATE TABLE enrollment.religion_exemptions (
    enrollment_id uuid PRIMARY KEY REFERENCES enrollment.enrollments (id) ON DELETE CASCADE,
    requested_on date NOT NULL,
    -- Directoral resolution that grants it
    reference varchar(120) NOT NULL DEFAULT '',
    granted_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Whether each enrollment was loaded into the SIAGIE: pending until exported; observed when the UGEL or the SIAGIE
-- rejected it, with the reason
CREATE TABLE enrollment.siagie_records (
    enrollment_id uuid PRIMARY KEY REFERENCES enrollment.enrollments (id) ON DELETE CASCADE,
    status varchar(20) NOT NULL DEFAULT 'pending',
    note varchar(300) NOT NULL DEFAULT '',
    exported_at timestamptz,
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT siagie_records_status_check CHECK (status IN ('pending', 'exported', 'observed')),
    CONSTRAINT siagie_records_note_check CHECK (status <> 'observed' OR btrim(note) <> '')
);

CREATE TRIGGER siagie_records_touch_updated_at BEFORE UPDATE ON enrollment.siagie_records
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Placement procedures: revalidation (studies abroad or of a former program) and equivalence (work competencies or
-- certifications) join the placement test and the validation. Where the student comes from, and the acta the
-- direction signs (an issued document of kind placement) with its yearly number
ALTER TABLE enrollment.placements DROP CONSTRAINT placements_kind_check;
ALTER TABLE enrollment.placements
    ADD CONSTRAINT placements_kind_check CHECK (kind IN ('placement_test', 'validation', 'revalidation', 'equivalence')),
    ADD COLUMN origin varchar(160) NOT NULL DEFAULT '',
    -- Studies the student brings ("3.° de secundaria, EBR") or the test result
    ADD COLUMN origin_studies varchar(160) NOT NULL DEFAULT '',
    ADD COLUMN act_year smallint,
    ADD COLUMN act_number integer,
    ADD COLUMN document_id uuid REFERENCES documents.issued (id) ON DELETE SET NULL,
    ADD CONSTRAINT placements_act_check CHECK ((act_year IS NULL) = (act_number IS NULL));

CREATE UNIQUE INDEX placements_act_number_key ON enrollment.placements (institution_id, act_year, act_number) WHERE act_number IS NOT NULL;

-- Areas validated by the procedure (the student does not take them again)
CREATE TABLE enrollment.placement_subjects (
    placement_id uuid NOT NULL REFERENCES enrollment.placements (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    PRIMARY KEY (placement_id, subject_id)
);

-- The acta de ubicación o convalidación is signed through documents
ALTER TABLE documents.signers DROP CONSTRAINT signers_kind_check;
ALTER TABLE documents.signers ADD CONSTRAINT signers_kind_check CHECK (kind IN ('schedules', 'placement'));
ALTER TABLE documents.issued DROP CONSTRAINT issued_kind_check;
ALTER TABLE documents.issued ADD CONSTRAINT issued_kind_check CHECK (kind IN ('schedules', 'placement'));
