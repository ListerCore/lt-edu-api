-- El esquema iam lo crea 0001_init: acá solo se borran sus objetos.
DROP VIEW IF EXISTS iam.membership_permission;

DROP TABLE IF EXISTS
    iam.login_event,
    iam.mfa_recovery_code,
    iam.account_token,
    iam.session,
    iam.membership_role,
    iam.membership,
    iam.account,
    iam.institution_role_permission,
    iam.role_permission,
    iam.permission,
    iam.role;

DROP SCHEMA IF EXISTS core CASCADE;
DROP SCHEMA IF EXISTS tenant CASCADE;
