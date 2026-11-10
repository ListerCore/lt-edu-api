-- Communications: announcements, notices and alerts of each institution, with their images and PDFs.
-- Files live in object storage (AIStor); these rows hold where and what they are
CREATE SCHEMA IF NOT EXISTS comms;

CREATE TABLE comms.announcements (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- Public address (/news/<slug>): 18 random lowercase letters and digits, never derived from the title
    slug char(18) NOT NULL,
    -- announcement: general news · notice: official communication · alert: needs action or attention
    kind varchar(20) NOT NULL,
    audience varchar(20) NOT NULL,
    title varchar(160) NOT NULL,
    body text NOT NULL,
    -- Also shown on the public site (/news); otherwise only inside the panel
    is_public boolean NOT NULL DEFAULT false,
    pinned boolean NOT NULL DEFAULT false,
    author_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    published_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT announcements_slug_key UNIQUE (slug),
    CONSTRAINT announcements_slug_format CHECK (slug ~ '^[a-z0-9]{18}$'),
    CONSTRAINT announcements_kind_check CHECK (kind IN ('announcement', 'notice', 'alert')),
    CONSTRAINT announcements_audience_check CHECK (audience IN ('community', 'students', 'guardians', 'staff')),
    CONSTRAINT announcements_title_length CHECK (char_length(btrim(title)) >= 5),
    CONSTRAINT announcements_body_length CHECK (char_length(btrim(body)) BETWEEN 10 AND 5000)
);

CREATE TRIGGER announcements_touch_updated_at BEFORE UPDATE ON comms.announcements
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- The panel feed and the public site: pinned first, newest first
CREATE INDEX idx_comms_announcements_feed ON comms.announcements (institution_id, pinned DESC, published_at DESC);
CREATE INDEX idx_comms_announcements_public ON comms.announcements (institution_id, pinned DESC, published_at DESC) WHERE is_public;

-- Images (the first one is the cover) and PDFs of an announcement, in the order they were attached
CREATE TABLE comms.announcement_attachments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    announcement_id uuid NOT NULL REFERENCES comms.announcements (id) ON DELETE CASCADE,
    kind varchar(20) NOT NULL,
    -- Key in the storage bucket; the bytes are only reachable through the API, which checks access
    object_key varchar(255) NOT NULL,
    -- Name the person uploaded, used when downloading
    file_name varchar(255) NOT NULL,
    content_type varchar(100) NOT NULL,
    size_bytes bigint NOT NULL,
    position smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT announcement_attachments_object_key_key UNIQUE (object_key),
    CONSTRAINT announcement_attachments_position_key UNIQUE (announcement_id, position),
    CONSTRAINT announcement_attachments_kind_check CHECK (kind IN ('image', 'document')),
    CONSTRAINT announcement_attachments_type_check CHECK (
        (kind = 'image' AND content_type IN ('image/png', 'image/jpeg', 'image/webp'))
        OR (kind = 'document' AND content_type = 'application/pdf')
    ),
    -- Images up to 5 MB, PDFs up to 10 MB (the API enforces the same limits before uploading)
    CONSTRAINT announcement_attachments_size_check CHECK (
        size_bytes > 0 AND size_bytes <= CASE kind WHEN 'image' THEN 5242880 ELSE 10485760 END
    ),
    CONSTRAINT announcement_attachments_position_check CHECK (position BETWEEN 0 AND 9),
    CONSTRAINT announcement_attachments_file_name_required CHECK (btrim(file_name) <> '')
);
