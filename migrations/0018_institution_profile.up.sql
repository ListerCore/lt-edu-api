-- Official data printed on the report card (SIAGIE format): the regional office (DRE/GRE), the UGEL, the modular
-- code (código modular, 7 digits) and the institution's logo as a small image data URL
ALTER TABLE academic.settings
    ADD COLUMN dre varchar(120) NOT NULL DEFAULT '',
    ADD COLUMN ugel varchar(120) NOT NULL DEFAULT '',
    ADD COLUMN modular_code varchar(7) NOT NULL DEFAULT '',
    ADD COLUMN logo text NOT NULL DEFAULT '',
    ADD CONSTRAINT settings_modular_code_check CHECK (modular_code ~ '^([0-9]{7})?$'),
    ADD CONSTRAINT settings_logo_check CHECK (logo = '' OR (logo ~ '^data:image/(png|jpeg|webp|svg\+xml);base64,' AND length(logo) <= 140000));
