-- Management type of the institution (tipo de gestión): public institutions charge no monthly tuition (pensión),
-- families only pay the enrollment and the parents' association fee (cuota APAFA, Ley N.° 28628); private ones
-- keep enrollment + tuition + other concepts. Written so it can run twice (it was applied by hand on the shared
-- dev database before the migration number was free)
ALTER TABLE academic.settings
    ADD COLUMN IF NOT EXISTS management varchar(10) NOT NULL DEFAULT 'private';
ALTER TABLE academic.settings DROP CONSTRAINT IF EXISTS settings_management_check;
ALTER TABLE academic.settings
    ADD CONSTRAINT settings_management_check CHECK (management IN ('public', 'private'));

-- New concept: the APAFA fee
ALTER TABLE finance.fee_items DROP CONSTRAINT IF EXISTS fee_items_concept_check;
ALTER TABLE finance.fee_items
    ADD CONSTRAINT fee_items_concept_check CHECK (concept IN ('enrollment', 'tuition', 'apafa', 'other'));
ALTER TABLE finance.charges DROP CONSTRAINT IF EXISTS charges_concept_check;
ALTER TABLE finance.charges
    ADD CONSTRAINT charges_concept_check CHECK (concept IN ('enrollment', 'tuition', 'apafa', 'other'));

-- Management of an institution; one without a profile yet counts as private (the column default)
CREATE OR REPLACE FUNCTION finance.management_of(inst uuid) RETURNS varchar
    LANGUAGE sql STABLE AS $$
    SELECT coalesce((SELECT management FROM academic.settings WHERE institution_id = inst), 'private')
$$;

-- Guard: a public institution adds no tuition, neither as a plan item nor as a charge. Rows that already were
-- tuition (history from before a switch) may still be updated, as long as they stay what they were
CREATE OR REPLACE FUNCTION finance.forbid_public_tuition() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    inst uuid;
BEGIN
    IF NEW.concept <> 'tuition' OR (TG_OP = 'UPDATE' AND OLD.concept = 'tuition') THEN
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'fee_items' THEN
        SELECT institution_id INTO inst FROM finance.fee_plans WHERE id = NEW.plan_id;
    ELSE
        inst := NEW.institution_id;
    END IF;
    IF finance.management_of(inst) = 'public' THEN
        RAISE EXCEPTION 'public institutions charge no tuition' USING ERRCODE = 'check_violation', CONSTRAINT = 'public_no_tuition';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS fee_items_public_no_tuition ON finance.fee_items;
CREATE TRIGGER fee_items_public_no_tuition BEFORE INSERT OR UPDATE OF concept ON finance.fee_items
    FOR EACH ROW EXECUTE FUNCTION finance.forbid_public_tuition();
DROP TRIGGER IF EXISTS charges_public_no_tuition ON finance.charges;
CREATE TRIGGER charges_public_no_tuition BEFORE INSERT OR UPDATE OF concept ON finance.charges
    FOR EACH ROW EXECUTE FUNCTION finance.forbid_public_tuition();

-- Guard: an institution turns public only when no plan of a period still running (or being planned) has tuition
-- items. The API checks it first with a readable message (and also asks for the pending tuition to be settled)
CREATE OR REPLACE FUNCTION finance.check_public_switch() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM finance.fee_items i
        JOIN finance.fee_plans p ON p.id = i.plan_id
        JOIN academic.periods pe ON pe.id = p.period_id
        WHERE p.institution_id = NEW.institution_id AND i.concept = 'tuition' AND pe.status <> 'closed'
    ) THEN
        RAISE EXCEPTION 'fee plans of open periods still have tuition' USING ERRCODE = 'check_violation', CONSTRAINT = 'public_no_tuition';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS settings_public_switch ON academic.settings;
CREATE TRIGGER settings_public_switch BEFORE UPDATE OF management ON academic.settings
    FOR EACH ROW WHEN (NEW.management = 'public' AND OLD.management <> 'public')
    EXECUTE FUNCTION finance.check_public_switch();
