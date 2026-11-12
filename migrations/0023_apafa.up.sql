-- APAFA (Asociación de Padres de Familia, Ley N.° 28628) of public institutions of Educación Básica: the meetings
-- the association convenes (ordinary, extraordinary, general assembly, board), what was agreed in each, and the
-- board (consejo directivo) of each period. Families are told through comms announcements; the agenda, the acta,
-- the financial statements and the attendance list hang on each meeting as attachments (owner_kind apafa_meeting).
-- The API only opens this schema to institutions of public management
CREATE SCHEMA IF NOT EXISTS apafa;

CREATE TABLE apafa.meetings (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    title varchar(160) NOT NULL,
    -- ordinary | extraordinary | assembly (asamblea general) | board (consejo directivo)
    kind varchar(20) NOT NULL,
    scheduled_at timestamptz NOT NULL,
    -- in_person | virtual | hybrid: place, link or both
    modality varchar(20) NOT NULL DEFAULT 'in_person',
    place varchar(200) NOT NULL DEFAULT '',
    meeting_url varchar(500) NOT NULL DEFAULT '',
    -- Orden del día, one point per line
    agenda varchar(4000) NOT NULL DEFAULT '',
    -- Who is convened, and so who reads the notices: guardians (every family) | community (the whole institution)
    audience varchar(20) NOT NULL DEFAULT 'guardians',
    -- scheduled | rescheduled (moved, still to be held) | held | cancelled
    status varchar(20) NOT NULL DEFAULT 'scheduled',
    -- Rescheduling keeps the date it had before and why it moved
    previous_at timestamptz,
    reschedule_reason varchar(300) NOT NULL DEFAULT '',
    cancel_reason varchar(300) NOT NULL DEFAULT '',
    -- After it is held: summary of the acta, the agreements and how many attended
    minutes varchar(6000) NOT NULL DEFAULT '',
    agreements varchar(4000) NOT NULL DEFAULT '',
    attendees integer,
    minutes_published_at timestamptz,
    -- Last notice sent to the families (convocatoria, change, cancellation or acta) and when it was first convened;
    -- a meeting never convened is a draft families do not see
    announcement_id uuid REFERENCES comms.announcements (id) ON DELETE SET NULL,
    notified_at timestamptz,
    created_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT meetings_kind_check CHECK (kind IN ('ordinary', 'extraordinary', 'assembly', 'board')),
    CONSTRAINT meetings_modality_check CHECK (modality IN ('in_person', 'virtual', 'hybrid')),
    CONSTRAINT meetings_audience_check CHECK (audience IN ('guardians', 'community')),
    CONSTRAINT meetings_status_check CHECK (status IN ('scheduled', 'rescheduled', 'held', 'cancelled')),
    CONSTRAINT meetings_title_required CHECK (btrim(title) <> ''),
    CONSTRAINT meetings_attendees_check CHECK (attendees IS NULL OR attendees BETWEEN 0 AND 100000)
);

CREATE INDEX idx_apafa_meetings_institution ON apafa.meetings (institution_id, scheduled_at DESC);

-- The board (consejo directivo) of the association: who holds each office and for which period
CREATE TABLE apafa.board_members (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    full_name varchar(160) NOT NULL,
    -- president | vice_president | secretary | treasurer | spokesperson (vocal)
    position varchar(20) NOT NULL,
    phone varchar(20) NOT NULL DEFAULT '',
    starts_on date NOT NULL,
    ends_on date NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT board_members_position_check CHECK (position IN ('president', 'vice_president', 'secretary', 'treasurer', 'spokesperson')),
    CONSTRAINT board_members_name_required CHECK (btrim(full_name) <> ''),
    CONSTRAINT board_members_period_check CHECK (ends_on >= starts_on)
);

CREATE INDEX idx_apafa_board_institution ON apafa.board_members (institution_id, ends_on DESC);
