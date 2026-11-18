-- +goose Up
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

CREATE TABLE public.onboarding_tokens (
    digest bytea CONSTRAINT onboarding_tokens_pkey PRIMARY KEY,
    user_id uuid NOT NULL,
    credential_version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT onboarding_tokens_user_fkey FOREIGN KEY (
        user_id
    ) REFERENCES public.users (id) ON DELETE RESTRICT,
    CONSTRAINT onboarding_tokens_identity_key UNIQUE (
        digest, user_id, credential_version
    ),
    CONSTRAINT onboarding_tokens_shape_check CHECK (
        octet_length(digest) = 32 AND credential_version > 0
        AND expires_at > created_at
        AND expires_at <= created_at + interval '24 hours'
        AND (consumed_at IS null OR consumed_at >= created_at)
    )
);

CREATE INDEX onboarding_tokens_user_idx ON public.onboarding_tokens (user_id);

CREATE UNIQUE INDEX onboarding_tokens_live_user_idx
ON public.onboarding_tokens (user_id) WHERE consumed_at IS null;

CREATE TABLE public.enrollment_attempts (
    session_id uuid CONSTRAINT enrollment_attempts_pkey PRIMARY KEY,
    user_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    credential_version bigint NOT NULL,
    password_hash text NOT NULL,
    pending_ciphertext bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    CONSTRAINT enrollment_attempts_session_fkey FOREIGN KEY (
        session_id
    ) REFERENCES public.sessions (id) ON DELETE RESTRICT,
    CONSTRAINT enrollment_attempts_token_fkey FOREIGN KEY (
        token_digest, user_id, credential_version
    ) REFERENCES public.onboarding_tokens (
        digest, user_id, credential_version
    ) ON DELETE RESTRICT,
    CONSTRAINT enrollment_attempts_shape_check CHECK (
        credential_version > 0 AND key_version > 0
        AND length(password_hash) BETWEEN 40 AND 512
        AND password_hash LIKE '$argon2id$%'
        AND octet_length(pending_ciphertext) BETWEEN 48 AND 1024
        AND expires_at > created_at
        AND expires_at <= created_at + interval '10 minutes'
        AND (ended_at IS null OR ended_at >= created_at)
    )
);

CREATE INDEX enrollment_attempts_token_idx
ON public.enrollment_attempts (token_digest, user_id, credential_version);

CREATE UNIQUE INDEX enrollment_attempts_active_user_idx
ON public.enrollment_attempts (user_id) WHERE ended_at IS null;

COMMENT ON TABLE public.onboarding_tokens IS
'Single-use SHA-256 onboarding digests bound to the pending credential '
'version.';

COMMENT ON COLUMN public.onboarding_tokens.consumed_at IS
'Set when enrollment starts or the issuer invalidates an earlier token.';

COMMENT ON TABLE public.enrollment_attempts IS
'Restricted own-account enrollment; completion leaves the account disabled.';

COMMENT ON COLUMN public.enrollment_attempts.pending_ciphertext IS
'AEAD-encrypted TOTP seed; confirmation is required before factors are '
'installed.';

COMMENT ON COLUMN public.enrollment_attempts.password_hash IS
'Pending Argon2id PHC; never used by login before enrollment completion.';

CREATE TABLE public.auth_attempt_buckets (
    bucket_start timestamptz NOT NULL,
    kind text NOT NULL,
    key_digest bytea NOT NULL,
    count integer NOT NULL DEFAULT 0,
    CONSTRAINT auth_attempt_buckets_pkey PRIMARY KEY (
        bucket_start, kind, key_digest
    ),
    CONSTRAINT auth_attempt_buckets_shape_check CHECK (
        kind IN ('account', 'ip', 'site', 'preauth_ip', 'preauth_site')
        AND octet_length(key_digest) = 32
        AND count BETWEEN 0 AND CASE kind
            WHEN 'account' THEN 5
            WHEN 'ip' THEN 30 WHEN 'preauth_ip' THEN 30 ELSE 100
        END
    )
);

COMMENT ON TABLE public.recovery_attempts IS
'Recovery reservations ended explicitly under the installation write lock.';

COMMENT ON COLUMN public.recovery_attempts.pending_ciphertext IS
'AEAD-encrypted replacement TOTP seed confirmed only at completion.';

COMMENT ON TABLE public.auth_attempt_buckets IS
'Rate-limit counters by time window and digested scope key.';

CREATE TABLE public.security_audit (
    id uuid CONSTRAINT security_audit_pkey PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    operation text NOT NULL,
    result text NOT NULL DEFAULT 'success',
    actor_id uuid,
    target_id uuid,
    request_id uuid,
    reason text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}',
    CONSTRAINT security_audit_operation_check CHECK (
        operation IN (
            'bootstrap.started', 'bootstrap.restarted', 'bootstrap.completed',
            'bootstrap.denied', 'auth.login_started', 'auth.login_completed',
            'auth.denied',
            'auth.logout',
            'auth.session_revoked', 'auth.sessions_revoked',
            'auth.password_changed',
            'auth.step_up',
            'users.created', 'users.onboarding_issued',
            'roles.created', 'roles.edited', 'roles.enabled', 'roles.disabled',
            'roles.archived', 'roles.restored', 'roles.copied', 'roles.deleted',
            'users.activated', 'users.disabled', 'users.roles_replaced',
            'content.assignees_replaced', 'content.draft_created',
            'content.draft_saved', 'content.draft_ready',
            'content.draft_published', 'content.draft_unpublished',
            'content.draft_schema_adopted',
            'content.collection_created', 'content.collection_revised',
            'content.initial_access_granted',
            'content.new_field_access_granted',
            'settings.draft_saved', 'settings.published',
            'settings.initialized',
            'auth.enrollment_started', 'auth.enrollment_completed',
            'auth.enrollment_cancelled',
            'mfa.recovery_started', 'mfa.recovery_cancelled',
            'mfa.recovery_expired', 'mfa.recovery_completed',
            'mfa.replacement_started', 'mfa.replaced',
            'mfa.recovery_codes_regenerated'
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
    ),
    CONSTRAINT security_audit_details_check CHECK (
        jsonb_typeof(details) = 'object'
        AND octet_length(details::text) <= 65536
    )
);

CREATE INDEX security_audit_recent_idx
ON public.security_audit (occurred_at DESC, id DESC);

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
'Append-only security events with actor and target IDs.';

COMMENT ON COLUMN public.security_audit.details IS
'Event identifiers and access changes. No credentials or content values.';

COMMENT ON TABLE public.users IS
'Account identities; activation requires verified password and MFA '
'enrollment.';

COMMENT ON COLUMN public.users.login_key IS
'Canonical login identifier used for enrollment and sign-in.';

COMMENT ON COLUMN public.users.state IS
'Pending accounts can enroll. Active accounts can sign in.';

COMMENT ON COLUMN public.users.credential_version IS
'Credential generation checked on every authenticated session access.';

COMMENT ON TABLE public.sessions IS
'Opaque session digests; never exposed through public views.';

COMMENT ON COLUMN public.sessions.kind IS
'Only full sessions authorize administration; other kinds have restricted '
'allowlists.';

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

CREATE TRIGGER users_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.users
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER sessions_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.sessions
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER security_state_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.security_state
FOR EACH STATEMENT
EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER mfa_totp_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.mfa_totp
FOR EACH STATEMENT
EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER recovery_codes_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.recovery_codes
FOR EACH STATEMENT
EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER recovery_attempts_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.recovery_attempts
FOR EACH STATEMENT
EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER security_audit_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
ON public.security_audit
FOR EACH STATEMENT
EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER onboarding_tokens_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.onboarding_tokens
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

CREATE TRIGGER enrollment_attempts_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.enrollment_attempts
FOR EACH STATEMENT EXECUTE FUNCTION public.installation_write_mutex();

REVOKE ALL ON public.security_state,
public.mfa_totp,
public.recovery_codes,
public.security_audit,
public.users,
public.sessions,
public.recovery_attempts,
public.onboarding_tokens,
public.enrollment_attempts,
public.auth_attempt_buckets FROM public,
sp_publisher,
sp_language_importer;

GRANT USAGE ON SCHEMA public TO sp_security;

GRANT SELECT ON public.users,
public.security_state,
public.mfa_totp,
public.recovery_codes,
public.sessions,
public.recovery_attempts,
public.auth_attempt_buckets,
public.onboarding_tokens,
public.enrollment_attempts TO sp_security;

GRANT UPDATE ON public.users, public.mfa_totp, public.recovery_codes,
public.sessions, public.recovery_attempts,
public.auth_attempt_buckets, public.onboarding_tokens,
public.enrollment_attempts TO sp_security;

GRANT INSERT ON public.sessions, public.recovery_attempts,
public.auth_attempt_buckets, public.recovery_codes, public.users,
public.mfa_totp, public.onboarding_tokens,
public.enrollment_attempts TO sp_security;

GRANT DELETE ON public.auth_attempt_buckets, public.sessions TO sp_security;

GRANT INSERT ON public.security_audit TO sp_security;
GRANT SELECT (
    id, occurred_at, operation, result, reason, actor_id, target_id, request_id
) ON public.security_audit TO sp_security;

-- +goose Down
DROP TABLE public.enrollment_attempts;
DROP TABLE public.onboarding_tokens;
DROP TABLE public.auth_attempt_buckets;
DROP TABLE public.recovery_attempts;
DROP TABLE public.security_audit;
DROP TABLE public.recovery_codes;
DROP TABLE public.mfa_totp;
DROP TABLE public.security_state;
DROP TABLE public.sessions;
DROP TABLE public.users;
DROP FUNCTION public.guard_security_state();
DROP FUNCTION public.guard_security_audit();
DROP FUNCTION public.guard_user_activation();
