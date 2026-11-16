-- Virtual tutoring of the CEBA demo (docs/ceba/PLAN.md, phase E6): a virtual tutor for the distance sections of
-- the Avanzado cycle (3.° V and 4.° V), one student with a tutor of their own, a group session held two weeks ago,
-- an individual one ahead for the student who stopped connecting, conversations and a referral to the SAANEE.
-- Dates follow today, so the demo stays current. Fictitious data
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba')
INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, email, phone, position, is_teacher, specialty, hired_on)
SELECT inst.id, 'P-0117', 'dni', '41870117', 'Elena', 'Huamán Rojas', 'elena.huaman17@personal.test', '966117117', 'Tutora virtual', true, 'Tutoría y orientación educativa', '2026-03-02'
FROM inst
ON CONFLICT DO NOTHING;

-- The virtual tutor leads the distance sections of 3.° and 4.° of the Avanzado cycle
UPDATE academic.sections sec SET tutor_id = st.id
FROM academic.periods p, iam.institutions i, people.staff st, academic.grades g
WHERE p.id = sec.period_id AND i.id = p.institution_id AND i.slug = 'demo-eba' AND p.status = 'active'
  AND sec.attention_mode = 'distance' AND sec.distance_variant = 'virtual' AND g.id = sec.grade_id AND g.short_name IN ('3.°', '4.°')
  AND st.institution_id = i.id AND st.code = 'P-0117';

-- Fredy Alonso comes to the CEBA some evenings: the tutor of 4.° A follows him
INSERT INTO wellbeing.tutor_assignments (period_id, student_id, institution_id, staff_id, assigned_by_name)
SELECT e.period_id, s.id, s.institution_id, st.id, 'Walter Quispe Ccahuana'
FROM people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
JOIN enrollment.enrollments e ON e.student_id = s.id AND e.status = 'active'
JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
JOIN people.staff st ON st.institution_id = i.id AND st.code = 'P-0111'
WHERE s.code = '2026-0910'
ON CONFLICT DO NOTHING;

-- A group session of 3.° V two weeks ago: three of the five came, each with an interview in their file
WITH tutor AS (SELECT st.id, st.institution_id FROM people.staff st JOIN iam.institutions i ON i.id = st.institution_id AND i.slug = 'demo-eba' WHERE st.code = 'P-0117'),
session AS (
    INSERT INTO wellbeing.tutoring_sessions (institution_id, staff_id, kind, mode, scheduled_at, duration_min, link, reason, status, notes, agreements, held_at, created_by_name)
    SELECT tutor.institution_id, tutor.id, 'group', 'virtual', date_trunc('day', now() AT TIME ZONE 'America/Lima' - interval '14 days') + interval '19 hours',
        45, 'https://meet.google.com/demo-tuto-3v', 'Organizar el tiempo de estudio con el trabajo', 'held',
        'Varios trabajan en la tarde. Se revisó cómo repartir las actividades de la semana y cómo usar el aula desde el celular.',
        'Conectarse al menos dos noches por semana y avisar por mensaje si no pueden entregar a tiempo.',
        now() - interval '14 days' + interval '1 hour', 'Elena Huamán Rojas'
    FROM tutor RETURNING id, institution_id, scheduled_at, reason, notes, agreements
),
called AS (
    SELECT s.id AS student_id, s.code IN ('2026-0901', '2026-0902', '2026-0905') AS came
    FROM people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba' WHERE s.code BETWEEN '2026-0901' AND '2026-0905'
),
interviews AS (
    INSERT INTO wellbeing.interviews (institution_id, student_id, kind, held_on, topic, notes, agreements, author_name)
    SELECT session.institution_id, called.student_id, 'student', (session.scheduled_at AT TIME ZONE 'America/Lima')::date, session.reason, session.notes, session.agreements, 'Elena Huamán Rojas'
    FROM session, called WHERE called.came RETURNING id, student_id
)
INSERT INTO wellbeing.tutoring_attendees (session_id, student_id, attended, interview_id)
SELECT session.id, called.student_id, called.came, interviews.id FROM session, called LEFT JOIN interviews ON interviews.student_id = called.student_id;

-- Marleni stopped connecting: an individual call ahead, answering the dropout alert
WITH tutor AS (SELECT st.id, st.institution_id FROM people.staff st JOIN iam.institutions i ON i.id = st.institution_id AND i.slug = 'demo-eba' WHERE st.code = 'P-0117'),
session AS (
    INSERT INTO wellbeing.tutoring_sessions (institution_id, staff_id, kind, mode, scheduled_at, duration_min, link, reason, alert_kind, created_by_name)
    SELECT tutor.institution_id, tutor.id, 'individual', 'phone', date_trunc('day', now() AT TIME ZONE 'America/Lima' + interval '2 days') + interval '18 hours 30 minutes',
        20, '', 'Saber por qué dejó de conectarse y acordar cómo retomar', 'inactive', 'Elena Huamán Rojas'
    FROM tutor RETURNING id
)
INSERT INTO wellbeing.tutoring_attendees (session_id, student_id)
SELECT session.id, s.id FROM session, people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba' WHERE s.code = '2026-0903';

-- Conversations: Rosa Elena wrote last (unread by the tutor); Edwin has not answered
WITH pair AS (
    SELECT s.id AS student_id, s.code, st.id AS staff_id, s.institution_id FROM people.students s
    JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba' JOIN people.staff st ON st.institution_id = i.id AND st.code = 'P-0117'
    WHERE s.code IN ('2026-0901', '2026-0904')
)
INSERT INTO wellbeing.threads (institution_id, student_id, staff_id, last_message_at, staff_read_at, student_read_at)
SELECT institution_id, student_id, staff_id, now() - interval '3 hours', now() - interval '1 day', now() - interval '3 hours' FROM pair
ON CONFLICT DO NOTHING;

INSERT INTO wellbeing.messages (thread_id, side, author_name, body, created_at)
SELECT t.id, m.side, CASE WHEN m.side = 'staff' THEN 'Elena Huamán Rojas' ELSE s.given_names || ' ' || s.paternal_surname END, m.body, now() - m.ago
FROM wellbeing.threads t JOIN people.students s ON s.id = t.student_id
JOIN (VALUES
    ('2026-0901', 'staff', 'Hola Rosa Elena, vi que entregaste la tarea de Matemática. ¡Muy bien! ¿Pudiste ver el video de la unidad 3?', interval '2 days'),
    ('2026-0901', 'student', 'Sí profesora, lo vi ayer en la noche. No entendí bien la parte de porcentajes.', interval '1 day 20 hours'),
    ('2026-0901', 'staff', 'Te dejo un material más corto en la biblioteca. Si quieres lo vemos el jueves en la tutoría.', interval '1 day 2 hours'),
    ('2026-0901', 'student', 'Gracias, el jueves me conecto después del trabajo.', interval '3 hours'),
    ('2026-0904', 'staff', 'Hola Edwin, hace varios días que no entras al aula. ¿Todo bien? Cuéntame si necesitas ayuda para ponerte al día.', interval '4 days')
) AS m(code, side, body, ago) ON m.code = s.code
JOIN iam.institutions i ON i.id = t.institution_id AND i.slug = 'demo-eba';

UPDATE wellbeing.threads t SET last_message_at = x.last FROM (SELECT thread_id, max(created_at) AS last FROM wellbeing.messages GROUP BY thread_id) x
WHERE x.thread_id = t.id AND t.institution_id = (SELECT id FROM iam.institutions WHERE slug = 'demo-eba');

-- Lucía has trouble reading the screen: referred to the SAANEE, which received it
INSERT INTO wellbeing.saanee_referrals (institution_id, student_id, reason, status, sent_on, reference, referred_name, updated_name)
SELECT s.institution_id, s.id, 'Le cuesta leer textos largos en pantalla y pide que se le lean las consignas. Conviene evaluar si necesita apoyos de lectura o adaptaciones del material.',
    'received', (now() AT TIME ZONE 'America/Lima')::date - 6, 'Oficio N.° 041-2026-CEBA', 'Elena Huamán Rojas', 'Walter Quispe Ccahuana'
FROM people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba' WHERE s.code = '2026-0907';
