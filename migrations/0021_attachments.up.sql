-- Attachments: PDFs, scans, annexes and other files hung on any record of an institution (a student's file, an
-- enrollment, a period resolution, an acta...). One generic table; owner_kind + owner_id say what the file belongs
-- to, and the API decides who may read or change it from the module that owner lives in. The bytes live in object
-- storage (AIStor); these rows hold where and what they are
CREATE SCHEMA IF NOT EXISTS attachments;

CREATE TABLE attachments.files (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- What the file hangs on. No foreign key: owners live in many schemas (the API checks they belong to the
    -- institution when it uploads)
    owner_kind varchar(40) NOT NULL,
    owner_id uuid NOT NULL,
    -- dni, birth_certificate, minutes, resolution, annex, other... (the API keeps the list allowed per owner kind)
    category varchar(40) NOT NULL,
    title varchar(160),
    description varchar(500),
    -- Key in the storage bucket; the bytes are only reachable through the API, which checks access
    object_key varchar(255) NOT NULL,
    -- Name the person uploaded, used when downloading
    file_name varchar(255) NOT NULL,
    content_type varchar(100) NOT NULL,
    size_bytes bigint NOT NULL,
    uploaded_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    -- Kept even when the account goes away
    uploaded_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT files_object_key_key UNIQUE (object_key),
    CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
        'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
        'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
        'attendance_justification', 'apafa_meeting'
    )),
    CONSTRAINT files_category_format CHECK (category ~ '^[a-z][a-z_]{1,39}$'),
    -- The type is sniffed from the bytes by the API, never taken from the file name
    CONSTRAINT files_type_check CHECK (content_type IN (
        'application/pdf', 'image/png', 'image/jpeg', 'image/webp',
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
    )),
    -- Up to 10 MB per file (the API enforces the same limit before uploading)
    CONSTRAINT files_size_check CHECK (size_bytes > 0 AND size_bytes <= 10485760),
    CONSTRAINT files_file_name_required CHECK (btrim(file_name) <> ''),
    CONSTRAINT files_title_length CHECK (title IS NULL OR char_length(btrim(title)) BETWEEN 1 AND 160)
);

-- The list of one owner, newest first
CREATE INDEX idx_attachments_files_owner ON attachments.files (institution_id, owner_kind, owner_id, created_at);
