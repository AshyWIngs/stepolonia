-- +goose Up
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

REVOKE ALL ON public.articles,
public.article_draft_translations,
public.article_revisions,
public.article_revision_translations,
public.public_routes FROM public;

GRANT SELECT ON public.articles,
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

REVOKE ALL ON public.public_article_translations,
public.public_article_routes FROM public;

-- +goose Down
DROP VIEW public.public_article_routes;
DROP VIEW public.public_article_translations;
DROP TABLE public.article_revision_translations;
DROP TABLE public.public_routes;
ALTER TABLE public.articles DROP CONSTRAINT articles_published_revision_fkey;
DROP TABLE public.article_revisions;
DROP TABLE public.article_draft_translations;
DROP TABLE public.articles;
DROP FUNCTION public.keep_route_reservation();
DROP FUNCTION public.guard_published_revision();
DROP FUNCTION public.require_closed_revision();
DROP FUNCTION public.guard_revision();
DROP FUNCTION public.guard_revision_translation();
DROP FUNCTION public.reset_draft_ready();
