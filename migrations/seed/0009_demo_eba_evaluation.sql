-- EBA closing of the CEBA demo (docs/ceba/PLAN.md, phase E7): the academic coordinator and the director sign the
-- consolidated actas, the director signs the certificates. The promotion rules stay those of the code until the
-- CEBA sets its own
WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba'),
signers AS (
    SELECT inst.id AS institution_id, st.id AS staff_id, st.position,
        CASE WHEN st.position ILIKE 'Coordinador%' THEN 1 ELSE 2 END AS sort,
        row_number() OVER (PARTITION BY st.position ILIKE 'Coordinador%' ORDER BY st.created_at) AS n
    FROM inst JOIN people.staff st ON st.institution_id = inst.id AND (st.position ILIKE 'Director%' OR st.position ILIKE 'Coordinador(a) académico%')
)
INSERT INTO documents.signers (institution_id, kind, staff_id, position, sort)
SELECT institution_id, 'evaluation_record', staff_id, CASE WHEN sort = 1 THEN 'Coordinador académico' ELSE 'Director del CEBA' END, sort
FROM signers WHERE n = 1
ON CONFLICT DO NOTHING;

WITH inst AS (SELECT id FROM iam.institutions WHERE slug = 'demo-eba')
INSERT INTO documents.signers (institution_id, kind, staff_id, position, sort)
SELECT inst.id, 'certificate', st.id, 'Director del CEBA', 1
FROM inst JOIN people.staff st ON st.institution_id = inst.id AND st.position ILIKE 'Director%'
ORDER BY st.created_at
LIMIT 1
ON CONFLICT DO NOTHING;
