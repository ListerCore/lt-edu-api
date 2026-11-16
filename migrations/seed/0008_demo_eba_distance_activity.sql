-- Distance students of the CEBA demo (docs/ceba/PLAN.md, phase E5): ten students in the virtual sections of 3.° and
-- 4.° of the advanced cycle, with their activity in the classroom since the start of the period. Each has a pattern:
-- regular (two to four days a week), fading (stopped connecting three weeks ago), irregular (one week in three) and
-- one who never connected, so the permanence report and the dropout alerts have something to show. Fictitious data
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
period AS (SELECT p.id, p.starts_on FROM academic.periods p JOIN inst ON inst.id = p.institution_id WHERE p.status = 'active' ORDER BY p.starts_on DESC LIMIT 1),
sections AS (
    SELECT s.id, s.level_id, s.grade_id, g.position FROM academic.sections s JOIN period ON period.id = s.period_id
    JOIN academic.levels l ON l.id = s.level_id AND l.code = 'avanzado' JOIN academic.grades g ON g.id = s.grade_id
    WHERE s.attention_mode = 'distance' AND s.distance_variant = 'virtual' AND g.position IN (2, 3)
),
people AS (
    SELECT * FROM (VALUES
        (1, 'Quispe',    'Huamán',   'Rosa Elena',     'f', '1992-04-11', 2, 'regular'),
        (2, 'Mendoza',   'Ccori',    'Julio César',    'm', '1988-09-02', 2, 'regular'),
        (3, 'Tineo',     'Pariona',  'Marleni',        'f', '1995-01-23', 2, 'fading'),
        (4, 'Huallpa',   'Sulca',    'Edwin',          'm', '1990-07-30', 2, 'irregular'),
        (5, 'Cárdenas',  'Vilca',    'Yesenia',        'f', '2001-12-05', 2, 'regular'),
        (6, 'Palomino',  'Quispe',   'Hernán',         'm', '1985-03-17', 3, 'regular'),
        (7, 'Gamboa',    'Ayala',    'Lucía',          'f', '1998-06-14', 3, 'fading'),
        (8, 'Ochoa',     'Lapa',     'Wilber',         'm', '1993-10-09', 3, 'regular'),
        (9, 'Rojas',     'Infanzón', 'Delia',          'f', '1987-02-28', 3, 'never'),
        (10, 'Bendezú',  'Cisneros', 'Fredy Alonso',   'm', '1996-11-21', 3, 'irregular')
    ) AS x (n, paternal, maternal, given, sex, birth, grade_position, pattern)
),
students AS (
    INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, maternal_surname, birth_date, sex, email, phone)
    SELECT inst.id, '2026-09' || lpad(n::text, 2, '0'), 'dni', (72000000 + n)::text, given, paternal, maternal, birth::date, sex,
        lower(translate(split_part(given, ' ', 1) || '.' || paternal, 'áéíóúñÁÉÍÓÚ', 'aeiounAEIOU')) || n || '@estudiantes.test', '9' || (61000000 + n * 731)::text
    FROM people CROSS JOIN inst
    RETURNING id, code
)
INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on, notes)
SELECT inst.id, st.id, period.id, sec.level_id, sec.grade_id, sec.id, 'new', period.starts_on, 'Forma a distancia (virtual): ' || p.pattern
FROM people p
JOIN students st ON st.code = '2026-09' || lpad(p.n::text, 2, '0')
JOIN sections sec ON sec.position = p.grade_position
CROSS JOIN inst CROSS JOIN period;

-- Their ficha (all connect by phone) and SIAGIE state
INSERT INTO people.enrollment_files (student_id, mother_tongue, occupation, internet, device, preferred_mode, last_studies, documents, updated_by_name)
SELECT s.id, CASE WHEN s.paternal_surname IN ('Quispe', 'Huallpa', 'Tineo') THEN 'quechua' ELSE 'castellano' END, 'Trabaja en el día',
    'mobile', 'phone', 'distance', '2.° de secundaria, EBR', ARRAY['dni', 'study_certificate'], 'Secretaría'
FROM people.students s JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba' WHERE s.code LIKE '2026-09__'
ON CONFLICT (student_id) DO NOTHING;

INSERT INTO enrollment.siagie_records (enrollment_id, status, exported_at, updated_by_name)
SELECT e.id, 'exported', e.enrolled_on + 12, 'Secretaría'
FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
WHERE s.code LIKE '2026-09__'
ON CONFLICT (enrollment_id) DO NOTHING;

-- Their activity, day by day since the start of the period: in the rooms of their grade, opening materials, and
-- handing in or writing in a forum now and then. Deterministic: a hash of the student and the day decides
SELECT attendance.record_activity(x.student_id, x.room_id, x.item_id, x.kind, x.at)
FROM (
    SELECT s.id AS student_id, r.id AS room_id, it.id AS item_id,
        CASE WHEN abs(hashtext(s.code || d::text)) % 9 = 0 THEN 'post' WHEN abs(hashtext(s.code || d::text)) % 7 = 0 THEN 'live' ELSE 'view' END AS kind,
        (d + time '19:00' + (abs(hashtext(s.code || d::text)) % 120) * interval '1 minute') AT TIME ZONE 'America/Lima' AS at
    FROM people.students s
    JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
    JOIN enrollment.enrollments e ON e.student_id = s.id AND e.status = 'active'
    CROSS JOIN LATERAL generate_series(e.enrolled_on, (now() AT TIME ZONE 'America/Lima')::date - 1, interval '1 day') AS g(d0)
    CROSS JOIN LATERAL (SELECT g.d0::date AS d) dd
    JOIN LATERAL (
        SELECT r.id FROM classroom.rooms r JOIN academic.subjects sub ON sub.id = r.subject_id JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
        WHERE r.period_id = e.period_id AND r.grade_id = e.grade_id ORDER BY abs(hashtext(r.id::text || d::text)) LIMIT 1
    ) r ON true
    LEFT JOIN LATERAL (SELECT it.id FROM classroom.items it WHERE it.room_id = r.id ORDER BY abs(hashtext(it.id::text || d::text)) LIMIT 1) it ON true
    WHERE s.code LIKE '2026-09__'
      AND CASE split_part(e.notes, ': ', 2)
          WHEN 'regular'   THEN abs(hashtext(s.code || d::text)) % 7 < 3
          WHEN 'fading'    THEN d < (now() AT TIME ZONE 'America/Lima')::date - 21 AND abs(hashtext(s.code || d::text)) % 7 < 2
          WHEN 'irregular' THEN ((d - e.enrolled_on) / 7) % 3 = 0 AND abs(hashtext(s.code || d::text)) % 7 < 2
          ELSE false
      END
) x
ORDER BY x.at;

-- The tutor's adjustment of one week of an irregular student, who sent the work on paper
INSERT INTO attendance.permanence_adjustments (enrollment_id, window_start, present, reason, by_name)
SELECT e.id, e.enrolled_on + 7, true, 'Entregó la actividad de la semana impresa: no tenía saldo de datos.', 'Tutoría'
FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id JOIN iam.institutions i ON i.id = s.institution_id AND i.slug = 'demo-eba'
WHERE s.code = '2026-0904'
ON CONFLICT DO NOTHING;
