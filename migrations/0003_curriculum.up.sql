-- Catálogo curricular nacional (global, igual para todas las instituciones).
--
-- Fuentes:
--   * Currículo Nacional de la Educación Básica (CNEB, RM N.° 281-2016-MINEDU y
--     RM N.° 159-2017-MINEDU): 31 competencias, sus capacidades y el plan de
--     estudios de EBR (Tabla N.° 4 y Cuadro N.° 1).
--   * Programa Curricular de Educación Inicial: competencias de los ciclos I y II.
--   * RVM N.° 034-2019-MINEDU, programas curriculares de EBA (ciclos inicial e
--     intermedio, Tabla 1; ciclo avanzado, Tablas 1 y 2): áreas y competencias
--     por área de EBA. Reemplaza la Tabla N.° 10 del CNEB.
--
--   modality ─< cycle ─< grade
--   cycle >─< area                 (cycle_area: plan de estudios oficial)
--   cycle >─< competency           (cycle_competency: en qué área se trabaja
--                                   cada competencia, o null si es transversal)
--   competency ─< capacity
--
-- Las áreas complementarias (talleres, tutoría) no son parte del plan oficial:
-- cada institución las agrega a su propio plan (academic.study_plan_area).

CREATE SCHEMA IF NOT EXISTS curriculum;

-- Escala de logro CNEB. Se usa en ubicación, evaluaciones y actas.
CREATE DOMAIN curriculum.achievement_level AS text
    CHECK (VALUE IN ('AD', 'A', 'B', 'C'));

CREATE TABLE curriculum.modality (
    code text PRIMARY KEY,
    name text NOT NULL
);

CREATE TABLE curriculum.cycle (
    code          text PRIMARY KEY,
    modality_code text NOT NULL REFERENCES curriculum.modality (code),
    -- Nivel en EBR; en EBA, nivel equivalente para certificación
    -- (inicial e intermedio ≈ primaria, avanzado ≈ secundaria).
    level         text NOT NULL CHECK (level IN ('initial', 'primary', 'secondary')),
    name          text NOT NULL,
    position      smallint NOT NULL UNIQUE
);

CREATE TABLE curriculum.grade (
    code       text PRIMARY KEY,
    cycle_code text NOT NULL REFERENCES curriculum.cycle (code),
    -- Grado dentro del nivel; en Inicial EBR es la edad (0–5 años).
    number     smallint NOT NULL CHECK (number BETWEEN 0 AND 6),
    name       text NOT NULL,
    position   smallint NOT NULL UNIQUE,
    UNIQUE (cycle_code, number),
    -- Destino de FKs que guardan grado y ciclo juntos (academic.section).
    UNIQUE (code, cycle_code)
);

CREATE TABLE curriculum.area (
    code           text PRIMARY KEY,
    name           text NOT NULL,
    -- curricular: área del plan oficial; tutoring: tutoría y orientación
    -- educativa (parte del currículo, no es área); workshop: taller o área
    -- complementaria de horas de libre disponibilidad.
    kind           text NOT NULL DEFAULT 'curricular' CHECK (kind IN ('curricular', 'tutoring', 'workshop')),
    -- Educación Religiosa: exonerable a pedido (Ley N.° 29635).
    exemptible     boolean NOT NULL DEFAULT false,
    -- Castellano como segunda lengua: solo en II.EE. bilingües.
    bilingual_only boolean NOT NULL DEFAULT false
);

CREATE TABLE curriculum.competency (
    -- Número oficial en el CNEB (1–31).
    number smallint PRIMARY KEY CHECK (number > 0),
    name   text NOT NULL
);

CREATE TABLE curriculum.capacity (
    -- "<competencia>.<orden>", ej. 23.3.
    code              text PRIMARY KEY,
    competency_number smallint NOT NULL REFERENCES curriculum.competency (number),
    position          smallint NOT NULL,
    name              text NOT NULL,
    UNIQUE (competency_number, position)
);

CREATE TABLE curriculum.cycle_area (
    cycle_code text NOT NULL REFERENCES curriculum.cycle (code),
    area_code  text NOT NULL REFERENCES curriculum.area (code),
    PRIMARY KEY (cycle_code, area_code)
);

-- Cada competencia se trabaja en un solo lugar por ciclo: en un área o, si
-- area_code es null, de manera transversal a todas las áreas.
CREATE TABLE curriculum.cycle_competency (
    cycle_code        text NOT NULL REFERENCES curriculum.cycle (code),
    competency_number smallint NOT NULL REFERENCES curriculum.competency (number),
    area_code         text,
    PRIMARY KEY (cycle_code, competency_number),
    FOREIGN KEY (cycle_code, area_code) REFERENCES curriculum.cycle_area (cycle_code, area_code)
);

-- Competencias que desarrolla un taller (área kind = 'workshop'), que no
-- figura en el plan oficial.
CREATE TABLE curriculum.workshop_competency (
    area_code         text NOT NULL REFERENCES curriculum.area (code),
    competency_number smallint NOT NULL REFERENCES curriculum.competency (number),
    PRIMARY KEY (area_code, competency_number)
);

-- ¿Se puede evaluar esta competencia en un aula de este ciclo y área? Sí si
-- el área la trabaja en el ciclo, si es transversal en el ciclo, o si el área
-- es un taller que la desarrolla.
CREATE FUNCTION curriculum.is_competency_of(cycle text, area text, competency smallint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM curriculum.cycle_competency cc
        WHERE cc.cycle_code = cycle AND cc.competency_number = competency
          AND (cc.area_code = area OR cc.area_code IS NULL)
    ) OR EXISTS (
        SELECT 1 FROM curriculum.workshop_competency wc
        WHERE wc.area_code = area AND wc.competency_number = competency
    )
$$;

-- ---------------------------------------------------------------------------
-- Modalidades, ciclos y grados
-- ---------------------------------------------------------------------------

INSERT INTO curriculum.modality (code, name) VALUES
    ('ebr', 'Educación Básica Regular'),
    ('eba', 'Educación Básica Alternativa');

INSERT INTO curriculum.cycle (code, modality_code, level, name, position) VALUES
    ('ebr_i',            'ebr', 'initial',   'Ciclo I',          1),
    ('ebr_ii',           'ebr', 'initial',   'Ciclo II',         2),
    ('ebr_iii',          'ebr', 'primary',   'Ciclo III',        3),
    ('ebr_iv',           'ebr', 'primary',   'Ciclo IV',         4),
    ('ebr_v',            'ebr', 'primary',   'Ciclo V',          5),
    ('ebr_vi',           'ebr', 'secondary', 'Ciclo VI',         6),
    ('ebr_vii',          'ebr', 'secondary', 'Ciclo VII',        7),
    ('eba_initial',      'eba', 'primary',   'Ciclo Inicial',    8),
    ('eba_intermediate', 'eba', 'primary',   'Ciclo Intermedio', 9),
    ('eba_advanced',     'eba', 'secondary', 'Ciclo Avanzado',   10);

INSERT INTO curriculum.grade (code, cycle_code, number, name, position)
SELECT g.cycle_code || '_' || g.number, g.cycle_code, g.number, g.name, row_number() OVER (ORDER BY c.position, g.number)
FROM (VALUES
    ('ebr_i', 0, '0 años'), ('ebr_i', 1, '1 año'), ('ebr_i', 2, '2 años'),
    ('ebr_ii', 3, '3 años'), ('ebr_ii', 4, '4 años'), ('ebr_ii', 5, '5 años'),
    ('ebr_iii', 1, '1.° grado'), ('ebr_iii', 2, '2.° grado'),
    ('ebr_iv', 3, '3.° grado'), ('ebr_iv', 4, '4.° grado'),
    ('ebr_v', 5, '5.° grado'), ('ebr_v', 6, '6.° grado'),
    ('ebr_vi', 1, '1.° grado'), ('ebr_vi', 2, '2.° grado'),
    ('ebr_vii', 3, '3.° grado'), ('ebr_vii', 4, '4.° grado'), ('ebr_vii', 5, '5.° grado'),
    ('eba_initial', 1, '1.° grado'), ('eba_initial', 2, '2.° grado'),
    ('eba_intermediate', 1, '1.° grado'), ('eba_intermediate', 2, '2.° grado'), ('eba_intermediate', 3, '3.° grado'),
    ('eba_advanced', 1, '1.° grado'), ('eba_advanced', 2, '2.° grado'),
    ('eba_advanced', 3, '3.° grado'), ('eba_advanced', 4, '4.° grado')
) AS g (cycle_code, number, name)
JOIN curriculum.cycle c ON c.code = g.cycle_code;

-- ---------------------------------------------------------------------------
-- Áreas
-- ---------------------------------------------------------------------------

INSERT INTO curriculum.area (code, name, kind, exemptible, bilingual_only) VALUES
    -- EBR y EBA
    ('communication',               'Comunicación',                            'curricular', false, false),
    ('spanish_l2',                  'Castellano como segunda lengua',          'curricular', false, true),
    ('english',                     'Inglés como lengua extranjera',           'curricular', false, false),
    ('mathematics',                 'Matemática',                              'curricular', false, false),
    ('art_culture',                 'Arte y Cultura',                          'curricular', false, false),
    ('physical_education',          'Educación Física',                        'curricular', false, false),
    ('religious_education',         'Educación Religiosa',                     'curricular', true,  false),
    ('work_education',              'Educación para el Trabajo',               'curricular', false, false),
    -- Solo EBR
    ('personal_social',             'Personal Social',                         'curricular', false, false),
    ('psychomotor',                 'Psicomotriz',                             'curricular', false, false),
    ('world_discovery',             'Descubrimiento del Mundo',                'curricular', false, false),
    ('science_technology',          'Ciencia y Tecnología',                    'curricular', false, false),
    ('personal_citizenship_civics', 'Desarrollo Personal, Ciudadanía y Cívica','curricular', false, false),
    ('social_sciences',             'Ciencias Sociales',                       'curricular', false, false),
    -- Solo EBA
    ('personal_citizenship',        'Desarrollo Personal y Ciudadano',         'curricular', false, false),
    ('science_technology_health',   'Ciencia, Tecnología y Salud',             'curricular', false, false),
    -- Complementarias
    ('tutoring',                    'Tutoría y Orientación Educativa',         'tutoring',   false, false),
    ('digital_competence',          'TIC y Competencia Digital',               'workshop',   false, false),
    ('technical_certification',     'Certificación Técnica Modular',           'workshop',   false, false);

-- ---------------------------------------------------------------------------
-- Competencias y capacidades (CNEB)
-- ---------------------------------------------------------------------------

INSERT INTO curriculum.competency (number, name) VALUES
    (1,  'Construye su identidad'),
    (2,  'Se desenvuelve de manera autónoma a través de su motricidad'),
    (3,  'Asume una vida saludable'),
    (4,  'Interactúa a través de sus habilidades sociomotrices'),
    (5,  'Aprecia de manera crítica manifestaciones artístico-culturales'),
    (6,  'Crea proyectos desde los lenguajes artísticos'),
    (7,  'Se comunica oralmente en su lengua materna'),
    (8,  'Lee diversos tipos de textos escritos en su lengua materna'),
    (9,  'Escribe diversos tipos de textos en su lengua materna'),
    (10, 'Se comunica oralmente en castellano como segunda lengua'),
    (11, 'Lee diversos tipos de textos escritos en castellano como segunda lengua'),
    (12, 'Escribe diversos tipos de textos en castellano como segunda lengua'),
    (13, 'Se comunica oralmente en inglés como lengua extranjera'),
    (14, 'Lee diversos tipos de textos escritos en inglés como lengua extranjera'),
    (15, 'Escribe diversos tipos de textos en inglés como lengua extranjera'),
    (16, 'Convive y participa democráticamente en la búsqueda del bien común'),
    (17, 'Construye interpretaciones históricas'),
    (18, 'Gestiona responsablemente el espacio y el ambiente'),
    (19, 'Gestiona responsablemente los recursos económicos'),
    (20, 'Indaga mediante métodos científicos para construir conocimientos'),
    (21, 'Explica el mundo físico basándose en conocimientos sobre los seres vivos, materia y energía, biodiversidad, Tierra y universo'),
    (22, 'Diseña y construye soluciones tecnológicas para resolver problemas de su entorno'),
    (23, 'Resuelve problemas de cantidad'),
    (24, 'Resuelve problemas de regularidad, equivalencia y cambio'),
    (25, 'Resuelve problemas de gestión de datos e incertidumbre'),
    (26, 'Resuelve problemas de forma, movimiento y localización'),
    (27, 'Gestiona proyectos de emprendimiento económico o social'),
    (28, 'Se desenvuelve en entornos virtuales generados por las TIC'),
    (29, 'Gestiona su aprendizaje de manera autónoma'),
    (30, 'Construye su identidad como persona humana, amada por Dios, digna, libre y trascendente, comprendiendo la doctrina de su propia religión, abierto al diálogo con las que le son cercanas'),
    (31, 'Asume la experiencia del encuentro personal y comunitario con Dios en su proyecto de vida en coherencia con su creencia religiosa');

-- Las competencias de lengua (7–15) comparten capacidades según sean orales,
-- de lectura o de escritura; se generan a partir de tres plantillas.
WITH
oral (position, name) AS (VALUES
    (1, 'Obtiene información de textos orales'),
    (2, 'Infiere e interpreta información de textos orales'),
    (3, 'Adecúa, organiza y desarrolla las ideas de forma coherente y cohesionada'),
    (4, 'Utiliza recursos no verbales y paraverbales de forma estratégica'),
    (5, 'Interactúa estratégicamente con distintos interlocutores'),
    (6, 'Reflexiona y evalúa la forma, el contenido y el contexto del texto oral')),
reading (position, name) AS (VALUES
    (1, 'Obtiene información del texto escrito'),
    (2, 'Infiere e interpreta información del texto'),
    (3, 'Reflexiona y evalúa la forma, el contenido y el contexto del texto escrito')),
writing (position, name) AS (VALUES
    (1, 'Adecúa el texto a la situación comunicativa'),
    (2, 'Organiza y desarrolla las ideas de forma coherente y cohesionada'),
    (3, 'Utiliza convenciones del lenguaje escrito de forma pertinente'),
    (4, 'Reflexiona y evalúa la forma, el contenido y el contexto del texto escrito')),
language (competency_number, position, name) AS (
    SELECT c, o.position, o.name FROM unnest(ARRAY[7, 10, 13]) AS c CROSS JOIN oral o
    UNION ALL
    SELECT c, r.position, r.name FROM unnest(ARRAY[8, 11, 14]) AS c CROSS JOIN reading r
    UNION ALL
    SELECT c, w.position, w.name FROM unnest(ARRAY[9, 12, 15]) AS c CROSS JOIN writing w),
other (competency_number, position, name) AS (VALUES
    (1, 1, 'Se valora a sí mismo'),
    (1, 2, 'Autorregula sus emociones'),
    (1, 3, 'Reflexiona y argumenta éticamente'),
    (1, 4, 'Vive su sexualidad de manera integral y responsable de acuerdo a su etapa de desarrollo y madurez'),
    (2, 1, 'Comprende su cuerpo'),
    (2, 2, 'Se expresa corporalmente'),
    (3, 1, 'Comprende las relaciones entre la actividad física, alimentación, postura e higiene y la salud'),
    (3, 2, 'Incorpora prácticas que mejoran su calidad de vida'),
    (4, 1, 'Se relaciona utilizando sus habilidades sociomotrices'),
    (4, 2, 'Crea y aplica estrategias y tácticas de juego'),
    (5, 1, 'Percibe manifestaciones artístico-culturales'),
    (5, 2, 'Contextualiza las manifestaciones artístico-culturales'),
    (5, 3, 'Reflexiona creativa y críticamente sobre las manifestaciones artístico-culturales'),
    (6, 1, 'Explora y experimenta los lenguajes de las artes'),
    (6, 2, 'Aplica procesos de creación'),
    (6, 3, 'Evalúa y comunica sus procesos y proyectos'),
    (16, 1, 'Interactúa con todas las personas'),
    (16, 2, 'Construye y asume acuerdos y normas'),
    (16, 3, 'Maneja conflictos de manera constructiva'),
    (16, 4, 'Delibera sobre asuntos públicos'),
    (16, 5, 'Participa en acciones que promueven el bienestar común'),
    (17, 1, 'Interpreta críticamente fuentes diversas'),
    (17, 2, 'Comprende el tiempo histórico'),
    (17, 3, 'Explica y argumenta procesos históricos'),
    (18, 1, 'Comprende las relaciones entre los elementos naturales y sociales'),
    (18, 2, 'Maneja fuentes de información para comprender el espacio geográfico y el ambiente'),
    (18, 3, 'Genera acciones para preservar el ambiente local y global'),
    (19, 1, 'Comprende las relaciones entre los elementos del sistema económico y financiero'),
    (19, 2, 'Toma decisiones económicas y financieras'),
    (20, 1, 'Problematiza situaciones'),
    (20, 2, 'Diseña estrategias para hacer indagación'),
    (20, 3, 'Genera y registra datos e información'),
    (20, 4, 'Analiza datos e información'),
    (20, 5, 'Evalúa y comunica el proceso y los resultados de su indagación'),
    (21, 1, 'Comprende y usa conocimientos sobre los seres vivos, materia y energía, biodiversidad, Tierra y universo'),
    (21, 2, 'Evalúa las implicancias del saber y del quehacer científico y tecnológico'),
    (22, 1, 'Determina una alternativa de solución tecnológica'),
    (22, 2, 'Diseña la alternativa de solución tecnológica'),
    (22, 3, 'Implementa y valida alternativas de solución tecnológica'),
    (22, 4, 'Evalúa y comunica el funcionamiento y los impactos de su alternativa de solución tecnológica'),
    (23, 1, 'Traduce cantidades a expresiones numéricas'),
    (23, 2, 'Comunica su comprensión sobre los números y las operaciones'),
    (23, 3, 'Usa estrategias y procedimientos de estimación y cálculo'),
    (23, 4, 'Argumenta afirmaciones sobre las relaciones numéricas y las operaciones'),
    (24, 1, 'Traduce datos y condiciones a expresiones algebraicas'),
    (24, 2, 'Comunica su comprensión sobre las relaciones algebraicas'),
    (24, 3, 'Usa estrategias y procedimientos para encontrar reglas generales'),
    (24, 4, 'Argumenta afirmaciones sobre relaciones de cambio y equivalencia'),
    (25, 1, 'Representa datos con gráficos y medidas estadísticas o probabilísticas'),
    (25, 2, 'Comunica la comprensión de los conceptos estadísticos y probabilísticos'),
    (25, 3, 'Usa estrategias y procedimientos para recopilar y procesar datos'),
    (25, 4, 'Sustenta conclusiones o decisiones basado en información obtenida'),
    (26, 1, 'Modela objetos con formas geométricas y sus transformaciones'),
    (26, 2, 'Comunica su comprensión sobre las formas y relaciones geométricas'),
    (26, 3, 'Usa estrategias y procedimientos para orientarse en el espacio'),
    (26, 4, 'Argumenta afirmaciones sobre relaciones geométricas'),
    (27, 1, 'Crea propuestas de valor'),
    (27, 2, 'Trabaja cooperativamente para lograr objetivos y metas'),
    (27, 3, 'Aplica habilidades técnicas'),
    (27, 4, 'Evalúa los resultados del proyecto de emprendimiento'),
    (28, 1, 'Personaliza entornos virtuales'),
    (28, 2, 'Gestiona información del entorno virtual'),
    (28, 3, 'Interactúa en entornos virtuales'),
    (28, 4, 'Crea objetos virtuales en diversos formatos'),
    (29, 1, 'Define metas de aprendizaje'),
    (29, 2, 'Organiza acciones estratégicas para alcanzar sus metas de aprendizaje'),
    (29, 3, 'Monitorea y ajusta su desempeño durante el proceso de aprendizaje'),
    (30, 1, 'Conoce a Dios y asume su identidad religiosa como persona digna, libre y trascendente'),
    (30, 2, 'Cultiva y valora las manifestaciones religiosas de su entorno argumentando su fe de manera comprensible y respetuosa'),
    (31, 1, 'Transforma su entorno desde el encuentro personal y comunitario con Dios y desde la fe que profesa'),
    (31, 2, 'Actúa coherentemente en razón de su fe según los principios de su conciencia moral en situaciones concretas de la vida'))
INSERT INTO curriculum.capacity (code, competency_number, position, name)
SELECT competency_number || '.' || position, competency_number, position, name
FROM (SELECT * FROM language UNION ALL SELECT * FROM other) AS all_capacities;

-- ---------------------------------------------------------------------------
-- Plan de estudios oficial: área → competencias, por grupo de ciclos.
-- Cada fila (ciclos, área, competencias) se expande a cycle_area y
-- cycle_competency; área null = competencias transversales.
-- ---------------------------------------------------------------------------

CREATE TEMPORARY TABLE official_plan (cycles text[], area_code text, competencies smallint[]) ON COMMIT DROP;

INSERT INTO official_plan (cycles, area_code, competencies) VALUES
    -- EBR Inicial, ciclo I (Programa Curricular de Educación Inicial)
    ('{ebr_i}', 'personal_social',  '{1,16}'),
    ('{ebr_i}', 'psychomotor',      '{2}'),
    ('{ebr_i}', 'communication',    '{7}'),
    ('{ebr_i}', 'world_discovery',  '{20,23,26}'),
    -- EBR Inicial, ciclo II
    ('{ebr_ii}', 'personal_social',    '{1,16,30}'),
    ('{ebr_ii}', 'psychomotor',        '{2}'),
    ('{ebr_ii}', 'communication',      '{7,8,9,6}'),
    ('{ebr_ii}', 'spanish_l2',         '{10}'),
    ('{ebr_ii}', 'mathematics',        '{23,26}'),
    ('{ebr_ii}', 'science_technology', '{20}'),
    ('{ebr_ii}', NULL,                 '{28,29}'),
    -- EBR Primaria (CNEB, Cuadro N.° 1)
    ('{ebr_iii,ebr_iv,ebr_v}', 'personal_social',     '{1,16,17,18,19}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'religious_education', '{30,31}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'physical_education',  '{2,3,4}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'communication',       '{7,8,9}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'art_culture',         '{5,6}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'spanish_l2',          '{10,11,12}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'english',             '{13,14,15}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'mathematics',         '{23,24,25,26}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'science_technology',  '{20,21,22}'),
    ('{ebr_iii,ebr_iv,ebr_v}', 'tutoring',            '{}'),
    ('{ebr_iii,ebr_iv,ebr_v}', NULL,                  '{28,29}'),
    -- EBR Secundaria (CNEB, Cuadro N.° 1)
    ('{ebr_vi,ebr_vii}', 'personal_citizenship_civics', '{1,16}'),
    ('{ebr_vi,ebr_vii}', 'social_sciences',             '{17,18,19}'),
    ('{ebr_vi,ebr_vii}', 'religious_education',         '{30,31}'),
    ('{ebr_vi,ebr_vii}', 'work_education',              '{27}'),
    ('{ebr_vi,ebr_vii}', 'physical_education',          '{2,3,4}'),
    ('{ebr_vi,ebr_vii}', 'communication',               '{7,8,9}'),
    ('{ebr_vi,ebr_vii}', 'art_culture',                 '{5,6}'),
    ('{ebr_vi,ebr_vii}', 'spanish_l2',                  '{10,11,12}'),
    ('{ebr_vi,ebr_vii}', 'english',                     '{13,14,15}'),
    ('{ebr_vi,ebr_vii}', 'mathematics',                 '{23,24,25,26}'),
    ('{ebr_vi,ebr_vii}', 'science_technology',          '{20,21,22}'),
    ('{ebr_vi,ebr_vii}', 'tutoring',                    '{}'),
    ('{ebr_vi,ebr_vii}', NULL,                          '{28,29}'),
    -- EBA ciclos inicial e intermedio (RVM N.° 034-2019-MINEDU)
    ('{eba_initial,eba_intermediate}', 'communication',             '{7,8,9}'),
    ('{eba_initial,eba_intermediate}', 'spanish_l2',                '{10,11,12}'),
    ('{eba_initial,eba_intermediate}', 'mathematics',               '{23,24,25,26}'),
    ('{eba_initial,eba_intermediate}', 'science_technology_health', '{20,21,22}'),
    ('{eba_initial,eba_intermediate}', 'personal_citizenship',      '{1,16,17,18,19}'),
    ('{eba_initial,eba_intermediate}', 'work_education',            '{27}'),
    ('{eba_initial,eba_intermediate}', 'religious_education',       '{30,31}'),
    ('{eba_initial,eba_intermediate}', 'tutoring',                  '{}'),
    ('{eba_initial,eba_intermediate}', NULL,                        '{28,29,5,6,2,3,4}'),
    -- Inglés en intermedio: solo la competencia oral.
    ('{eba_intermediate}', 'english', '{13}'),
    -- EBA ciclo avanzado (RVM N.° 034-2019-MINEDU, Tabla 2)
    ('{eba_advanced}', 'communication',             '{7,8,9}'),
    ('{eba_advanced}', 'spanish_l2',                '{10,11,12}'),
    ('{eba_advanced}', 'mathematics',               '{23,24,25,26}'),
    ('{eba_advanced}', 'science_technology_health', '{20,21,22}'),
    ('{eba_advanced}', 'personal_citizenship',      '{1,16,17,18,19}'),
    ('{eba_advanced}', 'work_education',            '{27}'),
    ('{eba_advanced}', 'religious_education',       '{30,31}'),
    ('{eba_advanced}', 'english',                   '{13,14,15}'),
    ('{eba_advanced}', 'art_culture',               '{5,6}'),
    ('{eba_advanced}', 'physical_education',        '{2,3,4}'),
    ('{eba_advanced}', 'tutoring',                  '{}'),
    ('{eba_advanced}', NULL,                        '{28,29}');

INSERT INTO curriculum.cycle_area (cycle_code, area_code)
SELECT DISTINCT cycle, area_code
FROM official_plan, unnest(cycles) AS cycle
WHERE area_code IS NOT NULL;

INSERT INTO curriculum.cycle_competency (cycle_code, competency_number, area_code)
SELECT cycle, competency, area_code
FROM official_plan, unnest(cycles) AS cycle, unnest(competencies) AS competency;

INSERT INTO curriculum.workshop_competency (area_code, competency_number) VALUES
    ('digital_competence',      28),
    ('technical_certification', 27);
