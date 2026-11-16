DELETE FROM attachments.files WHERE owner_kind = 'submission';
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet', 'classroom_item', 'library_item'
));
DROP TABLE IF EXISTS classroom.extensions;
DROP TABLE IF EXISTS classroom.submissions;
DROP TABLE IF EXISTS classroom.questions;
DROP TABLE IF EXISTS classroom.assessments;
DELETE FROM classroom.items WHERE kind IN ('quiz', 'task');
ALTER TABLE classroom.items DROP CONSTRAINT items_kind_check;
ALTER TABLE classroom.items ADD CONSTRAINT items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link', 'live', 'activity'));
