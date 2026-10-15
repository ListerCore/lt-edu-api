-- Aislamiento entre instituciones con Row Level Security.
--
-- El usuario de conexión (joseph) es superusuario y salta RLS. Por eso la API,
-- al abrir cada conexión del pool, debe ejecutar `SET ROLE joseph_app`: un rol
-- sin login ni privilegios especiales sobre el que sí se aplican las políticas.
-- Las migraciones siguen corriendo como joseph.
--
-- En cada transacción la API fija:
--   SET LOCAL app.institution_id = '<uuid>';   institución activa
--   SET LOCAL app.account_id     = '<uuid>';   cuenta autenticada (auditoría)
-- Sin app.institution_id, joseph_app no ve ninguna fila de institución.
--
-- Migraciones futuras que creen tablas deben terminar con
-- `SELECT util.apply_tenant_security();` (es idempotente).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'joseph_app') THEN
        CREATE ROLE joseph_app NOLOGIN;
    END IF;
END
$$;

-- El rol de conexión debe poder asumir joseph_app.
DO $$
BEGIN
    EXECUTE format('GRANT joseph_app TO %I', current_user);
END
$$;

-- Esquemas de datos de institución (acceso completo bajo RLS).
CREATE FUNCTION util.tenant_schemas() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $$
    SELECT ARRAY['tenant', 'core', 'iam', 'academic', 'enrollment', 'lms', 'attendance',
                 'assessment', 'wellbeing', 'comms', 'finance', 'ai', 'site']
$$;

-- Aplica permisos y políticas a todas las tablas. Reglas:
--   * tablas con institution_id → política institution_id = institución activa
--   * catálogos (curriculum, roles, permisos) → solo lectura
--   * bitácoras → solo lectura de la propia institución; activity_log admite INSERT
--   * ops → sin acceso (lo opera la plataforma)
CREATE FUNCTION util.apply_tenant_security() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    t record;
BEGIN
    EXECUTE 'GRANT USAGE ON SCHEMA util, audit, curriculum, '
        || array_to_string(util.tenant_schemas(), ', ') || ' TO joseph_app';

    EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA '
        || array_to_string(util.tenant_schemas(), ', ') || ' TO joseph_app';
    EXECUTE 'GRANT USAGE ON ALL SEQUENCES IN SCHEMA '
        || array_to_string(util.tenant_schemas(), ', ') || ', audit TO joseph_app';

    GRANT SELECT ON ALL TABLES IN SCHEMA curriculum TO joseph_app;
    GRANT SELECT ON ALL TABLES IN SCHEMA audit TO joseph_app;
    GRANT INSERT ON audit.activity_log TO joseph_app;

    -- Catálogos globales de IAM y finanzas: solo lectura.
    REVOKE INSERT, UPDATE, DELETE ON iam.role, iam.permission, iam.role_permission, finance.concept FROM joseph_app;
    -- Las instituciones las da de alta la plataforma.
    REVOKE INSERT, DELETE ON tenant.institution FROM joseph_app;

    FOR t IN
        SELECT c.table_schema, c.table_name
        FROM information_schema.columns c
        JOIN information_schema.tables tb
          ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
        WHERE c.column_name = 'institution_id'
          AND tb.table_type = 'BASE TABLE'
          AND c.table_schema = ANY (util.tenant_schemas() || ARRAY['audit'])
          -- Tablas de la cuenta, no de la institución: las filtra la API por account_id.
          AND (c.table_schema, c.table_name) NOT IN (('iam', 'session'), ('iam', 'login_event'), ('iam', 'membership'))
    LOOP
        EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', t.table_schema, t.table_name);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I.%I', t.table_schema, t.table_name);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I.%I TO joseph_app
             USING (institution_id = util.current_institution())
             WITH CHECK (institution_id = util.current_institution())',
            t.table_schema, t.table_name);
    END LOOP;

    -- La institución activa y aquellas donde la cuenta tiene membresía (para
    -- el selector de institución tras el login).
    ALTER TABLE tenant.institution ENABLE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON tenant.institution;
    CREATE POLICY tenant_isolation ON tenant.institution TO joseph_app
        USING (id = util.current_institution()
               OR id IN (SELECT m.institution_id FROM iam.membership m WHERE m.account_id = util.current_account()))
        WITH CHECK (id = util.current_institution());

    -- Una cuenta ve sus propias membresías en cualquier institución; solo
    -- gestiona las de la institución activa.
    ALTER TABLE iam.membership ENABLE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON iam.membership;
    CREATE POLICY tenant_isolation ON iam.membership TO joseph_app
        USING (institution_id = util.current_institution() OR account_id = util.current_account())
        WITH CHECK (institution_id = util.current_institution());

    -- Actividad previa a elegir institución (ej. login) se registra sin ella.
    DROP POLICY IF EXISTS activity_insert ON audit.activity_log;
    CREATE POLICY activity_insert ON audit.activity_log FOR INSERT TO joseph_app
        WITH CHECK (institution_id IS NULL OR institution_id = util.current_institution());
END
$$;

SELECT util.apply_tenant_security();
