-- +goose Up
CREATE TABLE public.roles (
    id uuid CONSTRAINT roles_pkey PRIMARY KEY,
    name_key text COLLATE "C" NOT NULL CONSTRAINT roles_name_key_key UNIQUE,
    state text NOT NULL DEFAULT 'disabled',
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT roles_name_key_check CHECK (
        length(name_key) BETWEEN 1 AND 128 AND name_key = btrim(name_key)
    ),
    CONSTRAINT roles_state_check CHECK (
        state IN ('disabled', 'active', 'archived')
    ),
    CONSTRAINT roles_version_check CHECK (version > 0)
);

CREATE TABLE public.user_roles (
    user_id uuid NOT NULL,
    role_id uuid NOT NULL,
    CONSTRAINT user_roles_pkey PRIMARY KEY (user_id, role_id),
    CONSTRAINT user_roles_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT,
    CONSTRAINT user_roles_role_fkey FOREIGN KEY (
        role_id
    ) REFERENCES public.roles (id) ON DELETE RESTRICT
);

CREATE INDEX user_roles_role_idx ON public.user_roles (role_id);

CREATE TABLE public.content_item_assignments (
    item_id uuid NOT NULL,
    user_id uuid NOT NULL,
    CONSTRAINT content_item_assignments_pkey PRIMARY KEY (item_id, user_id),
    CONSTRAINT content_item_assignments_item_fkey FOREIGN KEY (
        item_id
    ) REFERENCES public.content_items (id) ON DELETE RESTRICT,
    CONSTRAINT content_item_assignments_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT
);

CREATE INDEX content_item_assignments_user_idx
ON public.content_item_assignments (user_id);

CREATE TABLE public.permission_operations (
    key text COLLATE "C" CONSTRAINT permission_operations_pkey PRIMARY KEY,
    scope text NOT NULL,
    CONSTRAINT permission_operations_scope_check CHECK (
        scope IN ('installation', 'content')
    )
);

CREATE TABLE public.role_grants (
    id uuid CONSTRAINT role_grants_pkey PRIMARY KEY,
    role_id uuid NOT NULL,
    operation text COLLATE "C" NOT NULL,
    scope text NOT NULL DEFAULT 'installation',
    type_id uuid,
    object_scope text,
    can_delegate boolean NOT NULL DEFAULT false,
    delegate_operations text[] NOT NULL DEFAULT '{}',
    target_scope text,
    object_ceiling text,
    may_delegate_content boolean NOT NULL DEFAULT false,
    CONSTRAINT role_grants_role_fkey FOREIGN KEY (
        role_id
    ) REFERENCES public.roles (id) ON DELETE RESTRICT,
    CONSTRAINT role_grants_operation_fkey FOREIGN KEY (
        operation
    ) REFERENCES public.permission_operations (key) ON DELETE RESTRICT,
    CONSTRAINT role_grants_type_fkey FOREIGN KEY (
        type_id
    ) REFERENCES public.content_types (id) ON DELETE RESTRICT,
    CONSTRAINT role_grants_id_type_key UNIQUE (id, type_id),
    CONSTRAINT role_grants_operation_key UNIQUE NULLS NOT DISTINCT (
        role_id, operation, type_id, object_scope
    ),
    CONSTRAINT role_grants_scope_check CHECK (
        (
            scope = 'installation'
            AND (
                type_id IS null
                OR (
                    operation = 'content.permissions.delegate'
                    AND target_scope = 'type'
                )
            )
            AND object_scope IS null
            AND operation NOT LIKE 'content.items.%'
        )
        OR (
            scope = 'content'
            AND type_id IS NOT null
            AND object_scope IS NOT null
            AND (
                (operation = 'content.items.create' AND object_scope = 'type')
                OR (
                    operation <> 'content.items.create'
                    AND object_scope IN ('assigned', 'any')
                )
            )
            AND operation LIKE 'content.items.%'
        )
    ),
    CONSTRAINT role_grants_delegation_check CHECK (
        (
            operation = 'content.permissions.delegate'
            AND scope = 'installation'
            AND target_scope IS NOT null
            AND object_ceiling IS NOT null
            AND (
                (target_scope = 'installation' AND type_id IS null)
                OR (target_scope = 'type' AND type_id IS NOT null)
            )
            AND object_ceiling IN ('assigned', 'any')
            AND cardinality(delegate_operations) BETWEEN 1 AND 7
            AND delegate_operations
            <@ ARRAY[
                'content.items.create',
                'content.items.read',
                'content.items.update',
                'content.items.ready',
                'content.items.publish',
                'content.items.unpublish',
                'content.items.assign'
            ]::text[]
            AND array_position(delegate_operations, null) IS null
        )
        OR (
            operation <> 'content.permissions.delegate'
            AND delegate_operations = '{}'
            AND target_scope IS null
            AND object_ceiling IS null
            AND NOT may_delegate_content
        )
    )
);

CREATE INDEX role_grants_operation_idx ON public.role_grants (operation);

CREATE INDEX role_grants_type_idx ON public.role_grants (type_id);

CREATE TABLE public.grant_fields (
    grant_id uuid NOT NULL,
    type_id uuid NOT NULL,
    field_id uuid NOT NULL,
    CONSTRAINT grant_fields_pkey PRIMARY KEY (grant_id, field_id),
    CONSTRAINT grant_fields_grant_fkey FOREIGN KEY (
        grant_id, type_id
    ) REFERENCES public.role_grants (id, type_id) ON DELETE RESTRICT,
    CONSTRAINT grant_fields_field_fkey FOREIGN KEY (
        type_id, field_id
    ) REFERENCES public.content_fields (type_id, field_id) ON DELETE RESTRICT
);

CREATE INDEX grant_fields_field_idx ON public.grant_fields (type_id, field_id);

COMMENT ON TABLE public.grant_fields IS
'Exact field identities in a concrete content grant; no implicit future '
'fields.';

COMMENT ON TABLE public.roles IS
'Installation-local role identities; role names never authorize an '
'operation.';

COMMENT ON COLUMN public.roles.state IS
'Only active roles grant permissions; restore returns disabled.';

COMMENT ON TABLE public.user_roles IS
'Explicit account-role memberships; inactive accounts and roles '
'retain memberships.';

COMMENT ON TABLE public.content_item_assignments IS
'Explicit item assignments for scoped CMS grants; independent of public '
'byline.';

COMMENT ON TABLE public.permission_operations IS
'Registered Go operations; migration seeds the exact closed operation '
'set.';

COMMENT ON TABLE public.role_grants IS
'Exact installation or typed content grants and bounded delegation '
'authority; no future wildcard.';

COMMENT ON COLUMN public.role_grants.can_delegate IS
'Allows delegation only within the actor''s authorized delegation '
'envelope.';

COMMENT ON COLUMN public.role_grants.type_id IS
'Exact data type or delegation target type; installation-wide authority '
'has no implicit field grants.';

INSERT INTO public.permission_operations (key, scope) VALUES
('content.items.create', 'content'),
('content.items.read', 'content'),
('content.items.update', 'content'),
('content.items.ready', 'content'),
('content.items.publish', 'content'),
('content.items.unpublish', 'content'),
('content.items.assign', 'content'),
('content_types.manage', 'installation'),
('content_types.initialize_access', 'installation'),
('content.permissions.delegate', 'installation'),
('users.create', 'installation'),
('users.read', 'installation'),
('users.activate', 'installation'),
('users.disable', 'installation'),
('roles.create', 'installation'),
('roles.read', 'installation'),
('roles.edit', 'installation'),
('roles.assign', 'installation'),
('roles.disable', 'installation'),
('roles.enable', 'installation'),
('roles.archive', 'installation'),
('roles.restore', 'installation'),
('roles.delete', 'installation'),
('sessions.revoke_any', 'installation'),
('audit.read', 'installation'),
('settings.manage', 'installation');

CREATE TRIGGER roles_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.roles
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER user_roles_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.user_roles
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER content_item_assignments_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.content_item_assignments
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER permission_operations_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.permission_operations
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER role_grants_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.role_grants
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER grant_fields_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.grant_fields
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

REVOKE ALL ON public.grant_fields,
public.roles,
public.user_roles,
public.permission_operations,
public.role_grants FROM public,
sp_publisher,
sp_language_importer;

GRANT SELECT ON public.roles,
public.user_roles,
public.permission_operations,
public.role_grants,
public.grant_fields TO sp_security;

-- Role commands manage policy and state with column-level privileges.
GRANT INSERT (id, name_key) ON public.roles TO sp_security;
GRANT INSERT ON public.role_grants, public.grant_fields TO sp_security;
GRANT UPDATE (name_key, state, version) ON public.roles TO sp_security;
GRANT DELETE ON public.role_grants, public.grant_fields TO sp_security;
GRANT INSERT, DELETE ON public.user_roles TO sp_security;
GRANT SELECT (id) ON public.content_types TO sp_security;
GRANT SELECT (type_id, field_id) ON public.content_fields TO sp_security;

REVOKE ALL ON public.content_item_assignments FROM public;
GRANT SELECT, INSERT, DELETE ON public.content_item_assignments TO sp_security;
GRANT SELECT (key) ON public.content_types TO sp_security;
GRANT SELECT (id, type_id, version) ON public.content_items TO sp_security;
GRANT UPDATE (version) ON public.content_items TO sp_security;

-- Every field dependency belongs to a role grant.
-- +goose StatementBegin
CREATE FUNCTION public.delete_archived_role(
    role_id uuid, expected_version bigint
) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
 WITH deleted AS (
  DELETE FROM public.roles AS r
  WHERE r.id = $1 AND r.version = $2 AND r.state = 'archived'
  AND NOT EXISTS (SELECT 1 FROM public.user_roles AS m WHERE m.role_id = r.id)
  AND NOT EXISTS (SELECT 1 FROM public.role_grants AS g WHERE g.role_id = r.id)
  RETURNING r.id
 )
 SELECT EXISTS (SELECT 1 FROM deleted);
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION public.delete_archived_role(uuid, bigint) FROM public;
GRANT EXECUTE ON FUNCTION public.delete_archived_role(
    uuid, bigint
) TO sp_security;

-- +goose Down
DROP FUNCTION public.delete_archived_role(uuid, bigint);
REVOKE SELECT (id, key) ON public.content_types FROM sp_security;
REVOKE SELECT (type_id, field_id) ON public.content_fields FROM sp_security;
REVOKE SELECT (id, type_id, version), UPDATE (version)
ON public.content_items FROM sp_security;
DROP TABLE public.content_item_assignments;
DROP TABLE public.grant_fields;
DROP TABLE public.role_grants;
DROP TABLE public.permission_operations;
DROP TABLE public.user_roles;
DROP TABLE public.roles;
