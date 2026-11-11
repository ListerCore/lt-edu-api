-- The demo institutions carry the platform's own names, so no real school appears in the demo. Only renames the ones
-- still holding the old sample names
UPDATE iam.institutions i SET name = v.name
FROM (VALUES
    ('demo-ebr',       'Colegio Demo',               'Joseph Colegio'),
    ('demo-eba',       'CEBA Demo',                  'Joseph Escuela'),
    ('demo-ebe',       'CEBE Demo',                  'Joseph Especial'),
    ('demo-cepre',     'Academia Pre Demo',          'Joseph Academia'),
    ('demo-institute', 'Instituto Tecnológico Demo', 'Joseph Instituto')
) AS v (slug, old_name, name)
WHERE i.slug = v.slug AND i.name = v.old_name;
