-- Phase E8 (docs/ceba/PLAN.md): DRE compliance. The environment and equipment of the virtual tutors with their
-- connectivity and platform-access checks (RSG 613-2014-MINEDU, 6.3.2 c), the verification matrices of the two
-- technical reports (6.2.1 and Anexo 2; 6.3.2 c) with their evidence, and an availability log of the platform URL
CREATE SCHEMA IF NOT EXISTS equipment;
CREATE SCHEMA IF NOT EXISTS compliance;

-- A physical environment where tutors work
CREATE TABLE equipment.rooms (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    -- Floor and place in the building
    location varchar(200) NOT NULL DEFAULT '',
    area_m2 numeric(6, 1),
    workstations smallint NOT NULL DEFAULT 0,
    furniture varchar(500) NOT NULL DEFAULT '',
    power_supply varchar(300) NOT NULL DEFAULT '',
    notes varchar(500) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rooms_name_key UNIQUE (institution_id, name),
    CONSTRAINT rooms_area_check CHECK (area_m2 IS NULL OR area_m2 > 0),
    CONSTRAINT rooms_workstations_check CHECK (workstations BETWEEN 0 AND 500)
);

CREATE TRIGGER rooms_touch_updated_at BEFORE UPDATE ON equipment.rooms FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- A device of the inventory (PC-01, LAP-01…)
CREATE TABLE equipment.devices (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    code varchar(20) NOT NULL,
    -- desktop | laptop | tablet | network | printer | other
    kind varchar(20) NOT NULL,
    brand varchar(80) NOT NULL DEFAULT '',
    model varchar(120) NOT NULL DEFAULT '',
    specs varchar(300) NOT NULL DEFAULT '',
    room_id uuid REFERENCES equipment.rooms (id) ON DELETE SET NULL,
    -- virtual_tutoring | student_support | administration | shared
    purpose varchar(20) NOT NULL,
    -- Who answers for it, and who uses it when it is not a staff member ("Tutores de 3.° V")
    staff_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    assigned_to varchar(160) NOT NULL DEFAULT '',
    -- operational | pending
    status varchar(20) NOT NULL DEFAULT 'operational',
    notes varchar(500) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT devices_code_key UNIQUE (institution_id, code),
    CONSTRAINT devices_kind_check CHECK (kind IN ('desktop', 'laptop', 'tablet', 'network', 'printer', 'other')),
    CONSTRAINT devices_purpose_check CHECK (purpose IN ('virtual_tutoring', 'student_support', 'administration', 'shared')),
    CONSTRAINT devices_status_check CHECK (status IN ('operational', 'pending'))
);

CREATE TRIGGER devices_touch_updated_at BEFORE UPDATE ON equipment.devices FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- A connectivity test (of a room or a device) or a platform-access test from a device
CREATE TABLE equipment.checks (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- connectivity | platform_access
    kind varchar(20) NOT NULL,
    room_id uuid REFERENCES equipment.rooms (id) ON DELETE CASCADE,
    device_id uuid REFERENCES equipment.devices (id) ON DELETE CASCADE,
    checked_on date NOT NULL,
    -- Internet provider and link (fiber | adsl | wireless | mobile | other) of a connectivity test
    provider varchar(120) NOT NULL DEFAULT '',
    link_kind varchar(20) NOT NULL DEFAULT '',
    download_mbps numeric(7, 1),
    upload_mbps numeric(7, 1),
    latency_ms integer,
    -- Profile used to sign in on a platform-access test (tutor, administrador…), never a password
    profile varchar(60) NOT NULL DEFAULT '',
    passed boolean NOT NULL,
    result varchar(500) NOT NULL DEFAULT '',
    checked_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT checks_kind_check CHECK (kind IN ('connectivity', 'platform_access')),
    CONSTRAINT checks_link_check CHECK (link_kind IN ('', 'fiber', 'adsl', 'wireless', 'mobile', 'other')),
    CONSTRAINT checks_target_check CHECK (room_id IS NOT NULL OR device_id IS NOT NULL),
    CONSTRAINT checks_access_check CHECK (kind <> 'platform_access' OR device_id IS NOT NULL)
);

CREATE INDEX idx_equipment_checks_institution ON equipment.checks (institution_id, checked_on DESC);

-- The data of each technical report: number, file, evaluation date and the conclusions the technical lead writes
CREATE TABLE compliance.reports (
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- platform (6.2.1 and Anexo 2) | equipment (6.3.2 c)
    kind varchar(20) NOT NULL,
    number varchar(30) NOT NULL DEFAULT '',
    file_number varchar(40) NOT NULL DEFAULT '',
    city varchar(60) NOT NULL DEFAULT '',
    location varchar(300) NOT NULL DEFAULT '',
    evaluated_on date,
    general_result varchar(1500) NOT NULL DEFAULT '',
    limitations varchar(1500) NOT NULL DEFAULT '',
    conclusion varchar(1500) NOT NULL DEFAULT '',
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, kind),
    CONSTRAINT reports_kind_check CHECK (kind IN ('platform', 'equipment'))
);

CREATE TRIGGER reports_touch_updated_at BEFORE UPDATE ON compliance.reports FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- A row of a verification matrix: the element of the norm, where it lives in the platform, its evidence (annex and
-- folio, the captures hang on it as attachments) and its state. Only the technical lead marks it verified
CREATE TABLE compliance.items (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    report varchar(20) NOT NULL,
    -- informative | classroom (Anexo 2) | equipment (6.3.2 c)
    section varchar(20) NOT NULL,
    code varchar(40) NOT NULL,
    position smallint NOT NULL,
    element varchar(200) NOT NULL,
    module varchar(200) NOT NULL DEFAULT '',
    route varchar(200) NOT NULL DEFAULT '',
    annex varchar(10) NOT NULL DEFAULT '',
    folio varchar(10) NOT NULL DEFAULT '',
    -- pending | verified | not_applicable
    status varchar(20) NOT NULL DEFAULT 'pending',
    notes varchar(500) NOT NULL DEFAULT '',
    verified_by_name varchar(160) NOT NULL DEFAULT '',
    verified_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT items_key UNIQUE (institution_id, report, code),
    CONSTRAINT items_report_check CHECK (report IN ('platform', 'equipment')),
    CONSTRAINT items_section_check CHECK (section IN ('informative', 'classroom', 'equipment')),
    CONSTRAINT items_status_check CHECK (status IN ('pending', 'verified', 'not_applicable')),
    CONSTRAINT items_verified_check CHECK ((status = 'verified') = (verified_at IS NOT NULL))
);

CREATE TRIGGER items_touch_updated_at BEFORE UPDATE ON compliance.items FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Availability log of the platform URL (status code and response time), the sustento of the hosting
CREATE TABLE compliance.uptime_checks (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    url varchar(300) NOT NULL,
    checked_at timestamptz NOT NULL DEFAULT now(),
    status_code smallint,
    response_ms integer,
    ok boolean NOT NULL,
    error varchar(300) NOT NULL DEFAULT ''
);

CREATE INDEX idx_compliance_uptime_institution ON compliance.uptime_checks (institution_id, checked_at DESC);

-- Plans, photos, captures and the hosting papers hang on these records
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet', 'classroom_item', 'library_item',
    'submission', 'equipment_room', 'equipment_device', 'equipment_check', 'compliance_item'
));
