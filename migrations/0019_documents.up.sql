-- Issued documents with versions, signatures and public verification. A document (one timetable publication, for
-- now) keeps a tracking code for its whole life; each generation that changes its content is a new version with its
-- own verification code, frozen content and signatures. A version is published once every signer has signed, and the
-- version it replaces stays verifiable as superseded
CREATE SCHEMA IF NOT EXISTS documents;

-- Who signs each kind of document, in order: the director alone, or several people (a coordinator and the director,
-- an appointed person in charge). The position is what the signature prints under the name
CREATE TABLE documents.signers (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- schedules
    kind varchar(30) NOT NULL,
    staff_id uuid NOT NULL REFERENCES people.staff (id) ON DELETE CASCADE,
    position varchar(120) NOT NULL,
    sort smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT signers_staff_key UNIQUE (institution_id, kind, staff_id),
    CONSTRAINT signers_kind_check CHECK (kind IN ('schedules')),
    CONSTRAINT signers_position_required CHECK (btrim(position) <> '')
);

-- A document as the institution publishes it: the kind and what it covers (period, mode and target of a timetable)
CREATE TABLE documents.issued (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    kind varchar(30) NOT NULL,
    scope_key varchar(200) NOT NULL,
    title varchar(200) NOT NULL,
    -- Public, stable across versions: /seguimiento/<tracking_code>
    tracking_code varchar(12) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT issued_scope_key UNIQUE (institution_id, kind, scope_key),
    CONSTRAINT issued_tracking_key UNIQUE (tracking_code),
    CONSTRAINT issued_kind_check CHECK (kind IN ('schedules'))
);

-- Each version freezes the content it was generated with (the printable view model) and its hash
CREATE TABLE documents.versions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    document_id uuid NOT NULL REFERENCES documents.issued (id) ON DELETE CASCADE,
    number int NOT NULL,
    -- XXXX-XXXX-XXXX, printed next to the QR
    verification_code varchar(14) NOT NULL,
    content jsonb NOT NULL,
    content_hash char(64) NOT NULL,
    -- pending (waiting for signatures) | published | superseded (a newer one was published) | cancelled (a newer
    -- one was generated before this one was signed)
    status varchar(20) NOT NULL DEFAULT 'pending',
    note varchar(300) NOT NULL DEFAULT '',
    created_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_name varchar(200) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    CONSTRAINT versions_number_key UNIQUE (document_id, number),
    CONSTRAINT versions_code_key UNIQUE (verification_code),
    CONSTRAINT versions_status_check CHECK (status IN ('pending', 'published', 'superseded', 'cancelled'))
);

CREATE INDEX idx_documents_versions_document ON documents.versions (document_id, number DESC);

-- The signatures a version needs, copied from the signers when it was generated
CREATE TABLE documents.signatures (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    version_id uuid NOT NULL REFERENCES documents.versions (id) ON DELETE CASCADE,
    sort smallint NOT NULL,
    staff_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    name varchar(200) NOT NULL,
    position varchar(120) NOT NULL,
    signed_at timestamptz,
    signed_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    -- XXXX-XXXX-XXXX of the signature itself, once signed
    code varchar(14),
    CONSTRAINT signatures_sort_key UNIQUE (version_id, sort),
    CONSTRAINT signatures_code_key UNIQUE (code),
    CONSTRAINT signatures_signed_check CHECK ((signed_at IS NULL) = (code IS NULL))
);

CREATE INDEX idx_documents_signatures_staff ON documents.signatures (staff_id) WHERE signed_at IS NULL;

-- History of a document: generated, signed, published, superseded, cancelled
CREATE TABLE documents.events (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    document_id uuid NOT NULL REFERENCES documents.issued (id) ON DELETE CASCADE,
    version_number int NOT NULL,
    action varchar(20) NOT NULL,
    actor_name varchar(200) NOT NULL DEFAULT '',
    note varchar(300) NOT NULL DEFAULT '',
    at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT events_action_check CHECK (action IN ('generated', 'signed', 'published', 'superseded', 'cancelled'))
);

CREATE INDEX idx_documents_events_document ON documents.events (document_id, at);
