-- Academic structure of the five Joseph demo institutions: profile, levels and grades, periods with their terms and
-- the sections of each period, as a real institution of each kind would have them on 10 November 2026.
-- ON CONFLICT keeps it safe on a database that already has part of it

-- Profiles. The CEBA and the CEBE are public (no tuition: enrollment and the APAFA fee), the rest private
INSERT INTO academic.settings (institution_id, term_scheme, grading_scale, passing_grade, conduct_grade, attention_modes, shifts, management)
SELECT i.id, v.term_scheme, v.grading_scale, v.passing_grade, v.conduct_grade, v.attention_modes, v.shifts, v.management
FROM iam.institutions i
JOIN (VALUES
    ('demo-ebr',       'bimester',  'literal',   NULL::smallint, true,  '{}'::text[],                          '{morning,afternoon}'::text[],         'private'),
    ('demo-eba',       'bimester',  'literal',   NULL,           false, '{in_person,blended,distance}'::text[], '{afternoon,evening}'::text[],         'public'),
    ('demo-ebe',       'trimester', 'literal',   NULL,           false, '{}'::text[],                          '{morning}'::text[],                   'public'),
    ('demo-cepre',     'none',      'score',     NULL,           false, '{}'::text[],                          '{morning,afternoon,evening}'::text[], 'private'),
    ('demo-institute', 'none',      'vigesimal', 13,             false, '{}'::text[],                          '{morning,evening}'::text[],           'private')
) AS v (slug, term_scheme, grading_scale, passing_grade, conduct_grade, attention_modes, shifts, management) ON v.slug = i.slug
ON CONFLICT (institution_id) DO NOTHING;

-- Levels, EBA cycles, CEPRE areas and IEST study programs
INSERT INTO academic.levels (institution_id, code, name, description, position, formative_level, credits, attention_modes)
SELECT i.id, v.code, v.name, v.description, v.position, v.formative_level, v.credits, v.modes
FROM iam.institutions i
JOIN (VALUES
    ('demo-ebr', 'inicial', 'Inicial', 'Ciclo II: niños de 3 a 5 años.', 0, NULL, NULL::smallint, '{}'::text[]),
    ('demo-ebr', 'primaria', 'Primaria', 'Ciclos III, IV y V.', 1, NULL, NULL, '{}'),
    ('demo-ebr', 'secundaria', 'Secundaria', 'Ciclos VI y VII.', 2, NULL, NULL, '{}'),
    ('demo-eba', 'inicial', 'Ciclo Inicial', 'Alfabetización. Solo presencial.', 0, NULL, NULL, '{in_person}'),
    ('demo-eba', 'intermedio', 'Ciclo Intermedio', 'Equivale a la primaria. Presencial o semipresencial.', 1, NULL, NULL, '{in_person,blended}'),
    ('demo-eba', 'avanzado', 'Ciclo Avanzado', 'Equivale a la secundaria. Presencial, semipresencial o a distancia.', 2, NULL, NULL, '{in_person,blended,distance}'),
    ('demo-ebe', 'prite', 'PRITE', 'Intervención temprana de 0 a 3 años, con la familia.', 0, NULL, NULL, '{}'),
    ('demo-ebe', 'inicial', 'Inicial', 'De 3 a 5 años.', 1, NULL, NULL, '{}'),
    ('demo-ebe', 'primaria', 'Primaria', 'Hasta los 20 años en casos de discapacidad severa.', 2, NULL, NULL, '{}'),
    ('demo-cepre', 'uni', 'UNI · Ingenierías', 'Universidad Nacional de Ingeniería: matemática y física con mayor peso.', 0, NULL, NULL, '{}'),
    ('demo-cepre', 'unmsm-a', 'San Marcos · Área A', 'Ciencias de la Salud.', 1, NULL, NULL, '{}'),
    ('demo-cepre', 'unmsm-b', 'San Marcos · Área B', 'Ciencias Básicas.', 2, NULL, NULL, '{}'),
    ('demo-cepre', 'unmsm-c', 'San Marcos · Área C', 'Ingenierías.', 3, NULL, NULL, '{}'),
    ('demo-cepre', 'unmsm-d', 'San Marcos · Área D', 'Ciencias Económicas y de la Gestión.', 4, NULL, NULL, '{}'),
    ('demo-cepre', 'unmsm-e', 'San Marcos · Área E', 'Humanidades y Ciencias Jurídicas y Sociales.', 5, NULL, NULL, '{}'),
    ('demo-institute', 'enfermeria-tecnica', 'Enfermería Técnica', 'Cuidado del paciente en hospitales y centros de salud.', 0, 'professional_technical', 120, '{}'),
    ('demo-institute', 'desarrollo-sistemas', 'Desarrollo de Sistemas de Información', 'Programación, bases de datos y soporte de aplicaciones.', 1, 'professional_technical', 120, '{}'),
    ('demo-institute', 'contabilidad', 'Contabilidad', 'Contabilidad, tributación y gestión financiera.', 2, 'professional_technical', 120, '{}')
) AS v (slug, code, name, description, position, formative_level, credits, modes) ON v.slug = i.slug
ON CONFLICT DO NOTHING;

-- Grades, ages and plan semesters
WITH g (slug, level_code, code, name, short_name, position) AS (
    SELECT 'demo-ebr', 'inicial', n || '-anos', n || ' años', n || ' a', n - 3 FROM generate_series(3, 5) n
    UNION ALL SELECT 'demo-ebr', 'primaria', n::text, n || '.° grado', n || '.°', n - 1 FROM generate_series(1, 6) n
    UNION ALL SELECT 'demo-ebr', 'secundaria', n::text, n || '.° año', n || '.°', n - 1 FROM generate_series(1, 5) n
    UNION ALL SELECT 'demo-eba', 'inicial', n::text, n || '.° grado', n || '.°', n - 1 FROM generate_series(1, 2) n
    UNION ALL SELECT 'demo-eba', 'intermedio', n::text, n || '.° grado', n || '.°', n - 1 FROM generate_series(1, 3) n
    UNION ALL SELECT 'demo-eba', 'avanzado', n::text, n || '.° grado', n || '.°', n - 1 FROM generate_series(1, 4) n
    UNION ALL SELECT 'demo-ebe', 'prite', v.code, v.name, v.short_name, v.position
        FROM (VALUES ('0-1', '0 a 1 año', '0-1', 0), ('1-2', '1 a 2 años', '1-2', 1), ('2-3', '2 a 3 años', '2-3', 2)) v (code, name, short_name, position)
    UNION ALL SELECT 'demo-ebe', 'inicial', n || '-anos', n || ' años', n || ' a', n - 3 FROM generate_series(3, 5) n
    UNION ALL SELECT 'demo-ebe', 'primaria', n::text, n || '.° grado', n || '.°', n - 1 FROM generate_series(1, 6) n
    UNION ALL SELECT 'demo-institute', p.code, lower(r.roman), 'Semestre ' || r.roman, r.roman, r.n - 1
        FROM (VALUES ('enfermeria-tecnica'), ('desarrollo-sistemas'), ('contabilidad')) p (code)
        CROSS JOIN (VALUES (1, 'I'), (2, 'II'), (3, 'III'), (4, 'IV'), (5, 'V'), (6, 'VI')) r (n, roman)
)
INSERT INTO academic.grades (level_id, code, name, short_name, position)
SELECT l.id, g.code, g.name, g.short_name, g.position
FROM g
JOIN iam.institutions i ON i.slug = g.slug
JOIN academic.levels l ON l.institution_id = i.id AND l.code = g.level_code
ON CONFLICT DO NOTHING;

-- Periods
INSERT INTO academic.periods (institution_id, kind, name, season, starts_on, ends_on, term_scheme, status)
SELECT i.id, v.kind, v.name, v.season, v.starts_on::date, v.ends_on::date, v.term_scheme, v.status
FROM iam.institutions i
JOIN (VALUES
    ('demo-ebr', 'school_year', 'Año lectivo 2025', NULL, '2025-03-10', '2025-12-19', 'bimester', 'closed'),
    ('demo-ebr', 'school_year', 'Año lectivo 2026', NULL, '2026-03-09', '2026-12-18', 'bimester', 'active'),
    ('demo-eba', 'promotional', 'Periodo promocional 2026', NULL, '2026-03-16', '2026-12-11', 'bimester', 'active'),
    ('demo-eba', 'promotional', 'Periodo promocional 2027', NULL, '2027-03-15', '2027-12-10', 'bimester', 'planning'),
    ('demo-ebe', 'school_year', 'Año lectivo 2026', NULL, '2026-03-09', '2026-12-18', 'trimester', 'active'),
    ('demo-cepre', 'cycle', 'Ciclo Anual 2026', 'annual', '2026-03-02', '2026-12-12', 'none', 'active'),
    ('demo-cepre', 'cycle', 'Ciclo Repaso 2026', 'review', '2026-11-02', '2026-12-19', 'none', 'active'),
    ('demo-cepre', 'cycle', 'Ciclo Verano 2027', 'summer', '2027-01-04', '2027-03-06', 'none', 'planning'),
    ('demo-institute', 'semester', 'Semestre 2026-I', NULL, '2026-03-23', '2026-07-24', 'none', 'closed'),
    ('demo-institute', 'semester', 'Semestre 2026-II', NULL, '2026-08-17', '2026-12-18', 'none', 'active')
) AS v (slug, kind, name, season, starts_on, ends_on, term_scheme, status) ON v.slug = i.slug
ON CONFLICT DO NOTHING;

-- Terms, with the vacation weeks between them
INSERT INTO academic.terms (period_id, number, name, starts_on, ends_on, status)
SELECT p.id, v.number, v.name, v.starts_on::date, v.ends_on::date, v.status
FROM (VALUES
    ('demo-ebr', 'Año lectivo 2025', 1, 'I bimestre', '2025-03-10', '2025-05-09', 'closed'),
    ('demo-ebr', 'Año lectivo 2025', 2, 'II bimestre', '2025-05-19', '2025-07-25', 'closed'),
    ('demo-ebr', 'Año lectivo 2025', 3, 'III bimestre', '2025-08-11', '2025-10-10', 'closed'),
    ('demo-ebr', 'Año lectivo 2025', 4, 'IV bimestre', '2025-10-20', '2025-12-19', 'closed'),
    ('demo-ebr', 'Año lectivo 2026', 1, 'I bimestre', '2026-03-09', '2026-05-08', 'closed'),
    ('demo-ebr', 'Año lectivo 2026', 2, 'II bimestre', '2026-05-18', '2026-07-24', 'closed'),
    ('demo-ebr', 'Año lectivo 2026', 3, 'III bimestre', '2026-08-10', '2026-10-09', 'closed'),
    ('demo-ebr', 'Año lectivo 2026', 4, 'IV bimestre', '2026-10-19', '2026-12-18', 'open'),
    ('demo-eba', 'Periodo promocional 2026', 1, 'I bimestre', '2026-03-16', '2026-05-15', 'closed'),
    ('demo-eba', 'Periodo promocional 2026', 2, 'II bimestre', '2026-05-25', '2026-07-24', 'closed'),
    ('demo-eba', 'Periodo promocional 2026', 3, 'III bimestre', '2026-08-10', '2026-10-09', 'closed'),
    ('demo-eba', 'Periodo promocional 2026', 4, 'IV bimestre', '2026-10-19', '2026-12-11', 'open'),
    ('demo-eba', 'Periodo promocional 2027', 1, 'I bimestre', '2027-03-15', '2027-05-14', 'open'),
    ('demo-eba', 'Periodo promocional 2027', 2, 'II bimestre', '2027-05-24', '2027-07-23', 'open'),
    ('demo-eba', 'Periodo promocional 2027', 3, 'III bimestre', '2027-08-09', '2027-10-08', 'open'),
    ('demo-eba', 'Periodo promocional 2027', 4, 'IV bimestre', '2027-10-18', '2027-12-10', 'open'),
    ('demo-ebe', 'Año lectivo 2026', 1, 'I trimestre', '2026-03-09', '2026-05-29', 'closed'),
    ('demo-ebe', 'Año lectivo 2026', 2, 'II trimestre', '2026-06-08', '2026-09-04', 'closed'),
    ('demo-ebe', 'Año lectivo 2026', 3, 'III trimestre', '2026-09-14', '2026-12-18', 'open')
) AS v (slug, period_name, number, name, starts_on, ends_on, status)
JOIN iam.institutions i ON i.slug = v.slug
JOIN academic.periods p ON p.institution_id = i.id AND p.name = v.period_name
ON CONFLICT DO NOTHING;

-- Sections. Rows: institution, period, level, grade codes (NULL = every grade; CEPRE has none), names, shift,
-- form of attention, capacity
WITH plan (slug, period_name, level_code, grade_codes, names, shift, mode, capacity) AS (
    VALUES
        ('demo-ebr', 'Año lectivo 2025', 'inicial', NULL::text[], '{A}'::text[], 'morning', NULL, 20),
        ('demo-ebr', 'Año lectivo 2025', 'primaria', NULL, '{A,B}', 'morning', NULL, 30),
        ('demo-ebr', 'Año lectivo 2025', 'secundaria', NULL, '{A,B}', 'afternoon', NULL, 32),
        ('demo-ebr', 'Año lectivo 2026', 'inicial', NULL, '{A}', 'morning', NULL, 20),
        ('demo-ebr', 'Año lectivo 2026', 'primaria', NULL, '{A,B}', 'morning', NULL, 30),
        ('demo-ebr', 'Año lectivo 2026', 'secundaria', NULL, '{A,B}', 'afternoon', NULL, 32),
        ('demo-ebr', 'Año lectivo 2026', 'secundaria', '{1,2}', '{C}', 'afternoon', NULL, 32),
        ('demo-eba', 'Periodo promocional 2026', 'inicial', NULL, '{A}', 'afternoon', 'in_person', 20),
        ('demo-eba', 'Periodo promocional 2026', 'intermedio', NULL, '{A}', 'afternoon', 'in_person', 25),
        ('demo-eba', 'Periodo promocional 2026', 'avanzado', NULL, '{A}', 'evening', 'in_person', 30),
        ('demo-eba', 'Periodo promocional 2026', 'avanzado', '{3,4}', '{B}', 'evening', 'blended', 30),
        ('demo-ebe', 'Año lectivo 2026', 'prite', NULL, '{Grupo A}', 'morning', NULL, 8),
        ('demo-ebe', 'Año lectivo 2026', 'inicial', NULL, '{Girasoles}', 'morning', NULL, 8),
        ('demo-ebe', 'Año lectivo 2026', 'primaria', NULL, '{Arcoíris}', 'morning', NULL, 10),
        ('demo-institute', 'Semestre 2026-I', 'enfermeria-tecnica', '{i,iii,v}', '{A}', 'morning', NULL, 35),
        ('demo-institute', 'Semestre 2026-I', 'desarrollo-sistemas', '{i,iii,v}', '{A}', 'evening', NULL, 30),
        ('demo-institute', 'Semestre 2026-I', 'contabilidad', '{i,iii,v}', '{A}', 'evening', NULL, 30),
        ('demo-institute', 'Semestre 2026-II', 'enfermeria-tecnica', '{ii,iv,vi}', '{A}', 'morning', NULL, 35),
        ('demo-institute', 'Semestre 2026-II', 'enfermeria-tecnica', '{ii}', '{B}', 'evening', NULL, 35),
        ('demo-institute', 'Semestre 2026-II', 'desarrollo-sistemas', '{ii,iv,vi}', '{A}', 'evening', NULL, 30),
        ('demo-institute', 'Semestre 2026-II', 'contabilidad', '{ii,iv,vi}', '{A}', 'evening', NULL, 30)
)
INSERT INTO academic.sections (period_id, level_id, grade_id, name, shift, attention_mode, capacity)
SELECT p.id, l.id, g.id, n.name, plan.shift, plan.mode, plan.capacity
FROM plan
JOIN iam.institutions i ON i.slug = plan.slug
JOIN academic.periods p ON p.institution_id = i.id AND p.name = plan.period_name
JOIN academic.levels l ON l.institution_id = i.id AND l.code = plan.level_code
JOIN academic.grades g ON g.level_id = l.id AND (plan.grade_codes IS NULL OR g.code = ANY (plan.grade_codes))
CROSS JOIN LATERAL unnest(plan.names) AS n (name)
ON CONFLICT DO NOTHING;

-- CEPRE classrooms: one per area and shift in the annual cycle, an evening one per area in the review cycle
INSERT INTO academic.sections (period_id, level_id, grade_id, name, shift, capacity, room)
SELECT p.id, l.id, NULL, v.name, v.shift, v.capacity, v.room
FROM iam.institutions i
JOIN academic.levels l ON l.institution_id = i.id
JOIN (VALUES
    ('Ciclo Anual 2026', 'Aula 1 · Mañana', 'morning', 45, 'Pabellón A'),
    ('Ciclo Anual 2026', 'Aula 2 · Tarde', 'afternoon', 45, 'Pabellón A'),
    ('Ciclo Repaso 2026', 'Repaso · Noche', 'evening', 50, 'Pabellón B')
) AS v (period_name, name, shift, capacity, room) ON true
JOIN academic.periods p ON p.institution_id = i.id AND p.name = v.period_name
WHERE i.slug = 'demo-cepre'
ON CONFLICT DO NOTHING;
