DROP SCHEMA IF EXISTS programs CASCADE;
ALTER TABLE academic.levels DROP COLUMN IF EXISTS coordinator_id;
DELETE FROM iam.roles WHERE code = 'program_coordinator';
