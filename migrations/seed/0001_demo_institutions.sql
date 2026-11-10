-- One demo institution per type, for the public demo (demo.listercore.es): visitors pick a type and a role and
-- browse sample data. ON CONFLICT keeps it safe on a database that already has them
INSERT INTO iam.institutions (slug, name, kind) VALUES
    ('demo-ebr',       'Colegio Demo',                  'ebr'),
    ('demo-eba',       'CEBA Demo',                     'eba'),
    ('demo-ebe',       'CEBE Demo',                     'ebe'),
    ('demo-cepre',     'Academia Pre Demo',             'cepre'),
    ('demo-institute', 'Instituto Tecnológico Demo',    'institute')
ON CONFLICT (slug) DO NOTHING;
