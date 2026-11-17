-- +goose Up
-- Authentication uses a dedicated pool; editorial commands remain dormant.
CREATE TABLE public.users (
    id uuid CONSTRAINT users_pkey PRIMARY KEY,
    login_key text COLLATE "C" NOT NULL CONSTRAINT users_login_key_key UNIQUE,
    state text NOT NULL DEFAULT 'pending',
    version bigint NOT NULL DEFAULT 1,
    credential_version bigint NOT NULL DEFAULT 1,
    password_hash text,
    enrollment_completed_at timestamptz,
    CONSTRAINT users_login_key_check CHECK (
        length(login_key) BETWEEN 1 AND 256 AND login_key = btrim(login_key)
    ),
    CONSTRAINT users_state_check CHECK (
        state IN ('pending', 'disabled', 'active')
    ),
    CONSTRAINT users_password_check CHECK (
        password_hash IS null
        OR (
            length(password_hash) BETWEEN 40 AND 512
            AND password_hash LIKE '$argon2id$%'
        )
    ),
    CONSTRAINT users_enrollment_check CHECK (
        state <> 'active'
        OR (password_hash IS NOT null AND enrollment_completed_at IS NOT null)
    ),
    CONSTRAINT users_version_check CHECK (
        version > 0 AND credential_version > 0
    )
);

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
            AND type_id IS null
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
            AND target_scope IN ('installation', 'type')
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

CREATE TABLE public.sessions (
    id uuid CONSTRAINT sessions_pkey PRIMARY KEY,
    token_digest bytea NOT NULL CONSTRAINT sessions_token_digest_key UNIQUE,
    csrf_digest bytea NOT NULL,
    user_id uuid,
    kind text NOT NULL,
    credential_version bigint,
    step_up_at timestamptz,
    failed_attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT current_timestamp,
    last_seen timestamptz NOT NULL DEFAULT current_timestamp,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sessions_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT,
    CONSTRAINT sessions_digest_check CHECK (
        octet_length(token_digest) = 32 AND octet_length(csrf_digest) = 32
    ),
    CONSTRAINT sessions_kind_check CHECK (
        kind IN ('preauth', 'login', 'full', 'recovery', 'enrollment')
        AND (user_id IS null) = (kind = 'preauth')
        AND (credential_version IS null) = (kind = 'preauth')
        AND (credential_version IS null OR credential_version > 0)
        AND (step_up_at IS null OR kind = 'full')
        AND failed_attempts BETWEEN 0 AND 5
    ),
    CONSTRAINT sessions_time_check CHECK (
        last_seen >= created_at AND expires_at > last_seen
        AND expires_at <= created_at + CASE kind
            WHEN 'full' THEN interval '12 hours'
            WHEN 'recovery' THEN interval '10 minutes'
            WHEN 'enrollment' THEN interval '24 hours'
            ELSE interval '5 minutes'
        END
        AND (revoked_at IS null OR revoked_at >= created_at)
        AND (step_up_at IS null OR step_up_at BETWEEN created_at AND last_seen)
    )
);

CREATE INDEX sessions_user_idx ON public.sessions (user_id);

CREATE TABLE public.security_state (
    id integer CONSTRAINT security_state_pkey PRIMARY KEY,
    site text,
    bootstrap_completed boolean NOT NULL DEFAULT false,
    bootstrap_user_id uuid,
    CONSTRAINT security_state_singleton_check CHECK (id = 1),
    CONSTRAINT security_state_site_check CHECK (
        site IS null
        OR (
            length(site) <= 253
            AND site
            ~
            '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$'
        )
    ),
    CONSTRAINT security_state_shape_check CHECK (
        (site IS null) = (bootstrap_user_id IS null)
        AND (NOT bootstrap_completed OR bootstrap_user_id IS NOT null)
    ),
    CONSTRAINT security_state_user_fkey FOREIGN KEY (
        bootstrap_user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT
);

INSERT INTO public.security_state (id) VALUES (1);

CREATE TABLE public.mfa_totp (
    user_id uuid CONSTRAINT mfa_totp_pkey PRIMARY KEY,
    ciphertext bytea NOT NULL,
    key_version integer NOT NULL,
    confirmed_at timestamptz,
    last_counter bigint NOT NULL DEFAULT -1,
    CONSTRAINT mfa_totp_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT,
    CONSTRAINT mfa_totp_shape_check CHECK (
        octet_length(ciphertext) BETWEEN 48 AND 1024
        AND key_version > 0
        AND last_counter >= -1
        AND (confirmed_at IS null OR last_counter >= 0)
    )
);

CREATE TABLE public.recovery_codes (
    user_id uuid NOT NULL,
    digest bytea NOT NULL CONSTRAINT recovery_codes_digest_key UNIQUE,
    consumed_at timestamptz,
    CONSTRAINT recovery_codes_pkey PRIMARY KEY (user_id, digest),
    CONSTRAINT recovery_codes_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT,
    CONSTRAINT recovery_codes_digest_check CHECK (octet_length(digest) = 32)
);

CREATE TABLE public.recovery_attempts (
    id uuid CONSTRAINT recovery_attempts_pkey PRIMARY KEY,
    session_id uuid NOT NULL CONSTRAINT recovery_attempts_session_key UNIQUE,
    user_id uuid NOT NULL,
    code_digest bytea NOT NULL,
    credential_version bigint NOT NULL,
    pending_ciphertext bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT current_timestamp,
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    CONSTRAINT recovery_attempts_session_fkey FOREIGN KEY (
        session_id
    ) REFERENCES public.sessions (id) ON DELETE RESTRICT,
    CONSTRAINT recovery_attempts_code_fkey FOREIGN KEY (
        user_id, code_digest
    ) REFERENCES public.recovery_codes (user_id, digest) ON DELETE RESTRICT,
    CONSTRAINT recovery_attempts_shape_check CHECK (
        octet_length(code_digest) = 32
        AND octet_length(pending_ciphertext) BETWEEN 48 AND 1024
        AND credential_version > 0 AND key_version > 0
        AND expires_at > created_at
        AND expires_at <= created_at + interval '10 minutes'
        AND (ended_at IS null OR ended_at >= created_at)
    )
);

CREATE UNIQUE INDEX recovery_attempts_active_user_idx
ON public.recovery_attempts (user_id) WHERE ended_at IS null;

CREATE UNIQUE INDEX recovery_attempts_active_code_idx
ON public.recovery_attempts (code_digest) WHERE ended_at IS null;

CREATE TABLE public.auth_attempts (
    bucket_start timestamptz NOT NULL,
    kind text NOT NULL,
    key_digest bytea NOT NULL,
    count integer NOT NULL DEFAULT 0,
    CONSTRAINT auth_attempts_pkey PRIMARY KEY (bucket_start, kind, key_digest),
    CONSTRAINT auth_attempts_shape_check CHECK (
        kind IN ('account', 'ip', 'site', 'preauth_ip', 'preauth_site')
        AND octet_length(key_digest) = 32
        AND count BETWEEN 0 AND CASE kind
            WHEN 'account' THEN 5
            WHEN 'ip' THEN 30 WHEN 'preauth_ip' THEN 30 ELSE 100
        END
    )
);

COMMENT ON TABLE public.recovery_attempts IS
'Recovery reservations ended explicitly under the CMS mutex.';

COMMENT ON COLUMN public.recovery_attempts.pending_ciphertext IS
'AEAD-encrypted replacement TOTP seed confirmed only at completion.';

COMMENT ON TABLE public.auth_attempts IS
'Bounded authentication buckets storing account and IP digests.';

CREATE TABLE public.security_audit (
    id uuid CONSTRAINT security_audit_pkey PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    operation text NOT NULL,
    result text NOT NULL DEFAULT 'success',
    actor_id uuid,
    target_id uuid,
    request_id uuid,
    reason text NOT NULL DEFAULT '',
    CONSTRAINT security_audit_operation_check CHECK (
        operation IN (
            'bootstrap.started', 'bootstrap.restarted', 'bootstrap.completed',
            'bootstrap.denied', 'auth.login_started', 'auth.login_completed',
            'auth.denied',
            'auth.logout',
            'auth.sessions_revoked',
            'auth.step_up',
            'mfa.recovery_started', 'mfa.recovery_cancelled',
            'mfa.recovery_expired', 'mfa.recovery_completed'
        )
    ),
    CONSTRAINT security_audit_result_check CHECK (
        result IN ('success', 'denied', 'failure')
    ),
    CONSTRAINT security_audit_reason_check CHECK (
        reason IN (
            '', 'operator_enrollment', 'operator_restart', 'totp_verified',
            'invalid_credentials', 'expired', 'cancelled', 'session_revoked',
            'rate_limited', 'credential_changed'
        )
    )
);

COMMENT ON TABLE public.security_state IS
'Installation binding and irreversible one-time operator bootstrap marker.';

COMMENT ON COLUMN public.security_state.bootstrap_completed IS
'Never resets when accounts or administrative grants change.';

COMMENT ON TABLE public.mfa_totp IS
'AEAD-encrypted TOTP enrollment, key version and replay counter; never public.';

COMMENT ON COLUMN public.mfa_totp.last_counter IS
'Highest accepted TOTP time counter; -1 means no accepted code.';

COMMENT ON TABLE public.recovery_codes IS
'SHA-256 recovery digests; plaintext codes are returned only once after '
'commit.';

COMMENT ON TABLE public.security_audit IS
'Append-only bounded security events; immutable identity snapshots '
'without credential data.';

COMMENT ON TABLE public.grant_fields IS
'Exact field identities in a concrete content grant; no implicit future '
'fields.';

COMMENT ON TABLE public.users IS
'CMS account identities; activation requires verified password and MFA '
'enrollment.';

COMMENT ON COLUMN public.users.login_key IS
'Unique canonical login identifier; normalization belongs to the '
'future enrollment service.';

COMMENT ON COLUMN public.users.state IS
'Pending enrollment never authenticates; active requires completed enrollment.';

COMMENT ON COLUMN public.users.credential_version IS
'Credential generation checked on every authenticated session access.';

COMMENT ON TABLE public.roles IS
'Installation-local role identities; role names never authorize an '
'operation.';

COMMENT ON COLUMN public.roles.state IS
'Only active roles grant permissions; restore returns disabled.';

COMMENT ON TABLE public.user_roles IS
'Explicit account-role memberships; inactive accounts and roles '
'retain memberships.';

COMMENT ON TABLE public.permission_operations IS
'Registered Go operations; migration seeds the exact closed operation '
'set.';

COMMENT ON TABLE public.role_grants IS
'Exact installation or typed content grants and bounded delegation '
'authority; no future wildcard.';

COMMENT ON COLUMN public.role_grants.can_delegate IS
'Allows delegation only within the actor''s authorized delegation '
'envelope.';

COMMENT ON TABLE public.sessions IS
'Opaque session digests; never exposed through public views.';

COMMENT ON COLUMN public.sessions.kind IS
'Only full sessions authorize CMS; other kinds have restricted allowlists.';

COMMENT ON COLUMN public.sessions.credential_version IS
'Credential generation at issuance; NULL only for anonymous preauth.';

COMMENT ON COLUMN public.sessions.step_up_at IS
'Database time of successful password and TOTP reauthentication.';

COMMENT ON COLUMN public.sessions.expires_at IS
'Absolute expiry; idle touches update last_seen without extending this value.';

COMMENT ON COLUMN public.sessions.csrf_digest IS
'SHA-256 digest of a separate CSRF token; never a plaintext bearer.';

-- +goose StatementBegin
CREATE FUNCTION public.guard_security_state() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'bootstrap marker cannot be removed' USING ERRCODE = '23514';
    END IF;
    IF OLD.bootstrap_completed AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'completed bootstrap marker is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.site IS NOT NULL AND (NEW.site IS DISTINCT FROM OLD.site OR NEW.bootstrap_user_id IS DISTINCT FROM OLD.bootstrap_user_id) THEN
        RAISE EXCEPTION 'bootstrap installation binding is immutable' USING ERRCODE = '23514';
    END IF;
    IF NEW.bootstrap_completed AND NOT EXISTS (SELECT 1 FROM public.users u WHERE u.id=NEW.bootstrap_user_id AND u.state='active' AND u.enrollment_completed_at IS NOT NULL) THEN
        RAISE EXCEPTION 'bootstrap completion requires active enrolled account' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_security_audit() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    RAISE EXCEPTION 'security audit is append-only' USING ERRCODE = '23514';
END;
$body$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.guard_user_activation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF NEW.state = 'active' AND NOT EXISTS (
        SELECT 1 FROM public.mfa_totp m WHERE m.user_id=NEW.id AND m.confirmed_at IS NOT NULL AND m.last_counter >= 0
    ) THEN
        RAISE EXCEPTION 'active account requires verified MFA enrollment' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'accounts cannot be deleted' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION public.guard_security_state(),
public.guard_security_audit(),
public.guard_user_activation() FROM public;

CREATE TRIGGER security_state_immutable
BEFORE UPDATE OR DELETE
ON public.security_state
FOR EACH ROW
EXECUTE FUNCTION public.guard_security_state();

CREATE TRIGGER security_state_no_truncate
BEFORE TRUNCATE
ON public.security_state
FOR EACH STATEMENT
EXECUTE FUNCTION public.guard_security_state();

CREATE TRIGGER security_audit_append_only
BEFORE UPDATE OR DELETE OR TRUNCATE
ON public.security_audit
FOR EACH STATEMENT
EXECUTE FUNCTION public.guard_security_audit();

CREATE TRIGGER users_activation_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.users
FOR EACH ROW
EXECUTE FUNCTION public.guard_user_activation();

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

CREATE TRIGGER users_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.users
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER roles_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.roles
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER user_roles_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.user_roles
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER permission_operations_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.permission_operations
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER role_grants_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.role_grants
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER sessions_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.sessions
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER security_state_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.security_state
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER mfa_totp_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.mfa_totp
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER recovery_codes_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.recovery_codes
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER recovery_attempts_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.recovery_attempts
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER security_audit_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.security_audit
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

CREATE TRIGGER grant_fields_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.grant_fields
FOR EACH STATEMENT
EXECUTE FUNCTION public.cms_write_mutex();

REVOKE ALL ON public.security_state,
public.mfa_totp,
public.recovery_codes,
public.security_audit,
public.grant_fields,
public.users,
public.roles,
public.user_roles,
public.permission_operations,
public.role_grants,
public.sessions,
public.recovery_attempts,
public.auth_attempts FROM public,
sp_publisher,
sp_language_importer;

GRANT USAGE ON SCHEMA public TO sp_security;
GRANT SELECT ON public.users, public.roles, public.user_roles,
public.permission_operations, public.role_grants, public.grant_fields,
public.security_state, public.mfa_totp, public.recovery_codes,
public.sessions, public.recovery_attempts, public.auth_attempts TO sp_security;
GRANT UPDATE ON public.users, public.mfa_totp, public.recovery_codes,
public.sessions, public.recovery_attempts, public.auth_attempts TO sp_security;
GRANT INSERT ON public.sessions, public.recovery_attempts,
public.auth_attempts, public.recovery_codes TO sp_security;
GRANT DELETE ON public.auth_attempts, public.sessions TO sp_security;
GRANT INSERT ON public.security_audit TO sp_security;

-- +goose Down
DROP TABLE public.auth_attempts;
DROP TABLE public.recovery_attempts;
DROP TABLE public.security_audit;
DROP TABLE public.recovery_codes;
DROP TABLE public.mfa_totp;
DROP TABLE public.security_state;
DROP TABLE public.grant_fields;
DROP TABLE public.sessions;
DROP TABLE public.role_grants;
DROP TABLE public.permission_operations;
DROP TABLE public.user_roles;
DROP TABLE public.roles;
DROP TABLE public.users;
DROP FUNCTION public.guard_security_state();
DROP FUNCTION public.guard_security_audit();
DROP FUNCTION public.guard_user_activation();
