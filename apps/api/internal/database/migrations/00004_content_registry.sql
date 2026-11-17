-- +goose Up
CREATE TABLE public.content_types (
    id uuid CONSTRAINT content_types_pkey PRIMARY KEY,
    key text COLLATE "C" NOT NULL CONSTRAINT content_types_key_key UNIQUE,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT content_types_key_check CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT content_types_version_check CHECK (version > 0)
);

CREATE TABLE public.content_fields (
    type_id uuid NOT NULL,
    field_id uuid NOT NULL,
    key text COLLATE "C" NOT NULL,
    CONSTRAINT content_fields_pkey PRIMARY KEY (type_id, field_id),
    CONSTRAINT content_fields_type_fkey FOREIGN KEY (
        type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT content_fields_type_key UNIQUE (type_id, key),
    CONSTRAINT content_fields_key_check CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$')
);

COMMENT ON TABLE public.content_types IS
'Stable Registry type identities only; semantic schemas and commands '
'are not implemented.';

COMMENT ON TABLE public.content_fields IS
'Stable field identities owned by one content type; no executable '
'schema or values.';

-- +goose StatementBegin
CREATE FUNCTION public.cms_write_mutex() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    PERFORM pg_catalog.pg_advisory_xact_lock(1398031696, 1);
    RETURN NULL;
END;
$body$;
-- +goose StatementEnd

COMMENT ON FUNCTION public.cms_write_mutex() IS
'Acquire the installation CMS transaction lock before statement row '
'locks.';

REVOKE ALL ON FUNCTION public.cms_write_mutex() FROM public;

CREATE TRIGGER content_types_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.content_types
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_fields_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.content_fields
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

REVOKE ALL ON public.content_types,
public.content_fields FROM public,
sp_publisher,
sp_language_importer;

-- +goose Down
DROP TABLE public.content_fields;
DROP TABLE public.content_types;
DROP FUNCTION public.cms_write_mutex();
