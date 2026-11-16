DELETE FROM documents.signers WHERE kind IN ('evaluation_record', 'certificate');
DELETE FROM documents.issued WHERE kind IN ('evaluation_record', 'certificate');
ALTER TABLE documents.signers DROP CONSTRAINT signers_kind_check;
ALTER TABLE documents.signers ADD CONSTRAINT signers_kind_check CHECK (kind IN ('schedules', 'placement'));
ALTER TABLE documents.issued DROP CONSTRAINT issued_kind_check;
ALTER TABLE documents.issued ADD CONSTRAINT issued_kind_check CHECK (kind IN ('schedules', 'placement'));

DROP TABLE enrollment.siagie_grades;
DROP TABLE enrollment.certificates;
ALTER TABLE assessment.records DROP COLUMN document_id;
DROP TABLE assessment.promotion_rules;
