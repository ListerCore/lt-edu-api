ALTER TABLE academic.settings
    DROP CONSTRAINT settings_logo_check,
    DROP CONSTRAINT settings_modular_code_check,
    DROP COLUMN logo,
    DROP COLUMN modular_code,
    DROP COLUMN ugel,
    DROP COLUMN dre;
