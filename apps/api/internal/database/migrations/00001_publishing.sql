-- +goose Up
-- +goose StatementBegin
DO $precondition$
BEGIN
 -- Не принимаем чужие publishing-объекты за новую установку.
 IF EXISTS (SELECT 1 FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname IN
 ('security_state','mfa_totp','recovery_codes','security_audit','grant_fields','users','roles','user_roles','permission_operations','role_grants','sessions','content_types','content_fields','languages','articles','article_draft_translations','article_revisions','article_revision_translations','public_routes','ui_message_keys','site_state','site_revisions','site_revision_keys','site_revision_languages','site_revision_messages','public_languages','public_article_translations','public_article_routes','public_site','public_ui_languages','public_ui_messages'))
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
CREATE TABLE public.articles (
    id uuid CONSTRAINT articles_pkey PRIMARY KEY,
    version bigint NOT NULL DEFAULT 1,
    published_revision_id uuid,
    CONSTRAINT articles_version_check CHECK (version >= 1)
);
CREATE TABLE public.article_draft_translations (
    article_id uuid NOT NULL,
    language_code text COLLATE "C" NOT NULL,
    title text,
    slug text COLLATE "C",
    body_markdown text,
    is_ready boolean NOT NULL DEFAULT false,
    CONSTRAINT article_drafts_pkey PRIMARY KEY (article_id, language_code),
    CONSTRAINT article_drafts_article_fkey FOREIGN KEY (
        article_id
    ) REFERENCES public.articles (id),
    CONSTRAINT article_drafts_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT article_drafts_ready_check CHECK (NOT is_ready OR (
        title IS NOT null
        AND btrim(
            title,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
        AND slug IS NOT null
        AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
        AND length(slug) <= 200
        AND body_markdown IS NOT null AND btrim(
            body_markdown, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        ) <> ''
    ))
);
CREATE TABLE public.article_revisions (
    id uuid CONSTRAINT article_revisions_pkey PRIMARY KEY,
    article_id uuid NOT NULL,
    revision_no bigint NOT NULL,
    sealed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT article_revisions_article_fkey FOREIGN KEY (
        article_id
    ) REFERENCES public.articles (id),
    CONSTRAINT article_revisions_owner_key UNIQUE (article_id, id),
    CONSTRAINT article_revisions_number_key UNIQUE (article_id, revision_no),
    CONSTRAINT article_revisions_number_check CHECK (revision_no >= 1)
);
ALTER TABLE public.articles ADD CONSTRAINT articles_published_revision_fkey
FOREIGN KEY (id, published_revision_id) REFERENCES public.article_revisions (
    article_id, id
);
CREATE TABLE public.public_routes (
    language_code text COLLATE "C" NOT NULL,
    slug text COLLATE "C" NOT NULL,
    article_id uuid NOT NULL,
    CONSTRAINT public_routes_pkey PRIMARY KEY (language_code, slug),
    CONSTRAINT public_routes_owner_key UNIQUE (article_id, language_code, slug),
    CONSTRAINT public_routes_article_fkey FOREIGN KEY (
        article_id
    ) REFERENCES public.articles (id),
    CONSTRAINT public_routes_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT public_routes_slug_check CHECK (
        slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 200
    )
);
CREATE TABLE public.article_revision_translations (
    article_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    language_code text COLLATE "C" NOT NULL,
    title text NOT NULL,
    slug text COLLATE "C" NOT NULL,
    body_markdown text NOT NULL,
    CONSTRAINT article_revision_translations_pkey PRIMARY KEY (
        revision_id, language_code
    ),
    CONSTRAINT article_revision_translations_revision_fkey FOREIGN KEY (
        article_id, revision_id
    ) REFERENCES public.article_revisions (article_id, id),
    CONSTRAINT article_revision_translations_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT article_revision_translations_route_fkey FOREIGN KEY (
        article_id, language_code, slug
    ) REFERENCES public.public_routes (article_id, language_code, slug),
    CONSTRAINT article_revision_translations_title_check CHECK (
        btrim(
            title,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    ),
    CONSTRAINT article_revision_translations_body_check CHECK (
        btrim(
            body_markdown,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    )
);
-- +goose StatementBegin
CREATE FUNCTION public.reset_draft_ready() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF (NEW.title,NEW.slug,NEW.body_markdown) IS DISTINCT FROM (OLD.title,OLD.slug,OLD.body_markdown) THEN
  NEW.is_ready := false;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER article_drafts_reset_ready
BEFORE UPDATE
ON public.article_draft_translations
FOR EACH ROW
EXECUTE FUNCTION public.reset_draft_ready();
-- A row lock serializes inserts into a snapshot with its sealing transition.
-- +goose StatementBegin
CREATE FUNCTION public.guard_revision_translation()
RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
DECLARE closed boolean;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  SELECT sealed INTO closed FROM public.article_revisions WHERE id = OLD.revision_id FOR UPDATE;
  IF closed THEN RAISE EXCEPTION 'закрытая ревизия неизменяема' USING ERRCODE = '23514'; END IF;
 END IF;
 IF TG_OP <> 'DELETE' THEN
  SELECT sealed INTO closed FROM public.article_revisions WHERE id = NEW.revision_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'перед добавлением перевода ревизия должна быть видима' USING ERRCODE = '23503'; END IF;
  IF closed THEN RAISE EXCEPTION 'закрытая ревизия неизменяема' USING ERRCODE = '23514'; END IF;
  RETURN NEW;
 END IF;
 RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER article_revision_translations_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.article_revision_translations
FOR EACH ROW
EXECUTE FUNCTION public.guard_revision_translation();
-- +goose StatementBegin
CREATE FUNCTION public.guard_revision() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF TG_OP = 'INSERT' THEN
  IF NEW.sealed THEN RAISE EXCEPTION 'перед закрытием ревизия должна быть собрана' USING ERRCODE = '23514'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.sealed THEN RAISE EXCEPTION 'закрытая ревизия неизменяема' USING ERRCODE = '23514'; END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 IF (NEW.id,NEW.article_id,NEW.revision_no,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.article_id,OLD.revision_no,OLD.created_at) THEN
  RAISE EXCEPTION 'идентификаторы ревизии неизменяемы' USING ERRCODE = '23514';
 END IF;
 IF NEW.sealed AND NOT EXISTS (SELECT 1 FROM public.article_revision_translations WHERE revision_id = NEW.id) THEN
  RAISE EXCEPTION 'нельзя закрыть пустую ревизию' USING ERRCODE = '23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER article_revisions_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.article_revisions
FOR EACH ROW
EXECUTE FUNCTION public.guard_revision();
-- +goose StatementBegin
CREATE FUNCTION public.require_closed_revision()
RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM public.article_revisions WHERE id = NEW.id AND NOT sealed) THEN
  RAISE EXCEPTION 'нельзя зафиксировать незакрытую ревизию' USING ERRCODE = '23514';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER article_revisions_closed
AFTER INSERT OR UPDATE
ON public.article_revisions DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION public.require_closed_revision();
-- +goose StatementBegin
CREATE FUNCTION public.guard_published_revision()
RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
DECLARE closed boolean;
BEGIN
 IF NEW.published_revision_id IS NOT NULL THEN
  SELECT sealed INTO closed FROM public.article_revisions
   WHERE id = NEW.published_revision_id AND article_id = NEW.id FOR SHARE;
  IF closed IS DISTINCT FROM true THEN RAISE EXCEPTION 'публикация требует собственной закрытой ревизии' USING ERRCODE = '23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER articles_published_guard
BEFORE INSERT OR UPDATE OF published_revision_id,
id
ON public.articles
FOR EACH ROW
EXECUTE FUNCTION public.guard_published_revision();
-- +goose StatementBegin
CREATE FUNCTION public.keep_route_reservation() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 RAISE EXCEPTION 'резервирования публичных адресов постоянны' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER public_routes_permanent
BEFORE UPDATE OR DELETE
ON public.public_routes
FOR EACH ROW
EXECUTE FUNCTION public.keep_route_reservation();
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
REVOKE ALL ON public.languages,
public.articles,
public.article_draft_translations,
public.article_revisions,
public.article_revision_translations,
public.public_routes FROM public;
GRANT USAGE ON SCHEMA public TO sp_publisher, sp_language_importer;
GRANT SELECT ON public.languages,
public.articles,
public.article_draft_translations,
public.article_revisions,
public.article_revision_translations,
public.public_routes TO sp_publisher;
GRANT INSERT,
UPDATE,
DELETE ON public.articles,
public.article_draft_translations TO sp_publisher;
GRANT INSERT ON public.article_revisions,
public.article_revision_translations,
public.public_routes TO sp_publisher;
GRANT UPDATE (sealed) ON public.article_revisions TO sp_publisher;
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
COMMENT ON TABLE public.articles IS
'Статьи с независимым черновиком и указателем на текущую '
'опубликованную ревизию.';
COMMENT ON COLUMN public.articles.id IS
'Стабильный идентификатор статьи, общий для всех её переводов и '
'ревизий.';
COMMENT ON COLUMN public.articles.version IS
'Версия статьи для проверки конкурентных изменений; начинается с '
'единицы.';
COMMENT ON COLUMN public.articles.published_revision_id IS
'Текущая собственная закрытая ревизия; NULL означает отсутствие '
'публикации.';
COMMENT ON TABLE public.article_draft_translations IS
'Редактируемые переводы черновика; содержимое недоступно публичной '
'роли.';
COMMENT ON COLUMN public.article_draft_translations.article_id IS
'Статья, которой принадлежит редактируемый перевод.';
COMMENT ON COLUMN public.article_draft_translations.language_code IS
'Язык редактируемого перевода из каталога установки.';
COMMENT ON COLUMN public.article_draft_translations.title IS
'Заголовок черновика; NULL допустим до готовности перевода.';
COMMENT ON COLUMN public.article_draft_translations.slug IS
'Предлагаемый адрес перевода; черновик не резервирует публичный '
'маршрут.';
COMMENT ON COLUMN public.article_draft_translations.body_markdown IS
'Текст черновика в Markdown; NULL допустим до готовности перевода.';
COMMENT ON COLUMN public.article_draft_translations.is_ready IS
'Редактор подтвердил полноту перевода; изменение содержимого '
'сбрасывает готовность.';
COMMENT ON TABLE public.article_revisions IS
'Снимки публикации статьи; после закрытия неизменяемы и сохраняются в '
'истории.';
COMMENT ON COLUMN public.article_revisions.id IS
'Идентификатор снимка публикации.';
COMMENT ON COLUMN public.article_revisions.article_id IS
'Статья-владелец снимка; внешний указатель публикации проверяет этого '
'владельца.';
COMMENT ON COLUMN public.article_revisions.revision_no IS
'Порядковый номер снимка внутри статьи, начиная с единицы.';
COMMENT ON COLUMN public.article_revisions.sealed IS
'Снимок закрыт для добавления и изменения переводов; незакрытый снимок '
'нельзя зафиксировать.';
COMMENT ON COLUMN public.article_revisions.created_at IS
'Время создания снимка по текущим часам PostgreSQL после ожидания '
'блокировок Publish; не время начала транзакции.';
COMMENT ON TABLE public.public_routes IS
'Постоянные резервирования адресов статьи; старый адрес ведёт к '
'текущему переводу владельца.';
COMMENT ON COLUMN public.public_routes.language_code IS
'Язык адреса; публичная выдача допускается только при включённом языке.';
COMMENT ON COLUMN public.public_routes.slug IS
'Навсегда зарезервированный адрес в пределах языка, включая '
'исторические alias.';
COMMENT ON COLUMN public.public_routes.article_id IS
'Владелец адреса; адрес нельзя передать другой статье или удалить.';
COMMENT ON TABLE public.article_revision_translations IS
'Неизменяемые переводы закрытых снимков; история не выдаётся публичной '
'роли.';
COMMENT ON COLUMN public.article_revision_translations.article_id IS
'Владелец перевода; должен совпадать с владельцем снимка и адреса.';
COMMENT ON COLUMN public.article_revision_translations.revision_id IS
'Снимок, в который включён перевод.';
COMMENT ON COLUMN public.article_revision_translations.language_code IS
'Язык перевода; в снимке допускается один перевод на язык.';
COMMENT ON COLUMN public.article_revision_translations.title IS
'Непустой заголовок перевода в момент публикации.';
COMMENT ON COLUMN public.article_revision_translations.slug IS
'Адрес перевода в данном снимке, закреплённый за той же статьёй.';
COMMENT ON COLUMN public.article_revision_translations.body_markdown IS
'Непустой текст Markdown в момент публикации.';
COMMENT ON FUNCTION public.reset_draft_ready() IS
'Сбрасывает готовность чернового перевода при изменении содержимого.';
COMMENT ON FUNCTION public.guard_revision_translation() IS
'Сериализует добавление перевода с закрытием снимка и запрещает '
'изменение закрытого снимка.';
COMMENT ON FUNCTION public.guard_revision() IS
'Запрещает изменение закрытого снимка, идентификаторов и закрытие '
'пустого снимка.';
COMMENT ON FUNCTION public.require_closed_revision() IS
'На границе транзакции запрещает сохранение незакрытого снимка.';
COMMENT ON FUNCTION public.guard_published_revision() IS
'Разрешает публикацию только собственной закрытой ревизии статьи.';
COMMENT ON FUNCTION public.keep_route_reservation() IS
'Сохраняет постоянное владение публичным адресом при изменении '
'публикации.';
COMMENT ON FUNCTION public.lock_languages(
    text[]
) IS
'Блокирует выбранные языки до конца транзакции публикации без выдачи '
'редактору права менять каталог.';
COMMENT ON TRIGGER article_drafts_reset_ready
ON public.article_draft_translations IS
'Сбрасывает готовность чернового перевода при изменении содержимого.';
COMMENT ON TRIGGER article_revision_translations_guard
ON public.article_revision_translations IS
'Сериализует добавление перевода с закрытием снимка и запрещает '
'изменение закрытого снимка.';
COMMENT ON TRIGGER article_revisions_guard ON public.article_revisions IS
'Запрещает изменение закрытого снимка, идентификаторов и закрытие '
'пустого снимка.';
COMMENT ON TRIGGER article_revisions_closed ON public.article_revisions IS
'На границе транзакции запрещает сохранение незакрытого снимка.';
COMMENT ON TRIGGER articles_published_guard ON public.articles IS
'Разрешает публикацию только собственной закрытой ревизии статьи.';
COMMENT ON TRIGGER public_routes_permanent ON public.public_routes IS
'Сохраняет постоянное владение публичным адресом при изменении '
'публикации.';
COMMENT ON INDEX public.article_revisions_owner_key IS
'Составной ключ проверяет совпадение владельца опубликованного снимка.';
COMMENT ON INDEX public.article_revisions_number_key IS
'Номер снимка уникален только внутри статьи.';
COMMENT ON INDEX public.public_routes_owner_key IS
'Составной ключ проверяет совпадение владельца адреса и перевода '
'снимка.';

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
CREATE VIEW public.public_article_translations WITH (security_barrier = true) AS
SELECT
    t.article_id,
    t.revision_id,
    t.language_code,
    t.title,
    t.slug,
    t.body_markdown,
    r.created_at AS published_at
FROM public.article_revision_translations AS t
INNER JOIN
    public.articles AS a
    ON t.article_id = a.id AND t.revision_id = a.published_revision_id
INNER JOIN
    public.article_revisions AS r
    ON t.revision_id = r.id AND t.article_id = r.article_id
INNER JOIN public.languages AS l ON t.language_code = l.code AND l.is_enabled;
CREATE VIEW public.public_article_routes WITH (security_barrier = true) AS
SELECT
    r.language_code,
    r.slug,
    r.article_id,
    t.slug AS current_slug,
    t.revision_id
FROM public.public_routes AS r
INNER JOIN
    public.public_article_translations AS t
    ON r.article_id = t.article_id AND r.language_code = t.language_code;
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
COMMENT ON VIEW public.public_article_translations IS
'Только текущие опубликованные переводы на включённых языках; история '
'и черновики скрыты.';
COMMENT ON COLUMN public.public_article_translations.article_id IS
'Стабильный владелец опубликованного перевода.';
COMMENT ON COLUMN public.public_article_translations.revision_id IS
'Текущая опубликованная ревизия статьи.';
COMMENT ON COLUMN public.public_article_translations.language_code IS
'Включённый язык опубликованного перевода.';
COMMENT ON COLUMN public.public_article_translations.title IS
'Текущий опубликованный заголовок.';
COMMENT ON COLUMN public.public_article_translations.slug IS
'Текущий опубликованный адрес перевода.';
COMMENT ON COLUMN public.public_article_translations.body_markdown IS
'Текущий опубликованный текст Markdown.';
COMMENT ON COLUMN public.public_article_translations.published_at IS
'Время создания текущего опубликованного снимка статьи; обновляется '
'при повторной публикации, общее для всех переводов снимка.';
COMMENT ON VIEW public.public_article_routes IS
'Публичные адреса и alias, разрешаемые к текущему переводу того же '
'владельца и языка.';
COMMENT ON COLUMN public.public_article_routes.language_code IS
'Включённый язык публичного адреса.';
COMMENT ON COLUMN public.public_article_routes.slug IS
'Запрошенный текущий адрес или исторический alias.';
COMMENT ON COLUMN public.public_article_routes.article_id IS
'Статья-владелец публичного адреса.';
COMMENT ON COLUMN public.public_article_routes.current_slug IS
'Текущий адрес перевода для разрешения alias.';
COMMENT ON COLUMN public.public_article_routes.revision_id IS
'Текущая опубликованная ревизия владельца адреса.';
REVOKE ALL ON public.public_languages,
public.public_article_translations,
public.public_article_routes FROM public;


CREATE TABLE public.ui_message_keys (
    message_key text CONSTRAINT ui_message_keys_pkey PRIMARY KEY,
    placeholders text[] NOT NULL DEFAULT '{}',
    CONSTRAINT ui_message_keys_key_check CHECK (
        message_key ~ '^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$'
    )
);
INSERT INTO public.ui_message_keys (message_key) VALUES
('articles'), ('empty'), ('pagination'), ('previous'), ('next'), ('navigation'),
('all'), ('languages'), ('errors.400'), ('errors.404'), ('errors.503'),
('errors.other'), ('errors.retry'), ('errors.home');
CREATE TABLE public.site_revisions (
    tagline text NOT NULL,
    notice text NOT NULL,
    accent text NOT NULL,
    CONSTRAINT site_revisions_tagline_check CHECK (length(tagline) <= 300),
    CONSTRAINT site_revisions_notice_check CHECK (length(notice) <= 2000),
    CONSTRAINT site_revisions_accent_check CHECK (accent ~ '^#[0-9a-fA-F]{6}$'),
    id uuid CONSTRAINT site_revisions_pkey PRIMARY KEY,
    title text NOT NULL,
    description text NOT NULL,
    default_language text NOT NULL,
    fallback_language text NOT NULL,
    sealed boolean NOT NULL DEFAULT false,
    CONSTRAINT site_revisions_title_check CHECK (
        length(title) <= 200
        AND btrim(
            title,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    ),
    CONSTRAINT site_revisions_description_check CHECK (
        length(description) <= 2000
    ),
    CONSTRAINT site_revisions_default_fkey FOREIGN KEY (
        default_language
    ) REFERENCES public.languages (code),
    CONSTRAINT site_revisions_fallback_fkey FOREIGN KEY (
        fallback_language
    ) REFERENCES public.languages (code)
);
CREATE TABLE public.site_state (
    singleton boolean CONSTRAINT site_state_pkey PRIMARY KEY DEFAULT true,
    version bigint NOT NULL DEFAULT 1,
    draft_id uuid,
    published_id uuid,
    CONSTRAINT site_state_singleton_check CHECK (singleton),
    CONSTRAINT site_state_version_check CHECK (version >= 1),
    CONSTRAINT site_state_draft_fkey FOREIGN KEY (
        draft_id
    ) REFERENCES public.site_revisions (id),
    CONSTRAINT site_state_published_fkey FOREIGN KEY (
        published_id
    ) REFERENCES public.site_revisions (id)
);
INSERT INTO public.site_state (singleton) VALUES (true);
CREATE TABLE public.site_revision_keys (
    revision_id uuid NOT NULL,
    message_key text NOT NULL,
    placeholders text[] NOT NULL,
    CONSTRAINT site_revision_keys_pkey PRIMARY KEY (revision_id, message_key),
    CONSTRAINT site_revision_keys_revision_fkey FOREIGN KEY (
        revision_id
    ) REFERENCES public.site_revisions (id)
);
CREATE TABLE public.site_revision_languages (
    revision_id uuid NOT NULL,
    language_code text NOT NULL,
    name text NOT NULL,
    native_name text NOT NULL,
    direction text NOT NULL,
    CONSTRAINT site_revision_languages_pkey PRIMARY KEY (
        revision_id, language_code
    ),
    CONSTRAINT site_revision_languages_revision_fkey FOREIGN KEY (
        revision_id
    ) REFERENCES public.site_revisions (id),
    CONSTRAINT site_revision_languages_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT site_revision_languages_direction_check CHECK (
        direction IN ('ltr', 'rtl')
    )
);
CREATE TABLE public.site_revision_messages (
    revision_id uuid NOT NULL,
    language_code text NOT NULL,
    message_key text NOT NULL,
    message_value text NOT NULL,
    CONSTRAINT site_revision_messages_pkey PRIMARY KEY (
        revision_id, language_code, message_key
    ),
    CONSTRAINT site_revision_messages_language_fkey FOREIGN KEY (
        revision_id, language_code
    ) REFERENCES public.site_revision_languages (revision_id, language_code),
    CONSTRAINT site_revision_messages_key_fkey FOREIGN KEY (
        revision_id, message_key
    ) REFERENCES public.site_revision_keys (revision_id, message_key),
    CONSTRAINT site_revision_messages_value_check CHECK (
        length(message_value) <= 4000
        AND btrim(
            message_value,
            E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
            '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
            '\u200a\u2028\u2029\u202f\u205f\u3000'
        )
        <> ''
    )
);
-- +goose StatementBegin
CREATE FUNCTION public.ui_placeholders(
    value text
) RETURNS text[] LANGUAGE sql IMMUTABLE
SET search_path = pg_catalog, pg_temp AS $$
 SELECT COALESCE(array_agg(DISTINCT m[1] ORDER BY m[1]),'{}'::text[])
 FROM regexp_matches(value,'\{([a-z][a-z0-9_]*)\}','g') AS m
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.guard_site_child() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
DECLARE closed boolean; owner_id uuid;
BEGIN
 owner_id := CASE WHEN TG_OP='DELETE' THEN OLD.revision_id ELSE NEW.revision_id END;
 IF TG_OP='UPDATE' AND NEW.revision_id<>OLD.revision_id THEN
  RAISE EXCEPTION 'владелец снимка неизменяем' USING ERRCODE='23514';
 END IF;
 SELECT sealed INTO closed FROM public.site_revisions WHERE id=owner_id FOR UPDATE;
 IF closed IS DISTINCT FROM false THEN
  RAISE EXCEPTION 'опубликованный снимок неизменяем' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.guard_site_revision() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.sealed THEN RAISE EXCEPTION 'новый снимок обязан быть черновиком' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.sealed OR (TG_OP='UPDATE' AND NEW.id<>OLD.id) THEN
  RAISE EXCEPTION 'опубликованный снимок неизменяем' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.sealed THEN
  -- Блокировка языков согласована с импортом и исключает отключение во время публикации.
  PERFORM public.lock_languages(ARRAY(SELECT code FROM public.languages
   WHERE code IN (NEW.default_language,NEW.fallback_language)
   OR code IN (SELECT language_code FROM public.site_revision_languages WHERE revision_id=NEW.id) ORDER BY code));
  IF NOT EXISTS(SELECT 1 FROM public.languages WHERE code=NEW.default_language AND is_enabled)
   OR NOT EXISTS(SELECT 1 FROM public.site_revision_languages WHERE revision_id=NEW.id AND language_code=NEW.fallback_language)
   OR EXISTS(SELECT 1 FROM public.site_revision_languages c JOIN public.languages l ON l.code=c.language_code
    WHERE c.revision_id=NEW.id AND (NOT l.is_enabled OR (c.name,c.native_name,c.direction) IS DISTINCT FROM (l.name,l.native_name,l.direction)))
   OR NOT EXISTS(SELECT 1 FROM public.site_revision_keys WHERE revision_id=NEW.id)
   OR EXISTS((SELECT message_key,placeholders FROM public.ui_message_keys EXCEPT SELECT message_key,placeholders FROM public.site_revision_keys WHERE revision_id=NEW.id)
    UNION ALL (SELECT message_key,placeholders FROM public.site_revision_keys WHERE revision_id=NEW.id EXCEPT SELECT message_key,placeholders FROM public.ui_message_keys))
   OR EXISTS(SELECT 1 FROM public.site_revision_languages c CROSS JOIN public.site_revision_keys k
    LEFT JOIN public.site_revision_messages m ON m.revision_id=c.revision_id AND m.language_code=c.language_code AND m.message_key=k.message_key
    WHERE c.revision_id=NEW.id AND k.revision_id=NEW.id AND (m.message_key IS NULL
     OR public.ui_placeholders(m.message_value)<>k.placeholders
     OR regexp_replace(m.message_value,'\{[a-z][a-z0-9_]*\}','','g') ~ '[{}]')) THEN
   RAISE EXCEPTION 'неполная или некорректная публикация UI' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.guard_site_state() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'состояние установки обязательно' USING ERRCODE='23514'; END IF;
 IF NEW.version<>OLD.version+1 THEN RAISE EXCEPTION 'неверная версия установки' USING ERRCODE='23514'; END IF;
 IF NEW.published_id IS NOT NULL THEN
  PERFORM 1 FROM public.site_revisions WHERE id=NEW.published_id AND sealed FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'публикация не закрыта' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.draft_id IS NOT NULL AND EXISTS(SELECT 1 FROM public.site_revisions WHERE id=NEW.draft_id AND sealed) THEN
  RAISE EXCEPTION 'черновик уже закрыт' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER site_revisions_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revisions
FOR EACH ROW EXECUTE FUNCTION public.guard_site_revision();
CREATE TRIGGER site_state_guard
BEFORE UPDATE OR DELETE
ON public.site_state
FOR EACH ROW EXECUTE FUNCTION public.guard_site_state();
CREATE TRIGGER site_revision_keys_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_keys
FOR EACH ROW EXECUTE FUNCTION public.guard_site_child();
CREATE TRIGGER site_revision_languages_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_languages
FOR EACH ROW EXECUTE FUNCTION public.guard_site_child();
CREATE TRIGGER site_revision_messages_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_messages
FOR EACH ROW EXECUTE FUNCTION public.guard_site_child();
CREATE VIEW public.public_site WITH (security_barrier = true) AS
SELECT
    r.id,
    r.title,
    r.description,
    r.default_language,
    r.fallback_language,
    r.tagline,
    r.notice,
    r.accent
FROM public.site_state AS s
INNER JOIN public.site_revisions AS r ON s.published_id = r.id AND r.sealed
INNER JOIN public.languages AS d ON r.default_language = d.code AND d.is_enabled
INNER JOIN
    public.languages AS f
    ON r.fallback_language = f.code AND f.is_enabled;
CREATE VIEW public.public_ui_languages WITH (security_barrier = true) AS
SELECT
    c.revision_id,
    c.language_code,
    c.name,
    c.native_name,
    c.direction
FROM public.site_revision_languages AS c
INNER JOIN public.public_site AS s ON c.revision_id = s.id
INNER JOIN public.languages AS l ON c.language_code = l.code AND l.is_enabled;
CREATE VIEW public.public_ui_messages WITH (security_barrier = true) AS
SELECT
    m.revision_id,
    m.language_code,
    m.message_key,
    m.message_value
FROM public.site_revision_messages AS m
INNER JOIN
    public.public_ui_languages AS c
    ON m.revision_id = c.revision_id AND m.language_code = c.language_code;
COMMENT ON TABLE public.ui_message_keys IS
'Обязательные UI-ключи и сигнатуры текущего приложения; значения '
'задаёт редактор.';
COMMENT ON COLUMN public.ui_message_keys.message_key IS
'Машинный ключ обязательного сообщения, включая вложенные ключи через'
' точку.';
COMMENT ON COLUMN public.ui_message_keys.placeholders IS
'Упорядоченный набор уникальных имён подстановок; пустой массив '
'запрещает подстановки.';
COMMENT ON TABLE public.site_revisions IS
'Типизированный черновик или неизменяемый опубликованный снимок '
'настроек и UI.';
COMMENT ON COLUMN public.site_revisions.id IS
'Случайный UUID общей версии настроек и каталога.';
COMMENT ON COLUMN public.site_revisions.title IS
'Публичное название установки, от 1 до 200 символов.';
COMMENT ON COLUMN public.site_revisions.description IS
'Публичное описание установки, до 2000 символов.';
COMMENT ON COLUMN public.site_revisions.default_language IS
'Включённый язык контента по умолчанию.';
COMMENT ON COLUMN public.site_revisions.fallback_language IS
'Включённый язык полного запасного UI-каталога.';
COMMENT ON COLUMN public.site_revisions.sealed IS
'Признак проверенного неизменяемого снимка; false означает черновик.';
COMMENT ON TABLE public.site_state IS
'Единственная точка атомарного переключения публикации и контроля '
'конкурирующих команд.';
COMMENT ON COLUMN public.site_state.singleton IS
'Единственная строка состояния, допустимо только true.';
COMMENT ON COLUMN public.site_state.version IS
'Редакционная optimistic версия, увеличивается каждой успешной '
'командой.';
COMMENT ON COLUMN public.site_state.draft_id IS
'Текущий черновик; NULL после публикации или до первого сохранения.';
COMMENT ON COLUMN public.site_state.published_id IS
'Текущий закрытый снимок; NULL до первой утверждённой публикации.';
COMMENT ON TABLE public.site_revision_keys IS
'Зафиксированные определения обязательных ключей конкретной версии '
'UI.';
COMMENT ON COLUMN public.site_revision_keys.revision_id IS
'Снимок-владелец определения ключа.';
COMMENT ON COLUMN public.site_revision_keys.message_key IS
'Машинный ключ сообщения в этой версии.';
COMMENT ON COLUMN public.site_revision_keys.placeholders IS
'Упорядоченная сигнатура подстановок, зафиксированная при сохранении.';
COMMENT ON TABLE public.site_revision_languages IS
'Метаданные языков UI, включённых в конкретный снимок.';
COMMENT ON COLUMN public.site_revision_languages.revision_id IS
'Версия снимка, которой принадлежат зафиксированные метаданные UI-языка.';
COMMENT ON COLUMN public.site_revision_languages.language_code IS
'Нормализованный код языка из динамического справочника.';
COMMENT ON COLUMN public.site_revision_languages.name IS
'Название языка на момент сохранения.';
COMMENT ON COLUMN public.site_revision_languages.native_name IS
'Самоназвание языка на момент сохранения.';
COMMENT ON COLUMN public.site_revision_languages.direction IS
'Направление письма ltr или rtl на момент сохранения.';
COMMENT ON TABLE public.site_revision_messages IS
'Редакторские сообщения черновика или закрытой версии UI; публичны '
'только текущие.';
COMMENT ON COLUMN public.site_revision_messages.revision_id IS
'Версия снимка, содержащего текст сообщения и его определение ключа.';
COMMENT ON COLUMN public.site_revision_messages.language_code IS
'Язык каталога сообщения.';
COMMENT ON COLUMN public.site_revision_messages.message_key IS
'Обязательный ключ сообщения этой версии.';
COMMENT ON COLUMN public.site_revision_messages.message_value IS
'Простой текст длиной до 4000 символов; подстановки имеют вид {name}.';
COMMENT ON VIEW public.public_site IS
'Только текущие опубликованные настройки с включёнными default и '
'fallback.';
COMMENT ON COLUMN public.public_site.id IS
'Случайный UUID общей версии настроек и каталога.';
COMMENT ON COLUMN public.public_site.title IS
'Публичное название установки, от 1 до 200 символов.';
COMMENT ON COLUMN public.public_site.description IS
'Публичное описание установки, до 2000 символов.';
COMMENT ON COLUMN public.public_site.default_language IS
'Включённый язык контента по умолчанию.';
COMMENT ON COLUMN public.public_site.fallback_language IS
'Включённый язык полного запасного UI-каталога.';
COMMENT ON VIEW public.public_ui_languages IS
'Включённые UI-языки текущей публикации с зафиксированными '
'метаданными.';
COMMENT ON COLUMN public.public_ui_languages.revision_id IS
'Текущая опубликованная версия метаданных доступного UI-языка.';
COMMENT ON COLUMN public.public_ui_languages.language_code IS
'Нормализованный код языка из динамического справочника.';
COMMENT ON COLUMN public.public_ui_languages.name IS
'Название языка на момент сохранения.';
COMMENT ON COLUMN public.public_ui_languages.native_name IS
'Самоназвание языка на момент сохранения.';
COMMENT ON COLUMN public.public_ui_languages.direction IS
'Направление письма ltr или rtl на момент сохранения.';
COMMENT ON VIEW public.public_ui_messages IS
'Полные сообщения только текущей публикации на включённых языках.';
COMMENT ON COLUMN public.public_ui_messages.revision_id IS
'Текущая опубликованная версия каталога, из которого выдано сообщение.';
COMMENT ON COLUMN public.public_ui_messages.language_code IS
'Язык каталога сообщения.';
COMMENT ON COLUMN public.public_ui_messages.message_key IS
'Обязательный ключ сообщения этой версии.';
COMMENT ON COLUMN public.public_ui_messages.message_value IS
'Простой текст длиной до 4000 символов; подстановки имеют вид {name}.';
COMMENT ON FUNCTION public.ui_placeholders(
    text
) IS
'Извлекает упорядоченные уникальные имена подстановок простого '
'текста.';
REVOKE ALL ON FUNCTION public.ui_placeholders(text) FROM public;
GRANT EXECUTE ON FUNCTION public.ui_placeholders(text) TO sp_publisher;
COMMENT ON FUNCTION public.guard_site_child() IS
'Сериализует изменения каталога с закрытием снимка и запрещает '
'изменение публикаций.';
REVOKE ALL ON FUNCTION public.guard_site_child() FROM public;
GRANT EXECUTE ON FUNCTION public.guard_site_child() TO sp_publisher;
COMMENT ON FUNCTION public.guard_site_revision() IS
'Закрывает только полный согласованный каталог и защищает '
'опубликованные версии.';
REVOKE ALL ON FUNCTION public.guard_site_revision() FROM public;
GRANT EXECUTE ON FUNCTION public.guard_site_revision() TO sp_publisher;
COMMENT ON FUNCTION public.guard_site_state() IS
'Защищает singleton, последовательность версий и ссылки только на '
'закрытые публикации.';
REVOKE ALL ON FUNCTION public.guard_site_state() FROM public;
GRANT EXECUTE ON FUNCTION public.guard_site_state() TO sp_publisher;
COMMENT ON TRIGGER site_revisions_guard
ON public.site_revisions IS
'Запрещает создание закрытого снимка и изменения после его '
'публикации; перед закрытием проверяет языки, полноту каталога и '
'подстановки.';
COMMENT ON TRIGGER site_state_guard
ON public.site_state IS
'Сохраняет единственную строку состояния, последовательность версий и'
' ссылку публикации только на закрытый снимок.';
COMMENT ON TRIGGER site_revision_keys_guard
ON public.site_revision_keys IS
'Блокирует снимок при изменении определений ключей и запрещает их '
'перенос между версиями или правку после закрытия.';
COMMENT ON TRIGGER site_revision_languages_guard
ON public.site_revision_languages IS
'Сериализует изменение метаданных UI-языков с закрытием снимка и '
'сохраняет неизменность опубликованного набора языков.';
COMMENT ON TRIGGER site_revision_messages_guard
ON public.site_revision_messages IS
'Защищает сообщения закрытого каталога от вставки, замены и удаления;'
' изменение черновика блокирует строку снимка до конца транзакции.';
REVOKE ALL ON public.ui_message_keys,
public.site_revisions,
public.site_state,
public.site_revision_keys,
public.site_revision_languages,
public.site_revision_messages FROM public;
GRANT SELECT ON public.ui_message_keys TO sp_publisher;
GRANT SELECT,
INSERT,
UPDATE,
DELETE ON public.site_revisions,
public.site_state,
public.site_revision_keys,
public.site_revision_languages,
public.site_revision_messages TO sp_publisher;
REVOKE ALL ON public.public_site,
public.public_ui_languages,
public.public_ui_messages FROM public;

COMMENT ON COLUMN public.site_revisions.tagline IS
'Публичный краткий девиз установки, до 300 символов.';
COMMENT ON COLUMN public.site_revisions.notice IS
'Публичное текстовое уведомление, до 2000 символов; пустое скрывает '
'блок.';
COMMENT ON COLUMN public.site_revisions.accent IS
'Акцент оформления в безопасном формате #RRGGBB, без CSS-выражений.';

COMMENT ON COLUMN public.public_site.tagline IS
'Публичный краткий девиз установки, до 300 символов.';
COMMENT ON COLUMN public.public_site.notice IS
'Публичное текстовое уведомление, до 2000 символов; пустое скрывает '
'блок.';
COMMENT ON COLUMN public.public_site.accent IS
'Акцент оформления в безопасном формате #RRGGBB, без CSS-выражений.';

-- Bootstrap is owner-only; ordinary authentication and commands remain dormant.
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
    CONSTRAINT sessions_kind_check CHECK (kind = 'preauth' AND user_id IS null),
    CONSTRAINT sessions_time_check CHECK (
        last_seen >= created_at AND expires_at > last_seen
        AND expires_at <= created_at + interval '5 minutes'
        AND (revoked_at IS null OR revoked_at >= created_at)
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
            'bootstrap.denied'
        )
    ),
    CONSTRAINT security_audit_result_check CHECK (
        result IN ('success', 'denied', 'failure')
    ),
    CONSTRAINT security_audit_reason_check CHECK (
        reason IN (
            '', 'operator_enrollment', 'operator_restart', 'totp_verified',
            'invalid_credentials'
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
'Positive credential generation reserved for future session '
'revocation checks.';
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
'Only restricted preauth storage exists; full and enrollment sessions '
'are not implemented.';
COMMENT ON COLUMN public.sessions.csrf_digest IS
'SHA-256 digest of a separate CSRF token; never a plaintext bearer.';
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
CREATE TRIGGER content_types_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.content_types
FOR EACH STATEMENT EXECUTE FUNCTION public.cms_write_mutex();
CREATE TRIGGER content_fields_write_mutex
BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE ON public.content_fields
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
public.content_types,
public.content_fields FROM public,
sp_publisher,
sp_language_importer;


-- +goose Down
DROP TABLE public.security_audit;
DROP TABLE public.recovery_codes;
DROP TABLE public.mfa_totp;
DROP TABLE public.security_state;
DROP TABLE public.grant_fields;

DROP TABLE public.sessions;
DROP TABLE public.role_grants;
DROP TABLE public.content_fields;
DROP TABLE public.content_types;
DROP TABLE public.permission_operations;
DROP TABLE public.user_roles;
DROP TABLE public.roles;
DROP TABLE public.users;
DROP FUNCTION public.cms_write_mutex();
DROP FUNCTION public.guard_security_state();
DROP FUNCTION public.guard_security_audit();
DROP FUNCTION public.guard_user_activation();

DROP VIEW public.public_ui_messages;
DROP VIEW public.public_ui_languages;
DROP VIEW public.public_site;
DROP TABLE public.site_state;
DROP TABLE public.site_revision_messages;
DROP TABLE public.site_revision_languages;
DROP TABLE public.site_revision_keys;
DROP TABLE public.site_revisions;
DROP TABLE public.ui_message_keys;
DROP FUNCTION public.guard_site_state();
DROP FUNCTION public.guard_site_revision();
DROP FUNCTION public.guard_site_child();
DROP FUNCTION public.ui_placeholders(text);

DROP VIEW public.public_article_routes;
DROP VIEW public.public_article_translations;
DROP VIEW public.public_languages;
DROP TABLE public.article_revision_translations;
DROP TABLE public.public_routes;
ALTER TABLE public.articles DROP CONSTRAINT articles_published_revision_fkey;
DROP TABLE public.article_revisions;
DROP TABLE public.article_draft_translations;
DROP TABLE public.articles;
DROP FUNCTION public.lock_languages(text[]);
DROP TABLE public.languages;
DROP FUNCTION public.keep_route_reservation();
DROP FUNCTION public.guard_published_revision();
DROP FUNCTION public.require_closed_revision();
DROP FUNCTION public.guard_revision();
DROP FUNCTION public.guard_revision_translation();
DROP FUNCTION public.reset_draft_ready();
