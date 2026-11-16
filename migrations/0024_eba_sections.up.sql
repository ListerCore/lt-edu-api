-- EBA foundations (docs/ceba/PLAN.md, phase E0): the program each EBA section belongs to and, for the distance form,
-- whether it runs online or itinerant.
--   program: pebaja (Programa de EBA de Jóvenes y Adultos, from 14) | literacy (Alfabetización, from 15, learning
--            circles of the inicial and intermedio cycles, in person). NULL outside EBA
--   distance_variant: virtual (with internet) | itinerant (no connectivity or high dispersion). Only for distance
ALTER TABLE academic.sections
    ADD COLUMN program varchar(20),
    ADD COLUMN distance_variant varchar(20),
    ADD CONSTRAINT sections_program_check CHECK (program IS NULL OR program IN ('pebaja', 'literacy')),
    ADD CONSTRAINT sections_distance_variant_check CHECK (
        distance_variant IS NULL OR (attention_mode = 'distance' AND distance_variant IN ('virtual', 'itinerant'))
    );

-- Sections created before this were all PEBAJA, and the distance ones online
UPDATE academic.sections s SET program = 'pebaja'
FROM academic.levels l JOIN iam.institutions i ON i.id = l.institution_id
WHERE l.id = s.level_id AND i.kind = 'eba';

UPDATE academic.sections SET distance_variant = 'virtual' WHERE attention_mode = 'distance';
