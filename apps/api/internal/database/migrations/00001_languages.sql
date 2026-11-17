-- +goose Up
-- +goose StatementBegin
DO $precondition$
BEGIN
 -- Не принимаем чужие publishing-объекты за новую установку.
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('article_draft_translations','article_drafts_pkey','article_revision_translations','article_revision_translations_pkey','article_revisions','article_revisions_number_key','article_revisions_owner_key','article_revisions_pkey','articles','articles_pkey','content_fields','content_fields_pkey','content_fields_type_key','content_types','content_types_key_key','content_types_pkey','grant_fields','grant_fields_field_idx','grant_fields_pkey','languages','languages_pkey','mfa_totp','mfa_totp_pkey','permission_operations','permission_operations_pkey','public_article_routes','public_article_translations','public_languages','public_routes','public_routes_owner_key','public_routes_pkey','public_site','public_ui_languages','public_ui_messages','recovery_codes','recovery_codes_digest_key','recovery_codes_pkey','role_grants','role_grants_id_type_key','role_grants_operation_idx','role_grants_operation_key','role_grants_pkey','role_grants_type_idx','roles','roles_name_key_key','roles_pkey','security_audit','security_audit_pkey','security_state','security_state_pkey','sessions','sessions_pkey','sessions_token_digest_key','sessions_user_idx','site_revision_keys','site_revision_keys_pkey','site_revision_languages','site_revision_languages_pkey','site_revision_messages','site_revision_messages_pkey','site_revisions','site_revisions_pkey','site_state','site_state_pkey','ui_message_keys','ui_message_keys_pkey','user_roles','user_roles_pkey','user_roles_role_idx','users','users_login_key_key','users_pkey'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_type WHERE typnamespace='public'::regnamespace AND typname IN
 ('article_draft_translations','article_revision_translations','article_revisions','articles','content_fields','content_types','grant_fields','languages','mfa_totp','permission_operations','public_article_routes','public_article_translations','public_languages','public_routes','public_site','public_ui_languages','public_ui_messages','recovery_codes','role_grants','roles','security_audit','security_state','sessions','site_revision_keys','site_revision_languages','site_revision_messages','site_revisions','site_state','ui_message_keys','user_roles','users'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc WHERE pronamespace='public'::regnamespace AND proname IN
 ('guard_security_state','guard_security_audit','guard_user_activation','cms_write_mutex','reset_draft_ready','guard_revision_translation','guard_revision','require_closed_revision','guard_published_revision','keep_route_reservation','lock_languages','ui_placeholders','guard_site_child','guard_site_revision','guard_site_state')) THEN
 RAISE EXCEPTION 'publishing-объекты уже существуют; установка остановлена до DDL';
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
