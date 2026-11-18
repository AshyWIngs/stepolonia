-- +goose Up
CREATE TABLE public.content_items (
    id uuid CONSTRAINT content_items_pkey PRIMARY KEY,
    type_id uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    published_id uuid,
    CONSTRAINT content_items_type_key UNIQUE (type_id, id),
    CONSTRAINT content_items_type_fkey FOREIGN KEY (
        type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT content_items_version_check CHECK (version > 0)
);

CREATE TABLE public.content_drafts (
    item_id uuid CONSTRAINT content_drafts_pkey PRIMARY KEY,
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    shared_values jsonb NOT NULL DEFAULT '{}',
    shared_seq bigint NOT NULL DEFAULT 1,
    shared_ready_seq bigint,
    CONSTRAINT content_drafts_item_fkey FOREIGN KEY (
        type_id, item_id
    ) REFERENCES public.content_items (type_id, id) ON DELETE RESTRICT,
    CONSTRAINT content_drafts_schema_fkey FOREIGN KEY (
        type_id, schema_no
    ) REFERENCES public.content_schemas (type_id, schema_no) ON DELETE RESTRICT,
    CONSTRAINT content_drafts_owner_key UNIQUE (item_id, type_id, schema_no),
    CONSTRAINT content_drafts_shared_check CHECK (
        jsonb_typeof(shared_values) = 'object'
        AND octet_length(shared_values::text) <= 262144
    ),
    CONSTRAINT content_drafts_sequence_check CHECK (
        shared_seq > 0
        AND (shared_ready_seq IS null OR shared_ready_seq = shared_seq)
    )
);

CREATE INDEX content_drafts_schema_idx ON public.content_drafts (
    type_id, schema_no
);

CREATE TABLE public.content_draft_locales (
    item_id uuid NOT NULL,
    locale_code text COLLATE "C" NOT NULL,
    scalar_values jsonb NOT NULL DEFAULT '{}',
    local_seq bigint NOT NULL DEFAULT 1,
    ready_local_seq bigint,
    ready_shared_seq bigint,
    ready_schema_no bigint,
    ready_locale_generation bigint,
    CONSTRAINT content_draft_locales_pkey PRIMARY KEY (item_id, locale_code),
    CONSTRAINT content_draft_locales_item_fkey FOREIGN KEY (
        item_id
    ) REFERENCES public.content_drafts (item_id) ON DELETE RESTRICT,
    CONSTRAINT content_draft_locales_locale_fkey FOREIGN KEY (
        locale_code
    ) REFERENCES public.languages (code) ON DELETE RESTRICT,
    CONSTRAINT content_draft_locales_values_check CHECK (
        jsonb_typeof(scalar_values) = 'object'
        AND octet_length(scalar_values::text) <= 262144
    ),
    CONSTRAINT content_draft_locales_sequence_check CHECK (local_seq > 0),
    CONSTRAINT content_draft_locales_ready_check CHECK (
        (
            ready_local_seq IS null
            AND ready_shared_seq IS null
            AND ready_schema_no IS null
            AND ready_locale_generation IS null
        )
        OR (
            ready_local_seq IS NOT null
            AND ready_shared_seq IS NOT null
            AND ready_schema_no IS NOT null
            AND ready_locale_generation IS NOT null
            AND ready_local_seq = local_seq
            AND ready_shared_seq > 0
            AND ready_schema_no > 0
            AND ready_locale_generation > 0
        )
    )
);

CREATE INDEX content_draft_locales_locale_idx ON public.content_draft_locales (
    locale_code
);

CREATE TABLE public.content_publications (
    id uuid CONSTRAINT content_publications_pkey PRIMARY KEY,
    item_id uuid NOT NULL,
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    revision_no bigint NOT NULL,
    sealed boolean NOT NULL DEFAULT false,
    shared_seq bigint NOT NULL,
    shared_values jsonb NOT NULL DEFAULT '{}',
    display_metadata jsonb NOT NULL DEFAULT '{}',
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT content_publications_item_key UNIQUE (item_id, id),
    CONSTRAINT content_publications_revision_key UNIQUE (item_id, revision_no),
    CONSTRAINT content_publications_owner_key UNIQUE (
        id, item_id, type_id, schema_no
    ),
    CONSTRAINT content_publications_item_fkey FOREIGN KEY (
        type_id, item_id
    ) REFERENCES public.content_items (type_id, id) ON DELETE RESTRICT,
    CONSTRAINT content_publications_schema_fkey FOREIGN KEY (
        type_id, schema_no
    ) REFERENCES public.content_schemas (type_id, schema_no) ON DELETE RESTRICT,
    CONSTRAINT content_publications_sequence_check CHECK (
        revision_no > 0 AND shared_seq > 0
    ),
    CONSTRAINT content_publications_values_check CHECK (
        jsonb_typeof(shared_values) = 'object'
        AND octet_length(shared_values::text) <= 262144
    ),
    CONSTRAINT content_publications_display_check CHECK (
        jsonb_typeof(display_metadata) = 'object'
        AND octet_length(display_metadata::text) <= 1048576
    )
);

CREATE INDEX content_publications_order_idx ON public.content_publications (
    type_id, published_at DESC, item_id DESC, id
);

CREATE INDEX content_publications_item_idx
ON public.content_publications (type_id, item_id);

CREATE INDEX content_publications_schema_idx ON public.content_publications (
    type_id, schema_no
);

ALTER TABLE public.content_items
ADD CONSTRAINT content_items_publication_fkey FOREIGN KEY (
    id, published_id
)
REFERENCES public.content_publications (item_id, id) ON DELETE RESTRICT;

CREATE INDEX content_items_publication_idx ON public.content_items (
    id, published_id
);

CREATE TABLE public.content_routes (
    namespace text COLLATE "C" NOT NULL,
    locale_code text COLLATE "C" NOT NULL,
    slug text COLLATE "C" NOT NULL,
    type_id uuid NOT NULL,
    item_id uuid NOT NULL,
    CONSTRAINT content_routes_pkey PRIMARY KEY (namespace, locale_code, slug),
    CONSTRAINT content_routes_owner_key UNIQUE (item_id, locale_code, slug),
    CONSTRAINT content_routes_item_fkey FOREIGN KEY (
        type_id, item_id
    ) REFERENCES public.content_items (type_id, id) ON DELETE RESTRICT,
    CONSTRAINT content_routes_locale_fkey FOREIGN KEY (
        locale_code
    ) REFERENCES public.languages (code) ON DELETE RESTRICT,
    CONSTRAINT content_routes_slug_check CHECK (
        length(slug) BETWEEN 1 AND 200 AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    )
);

CREATE INDEX content_routes_item_idx ON public.content_routes (
    type_id, item_id
);

CREATE INDEX content_routes_locale_idx ON public.content_routes (locale_code);

CREATE TABLE public.content_publication_locales (
    publication_id uuid NOT NULL,
    item_id uuid NOT NULL,
    locale_code text COLLATE "C" NOT NULL,
    scalar_values jsonb NOT NULL DEFAULT '{}',
    locale_generation bigint NOT NULL,
    slug text COLLATE "C" NOT NULL,
    CONSTRAINT content_publication_locales_pkey PRIMARY KEY (
        publication_id, locale_code
    ),
    CONSTRAINT content_publication_locales_item_fkey FOREIGN KEY (
        item_id, publication_id
    ) REFERENCES public.content_publications (item_id, id) ON DELETE RESTRICT,
    CONSTRAINT content_publication_locales_locale_fkey FOREIGN KEY (
        locale_code
    ) REFERENCES public.languages (code) ON DELETE RESTRICT,
    CONSTRAINT content_publication_locales_route_fkey FOREIGN KEY (
        item_id, locale_code, slug
    ) REFERENCES public.content_routes (
        item_id, locale_code, slug
    ) ON DELETE RESTRICT,
    CONSTRAINT content_publication_locales_values_check CHECK (
        jsonb_typeof(scalar_values) = 'object'
        AND octet_length(scalar_values::text) <= 262144
    ),
    CONSTRAINT content_publication_locales_generation_check CHECK (
        locale_generation > 0
    ),
    CONSTRAINT content_publication_locales_slug_check CHECK (
        length(slug) BETWEEN 1 AND 200 AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    )
);

CREATE INDEX content_publication_locales_item_idx
ON public.content_publication_locales (
    item_id, publication_id
);

CREATE INDEX content_publication_locales_route_idx
ON public.content_publication_locales (item_id, locale_code, slug);

CREATE INDEX content_publication_locales_locale_idx
ON public.content_publication_locales (
    locale_code
);

CREATE TABLE public.content_draft_relations (
    item_id uuid NOT NULL,
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    field_id uuid NOT NULL,
    locale_scope text COLLATE "C" NOT NULL DEFAULT '',
    target_type_id uuid NOT NULL,
    target_item_id uuid NOT NULL,
    CONSTRAINT content_draft_relations_pkey PRIMARY KEY (
        item_id, field_id, locale_scope
    ),
    CONSTRAINT content_draft_relations_source_fkey FOREIGN KEY (
        item_id, type_id, schema_no
    ) REFERENCES public.content_drafts (
        item_id, type_id, schema_no
    ) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT content_draft_relations_definition_fkey FOREIGN KEY (
        type_id, schema_no, field_id
    ) REFERENCES public.content_field_definitions (
        type_id, schema_no, field_id
    ) ON DELETE RESTRICT,
    CONSTRAINT content_draft_relations_target_fkey FOREIGN KEY (
        target_type_id, target_item_id
    ) REFERENCES public.content_items (type_id, id) ON DELETE RESTRICT
);

CREATE INDEX content_draft_relations_target_idx
ON public.content_draft_relations (
    target_type_id, target_item_id
);

CREATE INDEX content_draft_relations_definition_idx
ON public.content_draft_relations (
    type_id, schema_no, field_id
);

CREATE TABLE public.content_publication_relations (
    publication_id uuid NOT NULL,
    item_id uuid NOT NULL,
    type_id uuid NOT NULL,
    schema_no bigint NOT NULL,
    field_id uuid NOT NULL,
    locale_scope text COLLATE "C" NOT NULL DEFAULT '',
    target_type_id uuid NOT NULL,
    target_item_id uuid NOT NULL,
    CONSTRAINT content_publication_relations_pkey PRIMARY KEY (
        publication_id, field_id, locale_scope
    ),
    CONSTRAINT content_publication_relations_source_fkey FOREIGN KEY (
        publication_id, item_id, type_id, schema_no
    ) REFERENCES public.content_publications (
        id, item_id, type_id, schema_no
    ) ON DELETE RESTRICT,
    CONSTRAINT content_publication_relations_definition_fkey FOREIGN KEY (
        type_id, schema_no, field_id
    ) REFERENCES public.content_field_definitions (
        type_id, schema_no, field_id
    ) ON DELETE RESTRICT,
    CONSTRAINT content_publication_relations_target_fkey FOREIGN KEY (
        target_type_id, target_item_id
    ) REFERENCES public.content_items (type_id, id) ON DELETE RESTRICT
);

CREATE INDEX content_publication_relations_target_idx
ON public.content_publication_relations (
    target_type_id, target_item_id
);

CREATE INDEX content_publication_relations_definition_idx
ON public.content_publication_relations (
    type_id, schema_no, field_id
);

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_item_identity() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'content identities cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF TG_OP <> 'INSERT' THEN
        IF (NEW.id,NEW.type_id) IS DISTINCT FROM (OLD.id,OLD.type_id) THEN
            RAISE EXCEPTION 'item identity is immutable' USING ERRCODE = '23514';
        END IF;
        -- Version-only commands have no access to publication data.
        IF NEW.published_id IS NOT DISTINCT FROM OLD.published_id THEN
            RETURN NEW;
        END IF;
    END IF;
    IF NEW.published_id IS NOT NULL THEN
        IF NOT EXISTS (SELECT 1 FROM public.content_publications p WHERE p.id=NEW.published_id AND p.item_id=NEW.id AND p.sealed) THEN
            RAISE EXCEPTION 'publication pointer requires sealed own revision' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_publication() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.sealed) THEN
        RAISE EXCEPTION 'publication is immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.id,NEW.item_id,NEW.type_id,NEW.schema_no,NEW.revision_no,NEW.shared_seq,NEW.shared_values,NEW.display_metadata,NEW.published_at)
        IS DISTINCT FROM (OLD.id,OLD.item_id,OLD.type_id,OLD.schema_no,OLD.revision_no,OLD.shared_seq,OLD.shared_values,OLD.display_metadata,OLD.published_at) THEN
        RAISE EXCEPTION 'only publication seal can change' USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.content_schemas s WHERE s.type_id=NEW.type_id AND s.schema_no=NEW.schema_no AND s.sealed) THEN
        RAISE EXCEPTION 'publication requires sealed schema' USING ERRCODE = '23514';
    END IF;
    IF NEW.sealed AND NOT EXISTS (SELECT 1 FROM public.content_publication_locales l WHERE l.publication_id=NEW.id) THEN
        RAISE EXCEPTION 'publication requires at least one locale' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.require_content_publication_sealed() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.content_publications p WHERE p.id=NEW.id AND p.sealed) THEN
        RAISE EXCEPTION 'publication must be sealed before commit' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_publication_child() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
DECLARE
    owner_id uuid;
    closed boolean;
BEGIN
    owner_id := CASE WHEN TG_OP='DELETE' THEN OLD.publication_id ELSE NEW.publication_id END;
    IF TG_OP='UPDATE' AND NEW.publication_id IS DISTINCT FROM OLD.publication_id THEN
        RAISE EXCEPTION 'publication child owner is immutable' USING ERRCODE = '23514';
    END IF;
    SELECT sealed INTO closed FROM public.content_publications WHERE id=owner_id FOR UPDATE;
    IF closed THEN
        RAISE EXCEPTION 'sealed publication children are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_route() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'route reservations are permanent' USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.content_types t WHERE t.id=NEW.type_id AND t.route_namespace=NEW.namespace) THEN
        RAISE EXCEPTION 'route namespace belongs to another type' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_relation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
DECLARE
    definition public.content_field_definitions%ROWTYPE;
BEGIN
    SELECT * INTO definition FROM public.content_field_definitions
        WHERE type_id=NEW.type_id AND schema_no=NEW.schema_no AND field_id=NEW.field_id;
    IF NOT FOUND OR definition.kind <> 'relation' OR definition.target_type_id IS DISTINCT FROM NEW.target_type_id
        OR (definition.scope='shared' AND NEW.locale_scope <> '')
        OR (definition.scope='localized' AND NEW.locale_scope = '') THEN
        RAISE EXCEPTION 'relation definition or scope mismatch' USING ERRCODE = '23514';
    END IF;
    IF NEW.locale_scope <> '' THEN
        IF TG_TABLE_NAME='content_draft_relations' THEN
            IF NOT EXISTS (SELECT 1 FROM public.content_draft_locales l WHERE l.item_id=NEW.item_id AND l.locale_code=NEW.locale_scope) THEN
                RAISE EXCEPTION 'localized relation needs own draft locale' USING ERRCODE = '23514';
            END IF;
        ELSE
            IF NOT EXISTS (SELECT 1 FROM public.content_publication_locales l WHERE l.publication_id=NEW.publication_id AND l.item_id=NEW.item_id AND l.locale_code=NEW.locale_scope) THEN
                RAISE EXCEPTION 'localized relation needs own publication locale' USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_content_locale_relation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP='UPDATE' AND (NEW.item_id,NEW.locale_code) IS NOT DISTINCT FROM (OLD.item_id,OLD.locale_code) THEN
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME='content_draft_locales' THEN
        IF EXISTS (SELECT 1 FROM public.content_draft_relations r WHERE r.item_id=OLD.item_id AND r.locale_scope=OLD.locale_code) THEN
            RAISE EXCEPTION 'draft locale_code has dependent relations' USING ERRCODE = '23503';
        END IF;
    ELSE
        IF EXISTS (SELECT 1 FROM public.content_publication_relations r WHERE r.publication_id=OLD.publication_id AND r.locale_scope=OLD.locale_code) THEN
            RAISE EXCEPTION 'publication locale_code has dependent relations' USING ERRCODE = '23503';
        END IF;
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

CREATE TRIGGER content_items_identity
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_items
FOR EACH ROW EXECUTE FUNCTION public.guard_content_item_identity();

CREATE TRIGGER content_publications_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_publications
FOR EACH ROW EXECUTE FUNCTION public.guard_content_publication();

CREATE CONSTRAINT TRIGGER content_publications_require_seal
AFTER INSERT OR UPDATE
ON public.content_publications
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.require_content_publication_sealed();

CREATE TRIGGER content_publication_locales_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_publication_locales
FOR EACH ROW EXECUTE FUNCTION public.guard_content_publication_child();

CREATE TRIGGER content_publication_relations_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_publication_relations
FOR EACH ROW EXECUTE FUNCTION public.guard_content_publication_child();

CREATE TRIGGER content_routes_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.content_routes
FOR EACH ROW EXECUTE FUNCTION public.guard_content_route();

CREATE TRIGGER content_draft_relations_definition
BEFORE INSERT OR UPDATE
ON public.content_draft_relations
FOR EACH ROW EXECUTE FUNCTION public.guard_content_relation();

CREATE TRIGGER content_publication_relations_definition
BEFORE INSERT OR UPDATE
ON public.content_publication_relations
FOR EACH ROW EXECUTE FUNCTION public.guard_content_relation();

CREATE TRIGGER content_draft_locales_relations
BEFORE UPDATE OR DELETE
ON public.content_draft_locales
FOR EACH ROW EXECUTE FUNCTION public.guard_content_locale_relation();

CREATE TRIGGER content_publication_locales_relations
BEFORE UPDATE OR DELETE
ON public.content_publication_locales
FOR EACH ROW EXECUTE FUNCTION public.guard_content_locale_relation();

CREATE VIEW public.public_content_types WITH (security_barrier = true) AS
SELECT
    key,
    route_namespace
FROM public.content_types;

CREATE VIEW public.public_content_items WITH (security_barrier = true) AS
SELECT
    i.id AS item_id,
    t.key AS type_key,
    t.route_namespace,
    p.schema_no,
    p.id AS publication_id,
    l.locale_code AS language_code,
    l.slug,
    p.published_at,
    coalesce((
        SELECT jsonb_object_agg(f.key, p.shared_values -> f.key)
        FROM public.content_field_definitions AS d
        INNER JOIN
            public.content_fields AS f
            ON d.type_id = f.type_id AND d.field_id = f.field_id
        WHERE
            d.type_id = p.type_id AND d.schema_no = p.schema_no
            AND d.scope = 'shared'
            AND d.visibility = 'public'
            AND d.kind <> 'relation'
            AND p.shared_values ? f.key
    ), '{}'::jsonb) AS shared_values,
    coalesce((
        SELECT jsonb_object_agg(f.key, l.scalar_values -> f.key)
        FROM public.content_field_definitions AS d
        INNER JOIN
            public.content_fields AS f
            ON d.type_id = f.type_id AND d.field_id = f.field_id
        WHERE
            d.type_id = p.type_id AND d.schema_no = p.schema_no
            AND d.scope = 'localized'
            AND d.visibility = 'public'
            AND d.kind <> 'relation'
            AND l.scalar_values ? f.key
    ), '{}'::jsonb) AS localized_values,
    coalesce((
        SELECT jsonb_object_agg(f.key, p.display_metadata -> f.key)
        FROM public.content_field_definitions AS d
        INNER JOIN
            public.content_fields AS f
            ON d.type_id = f.type_id AND d.field_id = f.field_id
        WHERE
            d.type_id = p.type_id AND d.schema_no = p.schema_no
            AND d.visibility = 'public' AND p.display_metadata ? f.key
    ), '{}'::jsonb) AS display_metadata,
    coalesce((
        SELECT
            jsonb_object_agg(
                f.key,
                jsonb_build_object(
                    'kind',
                    d.kind,
                    'scope',
                    d.scope,
                    'config',
                    d.config,
                    'required',
                    d.required
                )
            )
        FROM public.content_field_definitions AS d
        INNER JOIN
            public.content_fields AS f
            ON d.type_id = f.type_id AND d.field_id = f.field_id
        WHERE
            d.type_id = p.type_id
            AND d.schema_no = p.schema_no
            AND d.visibility = 'public'
    ), '{}'::jsonb) AS field_metadata
FROM public.content_items AS i
INNER JOIN public.content_types AS t ON i.type_id = t.id
INNER JOIN
    public.content_publications AS p
    ON i.published_id = p.id AND i.id = p.item_id AND p.sealed
INNER JOIN public.content_publication_locales AS l ON p.id = l.publication_id
INNER JOIN
    public.languages AS lang
    ON
        l.locale_code = lang.code
        AND lang.is_enabled
        AND l.locale_generation = lang.generation;

CREATE VIEW public.public_content_routes WITH (security_barrier = true) AS
SELECT
    r.namespace,
    r.locale_code AS language_code,
    r.slug AS requested_slug,
    r.item_id,
    p.type_key,
    p.slug AS canonical_slug,
    p.publication_id
FROM public.content_routes AS r
INNER JOIN
    public.public_content_items AS p
    ON r.item_id = p.item_id AND r.locale_code = p.language_code;

CREATE VIEW public.public_content_relations WITH (security_barrier = true) AS
SELECT
    visible_source.publication_id,
    visible_source.language_code,
    source_field.key AS field_key,
    visible_target.item_id AS target_item_id,
    visible_target.type_key AS target_type_key,
    visible_target.route_namespace AS target_route_namespace,
    visible_target.slug AS target_slug
FROM public.public_content_items AS visible_source
INNER JOIN public.content_publication_relations AS link
    ON
        visible_source.publication_id = link.publication_id
        AND (
            link.locale_scope = ''
            OR visible_source.language_code = link.locale_scope
        )
INNER JOIN public.content_field_definitions AS definition
    ON
        link.type_id = definition.type_id
        AND link.schema_no = definition.schema_no
        AND link.field_id = definition.field_id
        AND definition.visibility = 'public'
INNER JOIN
    public.content_fields AS source_field
    ON
        link.type_id = source_field.type_id
        AND link.field_id = source_field.field_id
INNER JOIN
    public.public_content_items AS visible_target
    ON
        link.target_item_id = visible_target.item_id
        AND visible_source.language_code = visible_target.language_code;

COMMENT ON TABLE public.content_items IS
'Stable item identities and own sealed publication pointer.';

COMMENT ON TABLE public.content_drafts IS
'One editable draft pinned to a semantic schema and shared candidate sequence.';

COMMENT ON COLUMN public.content_drafts.shared_ready_seq IS
'NULL until explicit shared approval of the exact shared sequence.';

COMMENT ON TABLE public.content_draft_locales IS
'Localized draft scalar_values and exact candidate approval tuple.';

COMMENT ON COLUMN public.content_draft_locales.ready_locale_generation IS
'Enabled language_code generation approved with local/shared/schema sequences.';

COMMENT ON TABLE public.content_publications IS
'Immutable sealed snapshots with pinned semantic schema and copied '
'display metadata.';

COMMENT ON COLUMN public.content_publications.display_metadata IS
'Historical field labels copied at publish; public view projects only '
'public definitions.';

COMMENT ON TABLE public.content_publication_locales IS
'Immutable locale_code snapshot tied to a permanent item route reservation.';

COMMENT ON TABLE public.content_routes IS
'Permanent namespace/locale_code/slug reservations belonging to one item.';

COMMENT ON TABLE public.content_draft_relations IS
'Optional single-target relations checked against the draft pinned definition.';

COMMENT ON TABLE public.content_publication_relations IS
'Immutable one-hop relations; target visibility is checked on each public '
'read.';

COMMENT ON COLUMN public.content_draft_relations.locale_scope IS
'Empty for shared; otherwise an existing localized draft row.';

COMMENT ON COLUMN public.content_publication_relations.locale_scope IS
'Empty for shared; otherwise an existing snapshot locale row.';

COMMENT ON VIEW public.public_content_types IS
'Public Collection route identities without editorial definitions.';

COMMENT ON VIEW public.public_content_items IS
'Current sealed enabled-generation snapshots with public scalar and '
'pinned metadata projection.';

COMMENT ON VIEW public.public_content_routes IS
'Reserved aliases resolve directly to the current available item locale_code.';

COMMENT ON VIEW public.public_content_relations IS
'Public one-hop targets available in the same current locale_code generation.';

CREATE TRIGGER content_items_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_items
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_drafts_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_drafts
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_draft_locales_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_draft_locales
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_publications_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_publications
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_routes_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_routes
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_publication_locales_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_publication_locales
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_draft_relations_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_draft_relations
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_publication_relations_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_publication_relations
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

REVOKE ALL ON public.content_items,
public.content_drafts,
public.content_draft_locales,
public.content_publications,
public.content_routes,
public.content_publication_locales,
public.content_draft_relations,
public.content_publication_relations FROM public, sp_language_importer;

REVOKE ALL ON public.public_content_types,
public.public_content_items,
public.public_content_routes,
public.public_content_relations FROM public;

REVOKE ALL ON FUNCTION public.guard_content_publication(),
public.require_content_publication_sealed(),
public.guard_content_publication_child(),
public.guard_content_route(),
public.guard_content_relation(),
public.guard_content_locale_relation() FROM public;

GRANT SELECT, INSERT ON public.content_items,
public.content_drafts,
public.content_draft_locales,
public.content_publications,
public.content_routes,
public.content_publication_locales,
public.content_draft_relations,
public.content_publication_relations TO sp_publisher;

GRANT UPDATE (version, published_id) ON public.content_items TO sp_publisher;

GRANT UPDATE (
    schema_no, shared_values, shared_seq, shared_ready_seq
) ON public.content_drafts TO sp_publisher;

GRANT UPDATE (
    scalar_values,
    local_seq,
    ready_local_seq,
    ready_shared_seq,
    ready_schema_no,
    ready_locale_generation
) ON public.content_draft_locales TO sp_publisher;

GRANT UPDATE (sealed) ON public.content_publications TO sp_publisher;

GRANT UPDATE (
    schema_no, target_type_id, target_item_id
) ON public.content_draft_relations TO sp_publisher;

GRANT DELETE ON public.content_draft_relations TO sp_publisher;

REVOKE ALL ON FUNCTION public.guard_content_item_identity() FROM public;

-- +goose Down
DROP VIEW public.public_content_relations;
DROP VIEW public.public_content_routes;
DROP VIEW public.public_content_items;
DROP VIEW public.public_content_types;
ALTER TABLE public.content_items DROP CONSTRAINT content_items_publication_fkey;
DROP TABLE public.content_publication_relations;
DROP TABLE public.content_draft_relations;
DROP TABLE public.content_publication_locales;
DROP TABLE public.content_routes;
DROP TABLE public.content_publications;
DROP TABLE public.content_draft_locales;
DROP TABLE public.content_drafts;
DROP TABLE public.content_items;
DROP FUNCTION public.guard_content_locale_relation();
DROP FUNCTION public.guard_content_relation();
DROP FUNCTION public.guard_content_route();
DROP FUNCTION public.guard_content_publication_child();
DROP FUNCTION public.require_content_publication_sealed();
DROP FUNCTION public.guard_content_publication();
DROP FUNCTION public.guard_content_item_identity();
