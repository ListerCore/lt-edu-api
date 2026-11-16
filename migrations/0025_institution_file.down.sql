DELETE FROM iam.user_roles WHERE role_id = (SELECT id FROM iam.roles WHERE code = 'technical_lead');
DELETE FROM iam.invitations WHERE role_id = (SELECT id FROM iam.roles WHERE code = 'technical_lead');
DELETE FROM iam.roles WHERE code = 'technical_lead';

DELETE FROM attachments.files WHERE owner_kind IN ('authorization', 'platform_sheet');
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting'
));

DROP TABLE IF EXISTS academic.platform_sheets;
DROP TABLE IF EXISTS academic.authorizations;
