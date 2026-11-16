-- Virtual classroom (docs/ceba/PLAN.md, phase E2; Anexo 2 of RSG N.° 613-2014-MINEDU, sección de aula virtual):
-- one room per subject (área) × grade × period, its material dosed in units by promotional period, a reusable
-- library, what each student opened, and the printed packages of the itinerant distance form. Files hang on items
-- and library entries as attachments (owner_kind classroom_item / library_item)
CREATE SCHEMA IF NOT EXISTS classroom;

CREATE TABLE classroom.rooms (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    subject_id uuid NOT NULL REFERENCES academic.subjects (id) ON DELETE CASCADE,
    -- NULL only where the structure has no grades (CEPRE areas)
    grade_id uuid REFERENCES academic.grades (id) ON DELETE CASCADE,
    description varchar(2000) NOT NULL DEFAULT '',
    -- Teacher in charge; teachers assigned to the subject in a section of the grade can edit it too
    staff_id uuid REFERENCES people.staff (id) ON DELETE SET NULL,
    -- draft: students do not see it yet · published: open to the students of the grade
    status varchar(20) NOT NULL DEFAULT 'draft',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rooms_subject_grade_key UNIQUE NULLS NOT DISTINCT (period_id, subject_id, grade_id),
    CONSTRAINT rooms_status_check CHECK (status IN ('draft', 'published'))
);

CREATE TRIGGER rooms_touch_updated_at BEFORE UPDATE ON classroom.rooms
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_classroom_rooms_period ON classroom.rooms (institution_id, period_id);
CREATE INDEX idx_classroom_rooms_staff ON classroom.rooms (staff_id) WHERE staff_id IS NOT NULL;

-- Units of a room (unidades / semanas), in order; a unit opens to students on opens_on (NULL: as soon as published)
CREATE TABLE classroom.units (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    room_id uuid NOT NULL REFERENCES classroom.rooms (id) ON DELETE CASCADE,
    position smallint NOT NULL,
    title varchar(160) NOT NULL,
    summary varchar(2000) NOT NULL DEFAULT '',
    opens_on date,
    published boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT units_title_required CHECK (btrim(title) <> ''),
    CONSTRAINT units_position_check CHECK (position >= 0)
);

CREATE TRIGGER units_touch_updated_at BEFORE UPDATE ON classroom.units
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_classroom_units_room ON classroom.units (room_id, position);

-- Reusable resources of the institution (biblioteca): readings, files, videos and links a teacher adds to any unit
CREATE TABLE classroom.library_items (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    -- reading | file | video | link
    kind varchar(20) NOT NULL,
    title varchar(160) NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    body text NOT NULL DEFAULT '',
    url varchar(500) NOT NULL DEFAULT '',
    -- Free label to find it (area or topic: "Matemática", "Proyecto de vida")
    topic varchar(80) NOT NULL DEFAULT '',
    created_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT library_items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link')),
    CONSTRAINT library_items_title_required CHECK (btrim(title) <> ''),
    CONSTRAINT library_items_body_length CHECK (char_length(body) <= 20000),
    CONSTRAINT library_items_url_check CHECK (url = '' OR url ~ '^https://[^\s<>"]+$')
);

CREATE TRIGGER library_items_touch_updated_at BEFORE UPDATE ON classroom.library_items
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_classroom_library_institution ON classroom.library_items (institution_id, lower(title));

-- Material of a unit, in order. reading: text written here · file: attachments · video / link: an https address ·
-- live: a synchronous session (meeting link, start, length, recording) · activity: instructions to work on.
-- complementary marks material complementario. An item taken from the library shows the library's text, link and
-- files (library_item_id), so editing the library updates every room that uses it
CREATE TABLE classroom.items (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    room_id uuid NOT NULL REFERENCES classroom.rooms (id) ON DELETE CASCADE,
    unit_id uuid NOT NULL REFERENCES classroom.units (id) ON DELETE CASCADE,
    position smallint NOT NULL,
    kind varchar(20) NOT NULL,
    title varchar(160) NOT NULL,
    body text NOT NULL DEFAULT '',
    url varchar(500) NOT NULL DEFAULT '',
    complementary boolean NOT NULL DEFAULT false,
    starts_at timestamptz,
    duration_minutes smallint,
    recording_url varchar(500) NOT NULL DEFAULT '',
    library_item_id uuid REFERENCES classroom.library_items (id) ON DELETE SET NULL,
    published boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link', 'live', 'activity')),
    CONSTRAINT items_title_required CHECK (btrim(title) <> ''),
    CONSTRAINT items_body_length CHECK (char_length(body) <= 20000),
    CONSTRAINT items_url_check CHECK (url = '' OR url ~ '^https://[^\s<>"]+$'),
    CONSTRAINT items_recording_url_check CHECK (recording_url = '' OR recording_url ~ '^https://[^\s<>"]+$'),
    CONSTRAINT items_live_check CHECK (
        (kind = 'live' AND starts_at IS NOT NULL AND duration_minutes BETWEEN 5 AND 600)
        OR (kind <> 'live' AND starts_at IS NULL AND duration_minutes IS NULL)
    ),
    CONSTRAINT items_position_check CHECK (position >= 0)
);

CREATE TRIGGER items_touch_updated_at BEFORE UPDATE ON classroom.items
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

CREATE INDEX idx_classroom_items_unit ON classroom.items (unit_id, position);
CREATE INDEX idx_classroom_items_room ON classroom.items (room_id);
CREATE INDEX idx_classroom_items_library ON classroom.items (library_item_id) WHERE library_item_id IS NOT NULL;
CREATE INDEX idx_classroom_items_live ON classroom.items (starts_at) WHERE kind = 'live';

-- What each student opened: first and last time and how often. Feeds participation and permanence (phase E5)
CREATE TABLE classroom.item_views (
    item_id uuid NOT NULL REFERENCES classroom.items (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    first_at timestamptz NOT NULL DEFAULT now(),
    last_at timestamptz NOT NULL DEFAULT now(),
    views integer NOT NULL DEFAULT 1,
    PRIMARY KEY (item_id, student_id),
    CONSTRAINT item_views_count_check CHECK (views >= 1)
);

CREATE INDEX idx_classroom_item_views_student ON classroom.item_views (student_id, last_at DESC);

-- Itinerant form: the printed package of a unit handed to a student and when it came back
CREATE TABLE classroom.deliveries (
    unit_id uuid NOT NULL REFERENCES classroom.units (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    delivered_on date NOT NULL,
    returned_on date,
    notes varchar(300) NOT NULL DEFAULT '',
    recorded_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (unit_id, student_id),
    CONSTRAINT deliveries_dates_check CHECK (returned_on IS NULL OR returned_on >= delivered_on)
);

CREATE TRIGGER deliveries_touch_updated_at BEFORE UPDATE ON classroom.deliveries
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Files of the items and of the library
ALTER TABLE attachments.files DROP CONSTRAINT files_owner_kind_check;
ALTER TABLE attachments.files ADD CONSTRAINT files_owner_kind_check CHECK (owner_kind IN (
    'enrollment', 'student', 'staff', 'tutoring', 'discipline', 'psychology', 'individual_plan', 'therapy',
    'progress_report', 'inclusion_support', 'period', 'closing', 'internship', 'degree',
    'attendance_justification', 'apafa_meeting', 'authorization', 'platform_sheet', 'classroom_item', 'library_item'
));
