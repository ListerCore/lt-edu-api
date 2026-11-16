ALTER TABLE academic.sections
    DROP CONSTRAINT IF EXISTS sections_distance_variant_check,
    DROP CONSTRAINT IF EXISTS sections_program_check,
    DROP COLUMN IF EXISTS distance_variant,
    DROP COLUMN IF EXISTS program;
