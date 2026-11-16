-- Institution file (docs/ceba/PLAN.md, phase E0): what the institution is authorized to offer and the technical
-- sheet of the e-learning platform a CEBA presents to the DRE (RSG N.° 613-2014-MINEDU, numeral 6.2.1), plus the
-- Responsable técnico role that keeps that sheet and, later, verifies the evidence of the technical reports

-- Resolutions that authorize the institution: its creation, each form of attention per cycle (EBA), an expansion
-- of the offer, or another act. The PDF of each one hangs on it as an attachment (owner_kind authorization)
CREATE TABLE academic.authorizations (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- creation | attention_mode | expansion | other
    kind varchar(20) NOT NULL,
    -- attention_mode: the cycle and the form it authorizes; NULL cycle means every cycle
    level_id uuid REFERENCES academic.levels (id) ON DELETE SET NULL,
    attention_mode varchar(20),
    -- Number of the resolution ("RD N.° 01234-2026-DREA") and who issued it (DRE, UGEL, MINEDU)
    resolution varchar(120) NOT NULL,
    issued_by varchar(160) NOT NULL DEFAULT '',
    issued_on date NOT NULL,
    -- Some authorizations are granted for a term
    expires_on date,
    -- Expediente of the procedure that led to it ("2026-0013636")
    file_number varchar(40) NOT NULL DEFAULT '',
    notes varchar(500) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT authorizations_kind_check CHECK (kind IN ('creation', 'attention_mode', 'expansion', 'other')),
    CONSTRAINT authorizations_mode_check CHECK (
        (kind = 'attention_mode' AND attention_mode IN ('in_person', 'blended', 'distance'))
        OR (kind <> 'attention_mode' AND attention_mode IS NULL AND level_id IS NULL)
    ),
    CONSTRAINT authorizations_resolution_required CHECK (btrim(resolution) <> ''),
    CONSTRAINT authorizations_term_check CHECK (expires_on IS NULL OR expires_on >= issued_on)
);

CREATE TRIGGER authorizations_touch_updated_at BEFORE UPDATE ON academic.authorizations
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_academic_authorizations_institution ON academic.authorizations (institution_id, issued_on DESC);

-- Technical sheet of the institution's e-learning platform, one per institution (informe 6.2.1, sections III and IV)
CREATE TABLE academic.platform_sheets (
    institution_id uuid PRIMARY KEY REFERENCES iam.institutions (id) ON DELETE CASCADE,
    platform_name varchar(120) NOT NULL DEFAULT '',
    url varchar(300) NOT NULL DEFAULT '',
    technology varchar(200) NOT NULL DEFAULT '',
    -- Who runs it day to day (person or company)
    administrator varchar(160) NOT NULL DEFAULT '',
    -- Profiles with access, as the report lists them
    access_profiles varchar(300) NOT NULL DEFAULT '',
    -- hosting (a hosting service) | own_server | other (described in hosting_other)
    hosting_kind varchar(20) NOT NULL DEFAULT 'hosting',
    hosting_other varchar(120) NOT NULL DEFAULT '',
    provider varchar(160) NOT NULL DEFAULT '',
    -- Name of the service, instance or server, and the contract or panel that proves it
    instance varchar(160) NOT NULL DEFAULT '',
    contract_reference varchar(160) NOT NULL DEFAULT '',
    service_status varchar(160) NOT NULL DEFAULT '',
    valid_until date,
    technical_lead varchar(160) NOT NULL DEFAULT '',
    technical_lead_specialty varchar(160) NOT NULL DEFAULT '',
    technical_lead_cip varchar(20) NOT NULL DEFAULT '',
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT platform_sheets_hosting_kind_check CHECK (hosting_kind IN ('hosting', 'own_server', 'other')),
    CONSTRAINT platform_sheets_url_check CHECK (url = '' OR url ~ '^https?://[^\s<>"]+$')
);

CREATE TRIGGER platform_sheets_touch_updated_at BEFORE UPDATE ON academic.platform_sheets
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- The resolution PDFs and the hosting evidence hang on these records
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet'
));

-- Responsable técnico: keeps the platform sheet, the equipment and the evidence of the technical reports
INSERT INTO iam.roles (code, name, description, scope) VALUES
    ('technical_lead', 'Responsable técnico', 'Ficha de la plataforma, equipos y evidencias de los informes técnicos.', 'institution')
ON CONFLICT (code) DO NOTHING;
