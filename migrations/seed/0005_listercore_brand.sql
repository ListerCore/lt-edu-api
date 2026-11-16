-- The platform is now ListerCore Edu: the demo institutions and the superadmin role carry the new brand. Only renames
-- the demos still holding the Joseph names, so an institution renamed by hand keeps its name
UPDATE iam.institutions i SET name = v.name
FROM (VALUES
    ('demo-ebr',       'Joseph Colegio',   'ListerCore Colegio'),
    ('demo-eba',       'Joseph Escuela',   'ListerCore CEBA'),
    ('demo-ebe',       'Joseph Especial',  'ListerCore CEBE'),
    ('demo-cepre',     'Joseph Academia',  'ListerCore Academia'),
    ('demo-institute', 'Joseph Instituto', 'ListerCore Instituto')
) AS v (slug, old_name, name)
WHERE i.slug = v.slug AND i.name = v.old_name;

UPDATE iam.roles
SET description = 'Gestiona instituciones, planes y módulos activos. Solo equipo de ListerCore Edu.'
WHERE code = 'superadmin';
