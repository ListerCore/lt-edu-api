-- Piezas compartidas por todos los dominios.
--
-- SIGE-JL es SaaS: muchas instituciones (CEBA, colegios EBR) en una sola base.
-- Cada tabla de una institución lleva institution_id y se aísla de dos formas:
--   1. Row Level Security (0012): la API fija la institución activa con
--      `SET LOCAL app.institution_id = '<uuid>'` y solo ve sus filas.
--   2. FKs compuestas (institution_id, x_id): una fila nunca puede apuntar a
--      datos de otra institución, aunque alguien conozca el uuid.
--
-- La API también fija `SET LOCAL app.account_id = '<uuid>'` para la auditoría.
--
-- Convenciones (ver docs/database.md):
--   * tablas en singular, columnas y códigos en inglés; el frontend traduce.
--   * estados como text + CHECK (no ENUM) para poder evolucionarlos.
--   * catálogos normativos (MINEDU) globales con código text como PK.

CREATE SCHEMA IF NOT EXISTS util;
CREATE SCHEMA IF NOT EXISTS audit;

CREATE FUNCTION util.set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;

CREATE FUNCTION util.current_account() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT nullif(current_setting('app.account_id', true), '')::uuid
$$;

CREATE FUNCTION util.current_institution() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT nullif(current_setting('app.institution_id', true), '')::uuid
$$;

-- Engancha set_updated_at a todas las tablas del esquema que tengan
-- updated_at; cada migración lo llama al final en vez de repetir triggers.
CREATE FUNCTION util.attach_updated_at(target_schema text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    t record;
BEGIN
    FOR t IN
        SELECT c.table_name
        FROM information_schema.columns c
        JOIN information_schema.tables tb
          ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
        WHERE c.table_schema = target_schema
          AND c.column_name = 'updated_at'
          AND tb.table_type = 'BASE TABLE'
    LOOP
        EXECUTE format(
            'CREATE OR REPLACE TRIGGER set_updated_at BEFORE UPDATE ON %I.%I
             FOR EACH ROW EXECUTE FUNCTION util.set_updated_at()',
            target_schema, t.table_name);
    END LOOP;
END
$$;

CREATE TABLE audit.change_log (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Null en tablas globales (catálogos, permisos por defecto).
    institution_id uuid,
    table_name     text NOT NULL,
    operation      text NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
    row_id         uuid,
    old_data       jsonb,
    new_data       jsonb,
    -- Sin FK: la bitácora debe sobrevivir a la baja de la cuenta o institución.
    actor_id       uuid,
    changed_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX change_log_row_idx ON audit.change_log (table_name, row_id, changed_at DESC);
CREATE INDEX change_log_institution_idx ON audit.change_log (institution_id, changed_at DESC);
CREATE INDEX change_log_actor_idx ON audit.change_log (actor_id, changed_at DESC);

-- Registra la fila completa antes/después. Ignora UPDATE que solo tocan
-- updated_at. SECURITY DEFINER: el rol de la API no puede escribir la
-- bitácora directamente, solo a través de este trigger.
CREATE FUNCTION audit.log_change() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    old_row jsonb;
    new_row jsonb;
    any_row jsonb;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_row := to_jsonb(OLD);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_row := to_jsonb(NEW);
    END IF;

    IF TG_OP = 'UPDATE' AND (old_row - 'updated_at') = (new_row - 'updated_at') THEN
        RETURN NEW;
    END IF;

    any_row := coalesce(new_row, old_row);

    INSERT INTO audit.change_log (institution_id, table_name, operation, row_id, old_data, new_data, actor_id)
    VALUES (
        (any_row ->> 'institution_id')::uuid,
        TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME,
        TG_OP,
        (any_row ->> 'id')::uuid,
        old_row,
        new_row,
        nullif(current_setting('app.account_id', true), '')::uuid
    );

    RETURN coalesce(NEW, OLD);
END
$$;

-- Activa la bitácora en una tabla. `events` permite, por ejemplo, auditar
-- solo UPDATE/DELETE en mensajes sin duplicar cada INSERT.
CREATE FUNCTION audit.attach(target regclass, events text DEFAULT 'INSERT OR UPDATE OR DELETE') RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    EXECUTE format(
        'CREATE OR REPLACE TRIGGER audit_change AFTER %s ON %s
         FOR EACH ROW EXECUTE FUNCTION audit.log_change()',
        events, target);
END
$$;

-- Actividad del usuario (perfil → Actividad, settings → auditoría).
-- La escribe la API; institution_id es null para acciones fuera de una
-- institución (ej. ingreso antes de elegirla).
CREATE TABLE audit.activity_log (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    institution_id uuid,
    account_id     uuid,
    action         text NOT NULL,
    entity         text,
    entity_id      uuid,
    detail         jsonb NOT NULL DEFAULT '{}',
    ip             inet,
    user_agent     text,
    occurred_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_log_account_idx ON audit.activity_log (account_id, occurred_at DESC);
CREATE INDEX activity_log_institution_idx ON audit.activity_log (institution_id, occurred_at DESC);
