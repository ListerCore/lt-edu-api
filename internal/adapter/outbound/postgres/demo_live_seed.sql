-- Live sessions of the CEBA demo (clases en vivo), run once by cmd/seed after the classroom (go:0022_demo_live): a
-- live class inside the platform starting within the hour and one on Google Meet tomorrow in two rooms of 3.° of the
-- Avanzado cycle, a group tutoring on the platform tomorrow for 3.° V and a psychology appointment on the platform for
-- Rosa Elena. Dates follow the day the seed runs. Fictitious data
WITH rooms AS (
    SELECT r.id AS room_id,
        (SELECT u.id FROM classroom.units u WHERE u.room_id = r.id AND u.published ORDER BY u.position LIMIT 1) AS unit_id,
        row_number() OVER (ORDER BY s.position) AS n
    FROM classroom.rooms r
    JOIN iam.institutions i ON i.id = r.institution_id AND i.slug = 'demo-eba'
    JOIN academic.periods p ON p.id = r.period_id AND p.status = 'active'
    JOIN academic.grades g ON g.id = r.grade_id AND g.short_name = '3.°'
    JOIN academic.subjects s ON s.id = r.subject_id
    WHERE r.status = 'published' AND EXISTS (SELECT 1 FROM classroom.units u WHERE u.room_id = r.id AND u.published)
)
INSERT INTO classroom.items (room_id, unit_id, position, kind, title, body, url, starts_at, duration_minutes, published, provider)
SELECT rooms.room_id, rooms.unit_id, coalesce((SELECT max(position) + 1 FROM classroom.items WHERE unit_id = rooms.unit_id), 0), 'live', x.title, x.body, x.url,
    x.starts_at, x.minutes, true, x.provider
FROM rooms
JOIN (VALUES
    (1::bigint, 'Clase en vivo: repaso de la unidad', 'Nos vemos aquí mismo, en la plataforma: entra desde el celular o la computadora.', '',
        date_trunc('hour', now()) + interval '1 hour', 60, 'platform'),
    (2::bigint, 'Clase en vivo por Google Meet', 'Resolvemos dudas de la unidad en Google Meet.', 'https://meet.google.com/demo-clase-3a',
        date_trunc('day', now() AT TIME ZONE 'America/Lima' + interval '1 day') AT TIME ZONE 'America/Lima' + interval '19 hours', 45, 'meet')
) AS x(n, title, body, url, starts_at, minutes, provider) ON x.n = rooms.n
WHERE rooms.unit_id IS NOT NULL;

-- Group tutoring of 3.° V tomorrow, on the platform
WITH tutor AS (SELECT st.id, st.institution_id FROM people.staff st JOIN iam.institutions i ON i.id = st.institution_id AND i.slug = 'demo-eba' WHERE st.code = 'P-0117'),
session AS (
    INSERT INTO wellbeing.tutoring_sessions (institution_id, staff_id, kind, mode, scheduled_at, duration_min, link, reason, created_by_name, provider)
    SELECT tutor.institution_id, tutor.id, 'group', 'virtual',
        date_trunc('day', now() AT TIME ZONE 'America/Lima' + interval '1 day') AT TIME ZONE 'America/Lima' + interval '19 hours 30 minutes',
        40, '', 'Repasar cómo usar el aula y las clases en vivo desde el celular', 'Elena Huamán Rojas', 'platform'
    FROM tutor RETURNING id
)
INSERT INTO wellbeing.tutoring_attendees (session_id, student_id)
SELECT session.id, s.id FROM session, people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
WHERE s.code BETWEEN '2026-0901' AND '2026-0905';

-- Rosa Elena asked to talk with psychology: a case (when she has none) and an appointment on the platform
INSERT INTO wellbeing.psych_cases (institution_id, student_id, origin, referred_name, reason, priority, status, opened_on, psychologist_name)
SELECT s.institution_id, s.id, 'self', '', 'Pide orientación para manejar el estrés entre el trabajo y los estudios.', 'normal', 'open',
    (now() AT TIME ZONE 'America/Lima')::date, coalesce((SELECT st.given_names || ' ' || st.surnames FROM people.staff st
        WHERE st.institution_id = s.institution_id AND st.position ILIKE 'Psic%' ORDER BY st.code LIMIT 1), '')
FROM people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
WHERE s.code = '2026-0901' AND NOT EXISTS (SELECT 1 FROM wellbeing.psych_cases c WHERE c.student_id = s.id AND c.status <> 'closed');

INSERT INTO wellbeing.psych_appointments (institution_id, case_id, student_id, scheduled_at, duration_min, mode, provider, created_by_name)
SELECT c.institution_id, c.id, c.student_id,
    date_trunc('day', now() AT TIME ZONE 'America/Lima' + interval '2 days') AT TIME ZONE 'America/Lima' + interval '16 hours',
    45, 'virtual', 'platform', c.psychologist_name
FROM wellbeing.psych_cases c JOIN people.students s ON s.id = c.student_id JOIN iam.institutions i ON i.id = c.institution_id AND i.slug = 'demo-eba'
WHERE s.code = '2026-0901' AND c.status <> 'closed'
ORDER BY c.opened_on DESC LIMIT 1;
