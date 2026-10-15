-- Base del proyecto: UTF-8 (tildes y ñ), zona horaria America/Lima y uuidv7.
--
-- El encoding se fija al crear la base (POSTGRES_INITDB_ARGS en compose.yml);
-- acá solo se valida para fallar temprano si la base no lo cumple.
-- uuidv7() es nativa desde PostgreSQL 18: las tablas usan
-- `id uuid PRIMARY KEY DEFAULT uuidv7()`.
DO $$
BEGIN
    IF (SELECT pg_encoding_to_char(encoding) FROM pg_database WHERE datname = current_database()) <> 'UTF8' THEN
        RAISE EXCEPTION 'la base % debe usar encoding UTF8', current_database();
    END IF;

    IF current_setting('server_version_num')::int < 180000 THEN
        RAISE EXCEPTION 'se requiere PostgreSQL 18+ para uuidv7() (actual: %)', current_setting('server_version');
    END IF;

    EXECUTE format('ALTER DATABASE %I SET timezone TO %L', current_database(), 'America/Lima');
    EXECUTE format('ALTER DATABASE %I SET client_encoding TO %L', current_database(), 'UTF8');
END
$$;

-- ALTER DATABASE aplica a sesiones nuevas; esta también lo necesita.
SET timezone TO 'America/Lima';

CREATE SCHEMA IF NOT EXISTS iam;
