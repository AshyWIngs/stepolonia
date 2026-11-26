-- +goose Up
CREATE TABLE public.content_types (
    id uuid CONSTRAINT content_types_pkey PRIMARY KEY,
    key text COLLATE "C" NOT NULL CONSTRAINT content_types_key_key UNIQUE,
    route_namespace text COLLATE "C" NOT NULL
    CONSTRAINT content_types_namespace_key UNIQUE,
    kind text NOT NULL DEFAULT 'collection',
    display_name text NOT NULL DEFAULT '',
    icon_svg text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    current_schema_no bigint NOT NULL DEFAULT 1,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT content_types_key_check CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT content_types_namespace_check CHECK (
        route_namespace ~ '^[a-z][a-z0-9-]{0,63}$'
    ),
    CONSTRAINT content_types_kind_check CHECK (kind = 'collection'),
    CONSTRAINT content_types_display_name_check CHECK (
        char_length(display_name) <= 120
    ),
    CONSTRAINT content_types_icon_svg_size_check CHECK (
        octet_length(icon_svg) <= 4096
    ),
    CONSTRAINT content_types_version_check CHECK (
        version > 0 AND current_schema_no > 0
    )
);

CREATE TABLE public.content_schemas (
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    sealed boolean NOT NULL DEFAULT false,
    CONSTRAINT content_schemas_pkey PRIMARY KEY (type_id, schema_no),
    CONSTRAINT content_schemas_type_fkey FOREIGN KEY (
        type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT content_schemas_number_check CHECK (schema_no > 0)
);

ALTER TABLE public.content_types
ADD CONSTRAINT content_types_schema_fkey FOREIGN KEY (
    id, current_schema_no
)
REFERENCES public.content_schemas (
    type_id, schema_no
) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE public.content_fields (
    type_id uuid NOT NULL,
    field_id uuid NOT NULL,
    key text COLLATE "C" NOT NULL,
    display_labels jsonb NOT NULL DEFAULT '{}',
    CONSTRAINT content_fields_pkey PRIMARY KEY (type_id, field_id),
    CONSTRAINT content_fields_type_fkey FOREIGN KEY (
        type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT content_fields_type_key UNIQUE (type_id, key),
    CONSTRAINT content_fields_key_check CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT content_fields_labels_check CHECK (
        jsonb_typeof(display_labels) = 'object'
        AND octet_length(display_labels::text) <= 8192
    )
);

CREATE TABLE public.content_field_definitions (
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    field_id uuid NOT NULL,
    kind text NOT NULL,
    scope text NOT NULL,
    visibility text NOT NULL,
    required boolean NOT NULL DEFAULT false,
    target_type_id uuid,
    config jsonb NOT NULL DEFAULT '{}',
    CONSTRAINT content_field_definitions_pkey PRIMARY KEY (
        type_id, schema_no, field_id
    ),
    CONSTRAINT content_field_definitions_schema_fkey FOREIGN KEY (
        type_id, schema_no
    ) REFERENCES public.content_schemas (type_id, schema_no) ON DELETE RESTRICT,
    CONSTRAINT content_field_definitions_field_fkey FOREIGN KEY (
        type_id, field_id
    ) REFERENCES public.content_fields (type_id, field_id) ON DELETE RESTRICT,
    CONSTRAINT content_field_definitions_target_fkey FOREIGN KEY (
        target_type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT content_field_definitions_kind_check CHECK (
        kind IN (
            'string',
            'text',
            'markdown',
            'boolean',
            'integer',
            'decimal',
            'datetime',
            'enum',
            'relation'
        )
    ),
    CONSTRAINT content_field_definitions_scope_check CHECK (
        scope IN ('shared', 'localized')
    ),
    CONSTRAINT content_field_definitions_visibility_check CHECK (
        visibility IN ('public', 'private')
    ),
    CONSTRAINT content_field_definitions_target_check CHECK (
        (kind = 'relation' AND target_type_id IS NOT null AND NOT required)
        OR (kind <> 'relation' AND target_type_id IS null)
    ),
    CONSTRAINT content_field_definitions_config_check CHECK (
        jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 8192
    )
);

CREATE INDEX content_field_definitions_field_idx
ON public.content_field_definitions (
    type_id, field_id
);

CREATE INDEX content_field_definitions_target_idx
ON public.content_field_definitions (
    target_type_id
);

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_schema() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.sealed) THEN
        RAISE EXCEPTION 'sealed schema is immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.type_id, NEW.schema_no) IS DISTINCT FROM (OLD.type_id, OLD.schema_no) THEN
        RAISE EXCEPTION 'schema identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.require_content_schema_sealed() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.content_schemas s WHERE s.type_id=NEW.type_id AND s.schema_no=NEW.schema_no AND s.sealed) THEN
        RAISE EXCEPTION 'schema must be sealed before commit' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_definition() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
DECLARE
    closed boolean;
    owner_type uuid;
    owner_schema bigint;
BEGIN
    owner_type := CASE WHEN TG_OP='DELETE' THEN OLD.type_id ELSE NEW.type_id END;
    owner_schema := CASE WHEN TG_OP='DELETE' THEN OLD.schema_no ELSE NEW.schema_no END;
    IF TG_OP='UPDATE' AND (NEW.type_id,NEW.schema_no,NEW.field_id) IS DISTINCT FROM (OLD.type_id,OLD.schema_no,OLD.field_id) THEN
        RAISE EXCEPTION 'definition identity is immutable' USING ERRCODE = '23514';
    END IF;
    SELECT sealed INTO closed FROM public.content_schemas WHERE type_id=owner_type AND schema_no=owner_schema FOR UPDATE;
    IF closed THEN
        RAISE EXCEPTION 'sealed schema children are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'content identities cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'content_types' THEN
        IF (NEW.id,NEW.key,NEW.route_namespace,NEW.kind) IS DISTINCT FROM (OLD.id,OLD.key,OLD.route_namespace,OLD.kind) THEN
            RAISE EXCEPTION 'type identity is immutable' USING ERRCODE = '23514';
        END IF;
    ELSIF TG_TABLE_NAME = 'content_fields' THEN
        IF (NEW.type_id,NEW.field_id,NEW.key) IS DISTINCT FROM (OLD.type_id,OLD.field_id,OLD.key) THEN
            RAISE EXCEPTION 'field identity is immutable' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

CREATE TRIGGER content_schemas_guard
BEFORE UPDATE OR DELETE
ON public.content_schemas
FOR EACH ROW EXECUTE FUNCTION public.guard_content_schema();

CREATE CONSTRAINT TRIGGER content_schemas_require_seal
AFTER INSERT OR UPDATE
ON public.content_schemas
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.require_content_schema_sealed();

CREATE TRIGGER content_definitions_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_field_definitions
FOR EACH ROW EXECUTE FUNCTION public.guard_content_definition();

CREATE TRIGGER content_types_identity
BEFORE UPDATE OR DELETE
ON public.content_types
FOR EACH ROW EXECUTE FUNCTION public.guard_content_identity();

CREATE TRIGGER content_fields_identity
BEFORE UPDATE OR DELETE
ON public.content_fields
FOR EACH ROW EXECUTE FUNCTION public.guard_content_identity();

COMMENT ON TABLE public.content_types IS
'Stable Collection identities and the current immutable semantic schema.';

COMMENT ON COLUMN public.content_types.display_name IS
'Collection name. An empty name uses the translated built-in label.';

COMMENT ON COLUMN public.content_types.icon_svg IS
'Static SVG for administrative navigation. Empty uses the default icon.';

COMMENT ON COLUMN public.content_types.enabled IS
'Whether authoring is available. Existing public snapshots are unaffected.';

COMMENT ON TABLE public.content_schemas IS
'Semantic versions assembled and sealed atomically before commit.';

COMMENT ON TABLE public.content_fields IS
'Immutable field identities with independently editable display labels.';

COMMENT ON COLUMN public.content_fields.display_labels IS
'Bounded locale label/help map; display edits never change semantic schema.';

COMMENT ON TABLE public.content_field_definitions IS
'Pinned scalar or optional relation definitions; executable options are '
'prohibited.';

CREATE TRIGGER content_types_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_types
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER content_schemas_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_schemas
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER content_fields_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_fields
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER content_field_definitions_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_field_definitions
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

REVOKE ALL ON public.content_types,
public.content_schemas,
public.content_fields,
public.content_field_definitions FROM public, sp_language_importer;

REVOKE ALL ON FUNCTION public.guard_content_schema(),
public.require_content_schema_sealed(),
public.guard_content_definition(),
public.guard_content_identity() FROM public;

GRANT SELECT, INSERT ON public.content_types,
public.content_schemas,
public.content_fields,
public.content_field_definitions TO sp_publisher;

GRANT UPDATE (
    current_schema_no, version, enabled, icon_svg
) ON public.content_types TO sp_publisher;

GRANT UPDATE (sealed) ON public.content_schemas TO sp_publisher;

GRANT UPDATE (display_labels) ON public.content_fields TO sp_publisher;

-- +goose Down
ALTER TABLE public.content_types DROP CONSTRAINT content_types_schema_fkey;
DROP TABLE public.content_field_definitions;
DROP TABLE public.content_fields;
DROP TABLE public.content_schemas;
DROP TABLE public.content_types;
DROP FUNCTION public.guard_content_identity();
DROP FUNCTION public.guard_content_definition();
DROP FUNCTION public.require_content_schema_sealed();
DROP FUNCTION public.guard_content_schema();
