-- EBA enrollment of the CEBA demo (docs/ceba/PLAN.md, phase E1): the director signs the actas de ubicación, every
-- student has a ficha única (tongue, occupation and connectivity spread like a rural-urban CEBA of Huamanga) and
-- the running period's enrollments carry a SIAGIE state: the continuing ones exported, two observed, the rest
-- pending. Deterministic (by student code), fictitious data
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba')
INSERT INTO documents.signers (institution_id, kind, staff_id, position, sort)
SELECT inst.id, 'placement', st.id, 'Director del CEBA', 1
FROM inst JOIN people.staff st ON st.institution_id = inst.id AND st.position ILIKE 'Director%'
ORDER BY st.created_at
LIMIT 1
ON CONFLICT DO NOTHING;

WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
students AS (
    SELECT s.id, abs(hashtext(s.code)) AS h,
        (SELECT sec.attention_mode FROM enrollment.enrollments e JOIN academic.sections sec ON sec.id = e.section_id
         WHERE e.student_id = s.id ORDER BY e.enrolled_on DESC LIMIT 1) AS mode
    FROM people.students s JOIN inst ON inst.id = s.institution_id
)
INSERT INTO people.enrollment_files (student_id, mother_tongue, occupation, internet, device, preferred_mode, last_studies, documents, updated_by_name)
SELECT id,
    CASE WHEN h % 10 < 4 THEN 'quechua' ELSE 'castellano' END,
    (ARRAY['Comerciante', 'Agricultor', 'Ama de casa', 'Mototaxista', 'Construcción civil', 'Ayudante de cocina', 'Estudiante', 'Artesano'])[h % 8 + 1],
    (ARRAY['mobile', 'mobile', 'mobile', 'home', 'none', 'public'])[h % 6 + 1],
    (ARRAY['phone', 'phone', 'phone', 'computer', 'none'])[h % 5 + 1],
    mode,
    (ARRAY['5.° de primaria, EBR', '6.° de primaria, EBR', '2.° de secundaria, EBR', 'Primaria incompleta', '3.° de secundaria, EBR'])[h % 5 + 1],
    CASE WHEN h % 3 = 0 THEN ARRAY['dni'] ELSE ARRAY['dni', 'study_certificate'] END,
    'Secretaría'
FROM students
ON CONFLICT (student_id) DO NOTHING;

WITH running AS (
    SELECT p.id FROM academic.periods p JOIN iam.institutions i ON i.id = p.institution_id
    WHERE i.slug = 'demo-eba' AND p.status = 'active' ORDER BY p.starts_on DESC LIMIT 1
),
enrolled AS (
    SELECT e.id, e.kind, e.enrolled_on, row_number() OVER (ORDER BY s.code) AS n
    FROM enrollment.enrollments e JOIN running ON running.id = e.period_id JOIN people.students s ON s.id = e.student_id
    WHERE e.status = 'active'
)
INSERT INTO enrollment.siagie_records (enrollment_id, status, note, exported_at, updated_by_name)
SELECT id,
    CASE WHEN n IN (7, 31) THEN 'observed' WHEN kind = 'continuing' THEN 'exported' ELSE 'pending' END,
    CASE WHEN n IN (7, 31) THEN 'El DNI no coincide con RENIEC: verificar el número con el estudiante.' ELSE '' END,
    CASE WHEN kind = 'continuing' OR n IN (7, 31) THEN enrolled_on + 10 END,
    'Secretaría'
FROM enrolled
ON CONFLICT (enrollment_id) DO NOTHING;
