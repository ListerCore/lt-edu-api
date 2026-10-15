-- Quita políticas y permisos de joseph_app en esta base. El rol es global al
-- clúster: solo se elimina si ninguna otra base lo usa.
DO $$
DECLARE
    p record;
BEGIN
    FOR p IN
        SELECT schemaname, tablename, policyname FROM pg_policies WHERE 'joseph_app' = ANY (roles)
    LOOP
        EXECUTE format('DROP POLICY %I ON %I.%I', p.policyname, p.schemaname, p.tablename);
        EXECUTE format('ALTER TABLE %I.%I DISABLE ROW LEVEL SECURITY', p.schemaname, p.tablename);
    END LOOP;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'joseph_app') THEN
        DROP OWNED BY joseph_app;
        BEGIN
            DROP ROLE joseph_app;
        EXCEPTION WHEN dependent_objects_still_exist THEN
            RAISE NOTICE 'joseph_app sigue en uso en otra base; no se elimina';
        END;
    END IF;
END
$$;

DROP FUNCTION IF EXISTS util.apply_tenant_security();
DROP FUNCTION IF EXISTS util.tenant_schemas();
