-- +goose Up
-- +goose StatementBegin
DO $precondition$
BEGIN
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('article_draft_translations','article_drafts_pkey','article_revision_translations','article_revision_translations_pkey','article_revisions','article_revisions_number_key','article_revisions_owner_key','article_revisions_pkey','articles','articles_pkey','auth_attempts','auth_attempts_pkey','content_draft_locales','content_draft_locales_locale_idx','content_draft_locales_pkey','content_draft_relations','content_draft_relations_definition_idx','content_draft_relations_pkey','content_draft_relations_target_idx','content_drafts','content_drafts_owner_key','content_drafts_pkey','content_drafts_schema_idx','content_field_definitions','content_field_definitions_field_idx','content_field_definitions_pkey','content_field_definitions_target_idx','content_fields','content_fields_pkey','content_fields_type_key','content_item_assignments','content_item_assignments_pkey','content_item_assignments_user_idx','content_items','content_items_pkey','content_items_publication_idx','content_items_type_key','content_publication_locales','content_publication_locales_item_idx','content_publication_locales_locale_idx','content_publication_locales_pkey','content_publication_locales_route_idx','content_publication_relations','content_publication_relations_definition_idx','content_publication_relations_pkey','content_publication_relations_target_idx','content_publications','content_publications_item_idx','content_publications_item_key','content_publications_order_idx','content_publications_owner_key','content_publications_pkey','content_publications_revision_key','content_publications_schema_idx','content_routes','content_routes_item_idx','content_routes_locale_idx','content_routes_owner_key','content_routes_pkey','content_schemas','content_schemas_pkey','content_types','content_types_key_key','content_types_namespace_key','content_types_pkey','enrollment_attempts','enrollment_attempts_active_user_idx','enrollment_attempts_pkey','enrollment_attempts_token_idx','grant_fields','grant_fields_field_idx','grant_fields_pkey','languages','languages_pkey','mfa_totp','mfa_totp_pkey','onboarding_tokens','onboarding_tokens_identity_key','onboarding_tokens_live_user_idx','onboarding_tokens_pkey','onboarding_tokens_user_idx','permission_operations','permission_operations_pkey','public_article_routes','public_article_translations','public_content_items','public_content_relations','public_content_routes','public_content_types','public_languages','public_routes','public_routes_owner_key','public_routes_pkey','public_site','public_ui_languages','public_ui_messages','recovery_attempts','recovery_attempts_active_code_idx','recovery_attempts_active_user_idx','recovery_attempts_pkey','recovery_attempts_session_key','recovery_codes','recovery_codes_digest_key','recovery_codes_pkey','role_grants','role_grants_id_type_key','role_grants_operation_idx','role_grants_operation_key','role_grants_pkey','role_grants_type_idx','roles','roles_name_key_key','roles_pkey','security_audit','security_audit_pkey','security_state','security_state_pkey','sessions','sessions_pkey','sessions_token_digest_key','sessions_user_idx','site_revision_keys','site_revision_keys_pkey','site_revision_languages','site_revision_languages_pkey','site_revision_messages','site_revision_messages_pkey','site_revisions','site_revisions_pkey','site_state','site_state_pkey','ui_message_keys','ui_message_keys_pkey','user_roles','user_roles_pkey','user_roles_role_idx','users','users_login_key_key','users_pkey'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_type WHERE typnamespace='public'::regnamespace AND typname IN
 ('article_draft_translations','article_revision_translations','article_revisions','articles','auth_attempts','content_draft_locales','content_draft_relations','content_drafts','content_field_definitions','content_fields','content_item_assignments','content_items','content_publication_locales','content_publication_relations','content_publications','content_routes','content_schemas','content_types','enrollment_attempts','grant_fields','languages','mfa_totp','onboarding_tokens','permission_operations','public_article_routes','public_article_translations','public_content_items','public_content_relations','public_content_routes','public_content_types','public_languages','public_routes','public_site','public_ui_languages','public_ui_messages','recovery_attempts','recovery_codes','role_grants','roles','security_audit','security_state','sessions','site_revision_keys','site_revision_languages','site_revision_messages','site_revisions','site_state','ui_message_keys','user_roles','users'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname IN
 ('cms_write_mutex','delete_archived_role','guard_content_definition','guard_content_identity','guard_content_item_identity','guard_content_locale_relation','guard_content_publication','guard_content_publication_child','guard_content_relation','guard_content_route','guard_content_schema','guard_language_generation','guard_published_revision','guard_revision','guard_revision_translation','guard_security_audit','guard_security_state','guard_site_child','guard_site_revision','guard_site_state','guard_user_activation','keep_route_reservation','lock_languages','require_closed_revision','require_content_publication_sealed','require_content_schema_sealed','reset_draft_ready','ui_placeholders')) THEN
 RAISE EXCEPTION 'application objects already exist; installation stopped before DDL';
 END IF;
END;
$precondition$;
-- +goose StatementEnd
-- Match Go strings.TrimSpace Unicode White_Space independently of DB locale.
CREATE TABLE public.languages (
    code text COLLATE "C" CONSTRAINT languages_pkey PRIMARY KEY,
    name text NOT NULL,
    native_name text NOT NULL,
    direction text NOT NULL,
    is_enabled boolean NOT NULL DEFAULT false,
    generation bigint NOT NULL DEFAULT 1,
    CONSTRAINT languages_generation_check CHECK (generation > 0),
    CONSTRAINT languages_code_check CHECK (
        length(code) BETWEEN 2 AND 63
        AND code = lower(code COLLATE "C")
        AND code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    ),
    CONSTRAINT languages_name_check CHECK (
        btrim(
            name,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    ),
    CONSTRAINT languages_native_name_check CHECK (
        btrim(
            native_name,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    ),
    CONSTRAINT languages_direction_check CHECK (direction IN ('ltr', 'rtl'))
);
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
'Acquire the installation write lock before statement row locks.';
REVOKE ALL ON FUNCTION public.cms_write_mutex() FROM public;
-- +goose StatementBegin
CREATE FUNCTION public.guard_language_generation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $body$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.generation <> 1 THEN
            RAISE EXCEPTION 'new locale generation must be one' USING ERRCODE = '23514';
        END IF;
    ELSE
        IF NEW.code IS DISTINCT FROM OLD.code THEN
            RAISE EXCEPTION 'locale identity is immutable' USING ERRCODE = '23514';
        END IF;
        NEW.generation := OLD.generation + CASE WHEN NEW.is_enabled IS DISTINCT FROM OLD.is_enabled THEN 1 ELSE 0 END;
    END IF;
    RETURN NEW;
END;
$body$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION public.guard_language_generation() FROM public;
CREATE TRIGGER languages_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.languages
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();
CREATE TRIGGER languages_generation_guard
BEFORE INSERT OR UPDATE ON public.languages
FOR EACH ROW EXECUTE FUNCTION public.guard_language_generation();
COMMENT ON COLUMN public.languages.generation IS
'Increments on every enabled toggle; old published locale snapshots stay '
'hidden.';
-- Publisher cannot update language metadata. PostgreSQL FOR SHARE needs UPDATE;
-- this single static function supplies the lock without granting that
-- privilege.
-- +goose StatementBegin
CREATE FUNCTION public.lock_languages(
    codes text[]
) RETURNS SETOF public.languages
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
 SELECT l.* FROM public.languages AS l WHERE l.code = ANY(codes)
 ORDER BY l.code COLLATE "C" FOR SHARE OF l;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION public.lock_languages(text[]) FROM public;
REVOKE CREATE ON SCHEMA public FROM public;
REVOKE ALL ON public.languages FROM public;
GRANT USAGE ON SCHEMA public TO sp_publisher, sp_language_importer;
GRANT SELECT ON public.languages TO sp_publisher;
GRANT EXECUTE ON FUNCTION public.lock_languages(text[]) TO sp_publisher;
GRANT SELECT ON public.languages TO sp_language_importer;
GRANT INSERT (code, name, native_name, direction),
UPDATE (
    name, native_name, direction
) ON public.languages TO sp_language_importer;
COMMENT ON TABLE public.languages IS
'Каталог языков установки; включение языка разрешается отдельно от '
'импорта метаданных.';
COMMENT ON COLUMN public.languages.code IS
'Нормализованный код языка BCP 47 в нижнем регистре; ключ переводов и '
'маршрутов.';
COMMENT ON COLUMN public.languages.name IS
'Название языка для выбора языка интерфейса.';
COMMENT ON COLUMN public.languages.native_name IS
'Самоназвание языка на его родном языке.';
COMMENT ON COLUMN public.languages.direction IS
'Направление письма: ltr или rtl.';
COMMENT ON COLUMN public.languages.is_enabled IS
'Разрешение публичной выдачи языка; новый язык по умолчанию выключен.';
COMMENT ON FUNCTION public.lock_languages(
    text[]
) IS
'Блокирует выбранные языки до конца транзакции публикации без выдачи '
'редактору права менять каталог.';
-- Представления исполняются с правами владельца; runtime не получает доступ
-- к истории.
CREATE VIEW public.public_languages WITH (security_barrier = true) AS
SELECT
    code,
    name,
    native_name,
    direction
FROM public.languages
WHERE is_enabled;
COMMENT ON VIEW public.public_languages IS
'Публичные метаданные только включённых языков установки.';
COMMENT ON COLUMN public.public_languages.code IS
'Код включённого языка для публичного выбора.';
COMMENT ON COLUMN public.public_languages.name IS
'Название включённого языка.';
COMMENT ON COLUMN public.public_languages.native_name IS
'Самоназвание включённого языка.';
COMMENT ON COLUMN public.public_languages.direction IS
'Направление письма включённого языка: ltr или rtl.';
REVOKE ALL ON public.public_languages FROM public;
-- +goose Down
DROP VIEW public.public_languages;
DROP FUNCTION public.lock_languages(text[]);
DROP TABLE public.languages;
DROP FUNCTION public.guard_language_generation();
DROP FUNCTION public.cms_write_mutex();
