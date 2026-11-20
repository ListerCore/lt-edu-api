-- The platform is ListerCore Edu (it was called Joseph): the superadmin role says whose team runs it
UPDATE iam.roles
SET description = 'Gestiona instituciones, planes y módulos activos. Solo equipo de ListerCore Edu.'
WHERE code = 'superadmin' AND description = 'Gestiona instituciones, planes y módulos activos. Solo equipo de Joseph.';
