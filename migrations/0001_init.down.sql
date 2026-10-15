DROP SCHEMA IF EXISTS iam;

DO $$
BEGIN
    EXECUTE format('ALTER DATABASE %I RESET timezone', current_database());
    EXECUTE format('ALTER DATABASE %I RESET client_encoding', current_database());
END
$$;
