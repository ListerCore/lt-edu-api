DROP TRIGGER IF EXISTS settings_public_switch ON academic.settings;
DROP TRIGGER IF EXISTS charges_public_no_tuition ON finance.charges;
DROP TRIGGER IF EXISTS fee_items_public_no_tuition ON finance.fee_items;
DROP FUNCTION IF EXISTS finance.check_public_switch();
DROP FUNCTION IF EXISTS finance.forbid_public_tuition();
DROP FUNCTION IF EXISTS finance.management_of(uuid);

-- The APAFA fee goes back to "other concept" (its name still says what it was)
UPDATE finance.charges SET concept = 'other' WHERE concept = 'apafa';
UPDATE finance.fee_items SET concept = 'other' WHERE concept = 'apafa';
ALTER TABLE finance.charges DROP CONSTRAINT IF EXISTS charges_concept_check;
ALTER TABLE finance.charges
    ADD CONSTRAINT charges_concept_check CHECK (concept IN ('enrollment', 'tuition', 'other'));
ALTER TABLE finance.fee_items DROP CONSTRAINT IF EXISTS fee_items_concept_check;
ALTER TABLE finance.fee_items
    ADD CONSTRAINT fee_items_concept_check CHECK (concept IN ('enrollment', 'tuition', 'other'));

ALTER TABLE academic.settings DROP CONSTRAINT IF EXISTS settings_management_check;
ALTER TABLE academic.settings DROP COLUMN IF EXISTS management;
