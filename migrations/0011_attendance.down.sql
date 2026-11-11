DROP SCHEMA IF EXISTS attendance CASCADE;
ALTER TABLE academic.settings DROP COLUMN IF EXISTS late_after_minutes, DROP COLUMN IF EXISTS absence_limit_percent;
ALTER TABLE people.students DROP COLUMN IF EXISTS card_token;
ALTER TABLE people.staff DROP COLUMN IF EXISTS card_token;
