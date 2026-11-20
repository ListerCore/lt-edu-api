DROP SCHEMA live CASCADE;
DROP TABLE wellbeing.psych_appointments;
ALTER TABLE wellbeing.tutoring_sessions DROP COLUMN provider;
ALTER TABLE classroom.items DROP COLUMN provider;
