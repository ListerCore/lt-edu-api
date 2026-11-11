DROP TABLE IF EXISTS academic.schedule_entries;
DROP TABLE IF EXISTS academic.assignments;
DROP TABLE IF EXISTS academic.time_blocks;
ALTER TABLE academic.settings DROP COLUMN IF EXISTS week_days;
ALTER TABLE academic.sections DROP COLUMN IF EXISTS tutor_id;
DROP TABLE IF EXISTS people.staff;
