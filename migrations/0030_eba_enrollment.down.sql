DELETE FROM documents.issued WHERE kind = 'placement';
DELETE FROM documents.signers WHERE kind = 'placement';
ALTER TABLE documents.issued DROP CONSTRAINT issued_kind_check;
ALTER TABLE documents.issued ADD CONSTRAINT issued_kind_check CHECK (kind IN ('schedules'));
ALTER TABLE documents.signers DROP CONSTRAINT signers_kind_check;
ALTER TABLE documents.signers ADD CONSTRAINT signers_kind_check CHECK (kind IN ('schedules'));

DROP TABLE enrollment.placement_subjects;
DROP INDEX enrollment.placements_act_number_key;
DELETE FROM enrollment.placements WHERE kind IN ('revalidation', 'equivalence');
ALTER TABLE enrollment.placements
    DROP CONSTRAINT placements_act_check,
    DROP CONSTRAINT placements_kind_check,
    DROP COLUMN document_id,
    DROP COLUMN act_number,
    DROP COLUMN act_year,
    DROP COLUMN origin_studies,
    DROP COLUMN origin;
ALTER TABLE enrollment.placements ADD CONSTRAINT placements_kind_check CHECK (kind IN ('placement_test', 'validation'));

DROP TABLE enrollment.siagie_records;
DROP TABLE enrollment.religion_exemptions;
DROP TABLE people.enrollment_files;
ALTER TABLE academic.sections DROP COLUMN multigrade;
