-- Alta del CEBA Privado Virtual "Joseph Lister" como institución de la
-- plataforma, con su plan de estudios (propuesta SIGE-JL §3 + áreas
-- complementarias de joseph-app) y el periodo 2026-II.
--
-- No es una migración: es la configuración de un cliente. Se aplica con
-- `make db/seed` y es idempotente (no hace nada si la institución ya existe).
--
-- Pendientes de completar con datos reales: RUC, códigos modulares,
-- resolución de creación, cortes de evaluación (bimestres).

DO $$
DECLARE
    inst uuid;
    per  uuid;
BEGIN
    IF EXISTS (SELECT 1 FROM tenant.institution WHERE slug = 'joseph-lister') THEN
        RAISE NOTICE 'joseph-lister ya existe; seed omitido';
        RETURN;
    END IF;

    INSERT INTO tenant.institution (slug, name, management, dre, ugel, ubigeo, status)
    VALUES ('joseph-lister', 'CEBA Privado Virtual "Joseph Lister"', 'private',
            'DRE Ayacucho', 'UGEL Huamanga', '050101', 'active')
    RETURNING id INTO inst;

    -- EBA a distancia: inicial e intermedio (equivalente a primaria) y avanzado
    -- (equivalente a secundaria).
    INSERT INTO tenant.service (institution_id, modality_code, level, attention_form) VALUES
        (inst, 'eba', 'primary',   'distance'),
        (inst, 'eba', 'secondary', 'distance');

    INSERT INTO tenant.campus (institution_id, name, is_main)
    VALUES (inst, 'Sede Ayacucho', true);

    -- Plan de estudios. Horas solo donde el RVM N.° 034-2019-MINEDU las fija:
    -- EPT 2 h en inicial e intermedio, Inglés 1 h en intermedio.
    INSERT INTO academic.study_plan_area (institution_id, cycle_code, area_code, weekly_hours)
    SELECT inst, p.cycle_code, p.area_code, p.weekly_hours
    FROM (VALUES
        ('eba_initial', 'communication', NULL),
        ('eba_initial', 'mathematics', NULL),
        ('eba_initial', 'science_technology_health', NULL),
        ('eba_initial', 'personal_citizenship', NULL),
        ('eba_initial', 'work_education', 2),
        ('eba_initial', 'religious_education', NULL),
        ('eba_initial', 'tutoring', NULL),

        ('eba_intermediate', 'communication', NULL),
        ('eba_intermediate', 'mathematics', NULL),
        ('eba_intermediate', 'science_technology_health', NULL),
        ('eba_intermediate', 'personal_citizenship', NULL),
        ('eba_intermediate', 'work_education', 2),
        ('eba_intermediate', 'religious_education', NULL),
        ('eba_intermediate', 'english', 1),
        ('eba_intermediate', 'tutoring', NULL),
        ('eba_intermediate', 'digital_competence', NULL),

        ('eba_advanced', 'communication', NULL),
        ('eba_advanced', 'mathematics', NULL),
        ('eba_advanced', 'science_technology_health', NULL),
        ('eba_advanced', 'personal_citizenship', NULL),
        ('eba_advanced', 'work_education', NULL),
        ('eba_advanced', 'religious_education', NULL),
        ('eba_advanced', 'english', NULL),
        ('eba_advanced', 'art_culture', NULL),
        ('eba_advanced', 'physical_education', NULL),
        ('eba_advanced', 'tutoring', NULL),
        ('eba_advanced', 'technical_certification', NULL)
    ) AS p (cycle_code, area_code, weekly_hours);

    INSERT INTO academic.period (institution_id, modality_code, code, starts_on, ends_on,
                                 enrollment_opens_on, enrollment_closes_on, status)
    VALUES (inst, 'eba', '2026-II', '2026-08-15', '2026-11-30', '2026-08-01', '2026-08-31', 'active')
    RETURNING id INTO per;

    -- Cronograma de pagos mostrado en joseph-app (finance-data).
    INSERT INTO finance.fee_schedule (institution_id, period_id, concept_code, installment, label, amount, due_on) VALUES
        (inst, per, 'enrollment', 1, 'Matrícula institucional', 100, '2026-08-15'),
        (inst, per, 'tuition',    1, 'Cuota 1 · Septiembre',    150, '2026-09-05'),
        (inst, per, 'tuition',    2, 'Cuota 2 · Octubre',       150, '2026-10-05'),
        (inst, per, 'tuition',    3, 'Cuota 3 · Noviembre',     150, '2026-11-05'),
        (inst, per, 'tuition',    4, 'Cuota 4 · Diciembre',     150, '2026-12-05');
END
$$;
