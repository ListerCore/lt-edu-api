-- Institution file of the CEBA demo (docs/ceba/PLAN.md, phase E0): its authorizing resolutions (creation and the
-- forms of attention of each cycle), the technical sheet of its e-learning platform, and the virtual and itinerant
-- distance sections of the advanced cycle in the running promotional period. The resolutions are fictitious
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
levels AS (SELECT l.id, l.code FROM academic.levels l JOIN inst ON inst.id = l.institution_id)
INSERT INTO academic.authorizations (institution_id, kind, level_id, attention_mode, resolution, issued_by, issued_on, file_number, notes)
SELECT inst.id, v.kind, levels.id, v.mode, v.resolution, v.issued_by, v.issued_on::date, v.file_number, v.notes
FROM inst
CROSS JOIN (VALUES
    ('creation',       NULL,         NULL,        'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', 'Creación y funcionamiento del CEBA, programa PEBAJA.'),
    ('attention_mode', 'inicial',    'in_person', 'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', ''),
    ('attention_mode', 'intermedio', 'in_person', 'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', ''),
    ('attention_mode', 'intermedio', 'blended',   'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', ''),
    ('attention_mode', 'avanzado',   'in_person', 'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', ''),
    ('attention_mode', 'avanzado',   'blended',   'RD N.° 00451-2019-DREA', 'Dirección Regional de Educación de Ayacucho', '2019-02-18', '2019-0004512', ''),
    ('attention_mode', 'avanzado',   'distance',  'RD N.° 01230-2026-DREA', 'Dirección Regional de Educación de Ayacucho', '2026-02-27', '2026-0013636', 'Forma de atención a distancia del ciclo avanzado (RSG N.° 613-2014-MINEDU).')
) AS v (kind, level_code, mode, resolution, issued_by, issued_on, file_number, notes)
LEFT JOIN levels ON levels.code = v.level_code
WHERE NOT EXISTS (SELECT 1 FROM academic.authorizations a WHERE a.institution_id = inst.id);

INSERT INTO academic.platform_sheets (institution_id, platform_name, url, technology, administrator, access_profiles, hosting_kind, provider,
    instance, contract_reference, service_status, valid_until, technical_lead, technical_lead_specialty, updated_by_name)
SELECT id, 'ListerCore Edu · Aula virtual', 'https://demo-eba.listercore.edu.pe', 'Aplicación web (Next.js y Go) con base de datos PostgreSQL',
    'Lister Core', 'Director, coordinador académico, secretaría, docente, tutor virtual, responsable técnico y estudiante', 'hosting',
    'Lister Core (servidor en la nube)', 'listercore-edu-prod-01', 'Contrato de servicio SaaS N.° 2026-014', 'Activo', '2027-03-31',
    'Responsable técnico de la demo', 'Ingeniería de Sistemas', 'Dirección'
FROM iam.institutions WHERE slug = 'demo-eba'
ON CONFLICT (institution_id) DO NOTHING;

-- Advanced cycle at a distance: grades 1.° to 4.° online, and an itinerant section for 3.° and 4.°
WITH targets AS (
    SELECT p.id AS period_id, l.id AS level_id, g.id AS grade_id, g.code AS grade_code
    FROM iam.institutions i
    JOIN academic.periods p ON p.institution_id = i.id AND p.name = 'Periodo promocional 2026'
    JOIN academic.levels l ON l.institution_id = i.id AND l.code = 'avanzado'
    JOIN academic.grades g ON g.level_id = l.id
    WHERE i.slug = 'demo-eba'
)
INSERT INTO academic.sections (period_id, level_id, grade_id, name, shift, attention_mode, program, distance_variant, capacity)
SELECT t.period_id, t.level_id, t.grade_id, v.name, 'evening', 'distance', 'pebaja', v.variant, v.capacity
FROM targets t
JOIN (VALUES ('V', 'virtual', 60, '{1,2,3,4}'::text[]), ('I', 'itinerant', 25, '{3,4}'::text[])) AS v (name, variant, capacity, grades)
    ON t.grade_code = ANY (v.grades)
ON CONFLICT DO NOTHING;
