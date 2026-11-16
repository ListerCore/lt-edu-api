DELETE FROM attachments.files WHERE owner_kind IN ('equipment_room', 'equipment_device', 'equipment_check', 'compliance_item');
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet', 'classroom_item', 'library_item',
    'submission'
));
DROP SCHEMA compliance CASCADE;
DROP SCHEMA equipment CASCADE;
