-- Participation and permanence (docs/ceba/PLAN.md, phase E5). Every action of a student in the platform becomes an
-- activity event, written by triggers on the tables that hold the action (nobody records it by hand): a sign-in, a
-- material opened, a live session joined, a quiz started, a task handed in, a forum post, a tutoring interview.
-- A daily summary per student and room feeds the reports; permanence in the distance form is computed from it with
-- the institution's rule, and the tutor may adjust a week with a reason

CREATE TABLE attendance.activity_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    -- Classroom room of the action (NULL for sign-ins and tutoring)
    room_id uuid REFERENCES classroom.rooms (id) ON DELETE SET NULL,
    -- The item acted on; no foreign key so the event outlives the item
    item_id uuid,
    -- login | view | live | attempt | submission | post | tutoring
    kind varchar(20) NOT NULL,
    at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT activity_events_kind_check CHECK (kind IN ('login', 'view', 'live', 'attempt', 'submission', 'post', 'tutoring'))
);

CREATE INDEX idx_attendance_activity_events_student ON attendance.activity_events (student_id, at DESC);
CREATE INDEX idx_attendance_activity_events_room ON attendance.activity_events (room_id, at DESC) WHERE room_id IS NOT NULL;

CREATE FUNCTION attendance.activity_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'activity events are append-only' USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER activity_events_append_only BEFORE UPDATE ON attendance.activity_events
    FOR EACH ROW EXECUTE FUNCTION attendance.activity_append_only();

-- One row per student, day (Lima) and room; room_key is the nil uuid for actions outside a room
CREATE TABLE attendance.activity_days (
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    day date NOT NULL,
    room_key uuid NOT NULL,
    logins integer NOT NULL DEFAULT 0,
    views integer NOT NULL DEFAULT 0,
    live integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    submissions integer NOT NULL DEFAULT 0,
    posts integer NOT NULL DEFAULT 0,
    tutoring integer NOT NULL DEFAULT 0,
    last_at timestamptz NOT NULL,
    PRIMARY KEY (student_id, day, room_key)
);

CREATE INDEX idx_attendance_activity_days_room ON attendance.activity_days (room_key, day);

-- record_activity writes the event and adds it to the day's summary
CREATE FUNCTION attendance.record_activity(p_student uuid, p_room uuid, p_item uuid, p_kind varchar, p_at timestamptz) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v_institution uuid;
BEGIN
    SELECT institution_id INTO v_institution FROM people.students WHERE id = p_student;
    IF v_institution IS NULL THEN
        RETURN;
    END IF;
    INSERT INTO attendance.activity_events (institution_id, student_id, room_id, item_id, kind, at)
    VALUES (v_institution, p_student, p_room, p_item, p_kind, p_at);
    INSERT INTO attendance.activity_days AS d (student_id, day, room_key, logins, views, live, attempts, submissions, posts, tutoring, last_at)
    VALUES (p_student, (p_at AT TIME ZONE 'America/Lima')::date, coalesce(p_room, '00000000-0000-0000-0000-000000000000'::uuid),
        (p_kind = 'login')::int, (p_kind = 'view')::int, (p_kind = 'live')::int, (p_kind = 'attempt')::int,
        (p_kind = 'submission')::int, (p_kind = 'post')::int, (p_kind = 'tutoring')::int, p_at)
    ON CONFLICT (student_id, day, room_key) DO UPDATE SET
        logins = d.logins + EXCLUDED.logins, views = d.views + EXCLUDED.views, live = d.live + EXCLUDED.live,
        attempts = d.attempts + EXCLUDED.attempts, submissions = d.submissions + EXCLUDED.submissions,
        posts = d.posts + EXCLUDED.posts, tutoring = d.tutoring + EXCLUDED.tutoring, last_at = greatest(d.last_at, EXCLUDED.last_at);
END;
$$;

-- Opening an item (each new view): a live session counts as attending it
CREATE FUNCTION attendance.on_item_view() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_room uuid;
    v_kind varchar;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.views <= OLD.views THEN
        RETURN NEW;
    END IF;
    SELECT i.room_id, CASE WHEN i.kind = 'live' THEN 'live' ELSE 'view' END INTO v_room, v_kind FROM classroom.items i WHERE i.id = NEW.item_id;
    PERFORM attendance.record_activity(NEW.student_id, v_room, NEW.item_id, v_kind, NEW.last_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER item_views_activity AFTER INSERT OR UPDATE ON classroom.item_views
    FOR EACH ROW EXECUTE FUNCTION attendance.on_item_view();

-- Starting an attempt, and handing it in
CREATE FUNCTION attendance.on_submission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_room uuid;
BEGIN
    SELECT room_id INTO v_room FROM classroom.items WHERE id = NEW.item_id;
    IF TG_OP = 'INSERT' THEN
        PERFORM attendance.record_activity(NEW.student_id, v_room, NEW.item_id, 'attempt', NEW.started_at);
    ELSIF NEW.submitted_at IS NOT NULL AND OLD.submitted_at IS NULL THEN
        PERFORM attendance.record_activity(NEW.student_id, v_room, NEW.item_id, 'submission', NEW.submitted_at);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER submissions_activity AFTER INSERT OR UPDATE OF submitted_at ON classroom.submissions
    FOR EACH ROW EXECUTE FUNCTION attendance.on_submission();

-- A forum post written by a student
CREATE FUNCTION attendance.on_post() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_room uuid;
BEGIN
    IF NEW.author_student_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT room_id INTO v_room FROM classroom.items WHERE id = NEW.item_id;
    PERFORM attendance.record_activity(NEW.author_student_id, v_room, NEW.item_id, 'post', NEW.created_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER posts_activity AFTER INSERT ON classroom.posts
    FOR EACH ROW EXECUTE FUNCTION attendance.on_post();

-- A sign-in with the student role: the student records of that account in the role's institution
CREATE FUNCTION attendance.on_session() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_student uuid;
BEGIN
    FOR v_student IN
        SELECT s.id FROM iam.user_roles ur
        JOIN iam.roles r ON r.id = ur.role_id AND r.code = 'student'
        JOIN iam.users u ON u.id = ur.user_id
        JOIN people.students s ON s.institution_id = ur.institution_id AND lower(s.email) = lower(u.email)
        WHERE ur.id = NEW.active_user_role_id
    LOOP
        PERFORM attendance.record_activity(v_student, NULL, NULL, 'login', NEW.created_at);
    END LOOP;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sessions_activity AFTER INSERT ON iam.sessions
    FOR EACH ROW WHEN (NEW.active_user_role_id IS NOT NULL) EXECUTE FUNCTION attendance.on_session();

-- A tutoring interview with the student
CREATE FUNCTION attendance.on_interview() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'student' THEN
        PERFORM attendance.record_activity(NEW.student_id, NULL, NULL, 'tutoring', (NEW.held_on + time '12:00') AT TIME ZONE 'America/Lima');
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER interviews_activity AFTER INSERT ON wellbeing.interviews
    FOR EACH ROW EXECUTE FUNCTION attendance.on_interview();

-- The rule of permanence of each institution: a week (window_days) counts when the student was active on at least
-- min_active_days of its days. inactive_days without activity raises a dropout alert, and a permanence below
-- low_percent another
CREATE TABLE attendance.permanence_rules (
    institution_id uuid PRIMARY KEY REFERENCES iam.institutions (id) ON DELETE CASCADE,
    window_days smallint NOT NULL DEFAULT 7,
    min_active_days smallint NOT NULL DEFAULT 1,
    inactive_days smallint NOT NULL DEFAULT 14,
    low_percent smallint NOT NULL DEFAULT 60,
    updated_by_name varchar(160) NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT permanence_rules_window_check CHECK (window_days BETWEEN 1 AND 31),
    CONSTRAINT permanence_rules_min_check CHECK (min_active_days BETWEEN 1 AND window_days),
    CONSTRAINT permanence_rules_inactive_check CHECK (inactive_days BETWEEN 3 AND 90),
    CONSTRAINT permanence_rules_low_check CHECK (low_percent BETWEEN 1 AND 100)
);

CREATE TRIGGER permanence_rules_touch_updated_at BEFORE UPDATE ON attendance.permanence_rules
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- The tutor's adjustment of one window of an enrollment (it was present without leaving a trace, or the activity
-- does not count), with the reason; it goes to the audit log
CREATE TABLE attendance.permanence_adjustments (
    enrollment_id uuid NOT NULL REFERENCES enrollment.enrollments (id) ON DELETE CASCADE,
    window_start date NOT NULL,
    present boolean NOT NULL,
    reason varchar(300) NOT NULL,
    by_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (enrollment_id, window_start),
    CONSTRAINT permanence_adjustments_reason_check CHECK (btrim(reason) <> '')
);

-- What already happened before this migration becomes events too, so the reports start complete
SELECT attendance.record_activity(v.student_id, i.room_id, v.item_id, CASE WHEN i.kind = 'live' THEN 'live' ELSE 'view' END, v.last_at)
FROM classroom.item_views v JOIN classroom.items i ON i.id = v.item_id;
SELECT attendance.record_activity(s.student_id, i.room_id, s.item_id, 'attempt', s.started_at)
FROM classroom.submissions s JOIN classroom.items i ON i.id = s.item_id;
SELECT attendance.record_activity(s.student_id, i.room_id, s.item_id, 'submission', s.submitted_at)
FROM classroom.submissions s JOIN classroom.items i ON i.id = s.item_id WHERE s.submitted_at IS NOT NULL;
SELECT attendance.record_activity(p.author_student_id, i.room_id, p.item_id, 'post', p.created_at)
FROM classroom.posts p JOIN classroom.items i ON i.id = p.item_id WHERE p.author_student_id IS NOT NULL;
