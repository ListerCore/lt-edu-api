-- Forums of the virtual classroom (docs/ceba/PLAN.md, phase E4; "foros" of the Anexo 2): a forum is material of a
-- unit (item of kind forum) with threads and one level of replies, moderated by the room's teachers (pin, hide with a
-- reason, close). forum_reads keeps when each person last opened it, to mark what is new. Students' posts count as
-- participation (phase E5)
ALTER TABLE classroom.items DROP CONSTRAINT items_kind_check;
ALTER TABLE classroom.items ADD CONSTRAINT items_kind_check CHECK (kind IN ('reading', 'file', 'video', 'link', 'live', 'activity', 'quiz', 'task', 'forum'));

CREATE TABLE classroom.forums (
    item_id uuid PRIMARY KEY REFERENCES classroom.items (id) ON DELETE CASCADE,
    -- debate: a question to discuss · questions: doubts for the teacher · intro: students introduce themselves
    kind varchar(20) NOT NULL DEFAULT 'debate',
    closed boolean NOT NULL DEFAULT false,
    closed_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT forums_kind_check CHECK (kind IN ('debate', 'questions', 'intro'))
);

CREATE TRIGGER forums_touch_updated_at BEFORE UPDATE ON classroom.forums
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- A thread (parent_id NULL) or a reply to one. The author is an account; author_student_id is set when a student
-- wrote it (for participation). Hidden posts keep their text for staff, with who hid them and why
CREATE TABLE classroom.posts (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    item_id uuid NOT NULL REFERENCES classroom.forums (item_id) ON DELETE CASCADE,
    parent_id uuid REFERENCES classroom.posts (id) ON DELETE CASCADE,
    author_user_id uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    author_student_id uuid REFERENCES people.students (id) ON DELETE SET NULL,
    author_name varchar(200) NOT NULL,
    author_role varchar(40) NOT NULL DEFAULT '',
    body varchar(5000) NOT NULL,
    pinned boolean NOT NULL DEFAULT false,
    hidden boolean NOT NULL DEFAULT false,
    hidden_reason varchar(300) NOT NULL DEFAULT '',
    hidden_by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    edited_at timestamptz,
    CONSTRAINT posts_body_required CHECK (btrim(body) <> ''),
    CONSTRAINT posts_pin_threads_only CHECK (NOT pinned OR parent_id IS NULL),
    CONSTRAINT posts_hidden_reason_check CHECK (NOT hidden OR btrim(hidden_reason) <> '')
);

CREATE INDEX idx_classroom_posts_item ON classroom.posts (item_id, parent_id, created_at);
CREATE INDEX idx_classroom_posts_student ON classroom.posts (author_student_id, created_at DESC) WHERE author_student_id IS NOT NULL;

CREATE TABLE classroom.forum_reads (
    item_id uuid NOT NULL REFERENCES classroom.forums (item_id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    read_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, user_id)
);
