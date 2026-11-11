-- One demo institution per type, for the public demo (demo.listercore.es): visitors pick a type and a role and
-- browse sample data. ON CONFLICT keeps it safe on a database that already has them
INSERT INTO iam.institutions (slug, name, kind) VALUES
    ('demo-ebr',       'Joseph Colegio',   'ebr'),
    ('demo-eba',       'Joseph Escuela',   'eba'),
    ('demo-ebe',       'Joseph Especial',  'ebe'),
    ('demo-cepre',     'Joseph Academia',  'cepre'),
    ('demo-institute', 'Joseph Instituto', 'institute')
ON CONFLICT (slug) DO NOTHING;
