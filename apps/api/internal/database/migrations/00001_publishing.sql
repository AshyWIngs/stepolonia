-- +goose Up
-- Match Go strings.TrimSpace Unicode White_Space independently of DB locale.
CREATE TABLE public.languages (
 code text COLLATE "C" CONSTRAINT languages_pkey PRIMARY KEY,
 name text NOT NULL,
 native_name text NOT NULL,
 direction text NOT NULL,
 is_enabled boolean NOT NULL DEFAULT false,
 CONSTRAINT languages_code_check CHECK (length(code) BETWEEN 2 AND 63 AND code = lower(code COLLATE "C") AND code ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
 CONSTRAINT languages_name_check CHECK (btrim(name, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> ''),
 CONSTRAINT languages_native_name_check CHECK (btrim(native_name, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> ''),
 CONSTRAINT languages_direction_check CHECK (direction IN ('ltr','rtl'))
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
 CONSTRAINT article_drafts_pkey PRIMARY KEY (article_id,language_code),
 CONSTRAINT article_drafts_article_fkey FOREIGN KEY (article_id) REFERENCES public.articles(id),
 CONSTRAINT article_drafts_language_fkey FOREIGN KEY (language_code) REFERENCES public.languages(code),
 CONSTRAINT article_drafts_ready_check CHECK (NOT is_ready OR (
 title IS NOT NULL AND btrim(title, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> '' AND
 slug IS NOT NULL AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 200 AND
 body_markdown IS NOT NULL AND btrim(body_markdown, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> ''))
);
CREATE TABLE public.article_revisions (
 id uuid CONSTRAINT article_revisions_pkey PRIMARY KEY,
 article_id uuid NOT NULL,
 revision_no bigint NOT NULL,
 sealed boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT current_timestamp,
 CONSTRAINT article_revisions_article_fkey FOREIGN KEY (article_id) REFERENCES public.articles(id),
 CONSTRAINT article_revisions_owner_key UNIQUE (article_id,id),
 CONSTRAINT article_revisions_number_key UNIQUE (article_id,revision_no),
 CONSTRAINT article_revisions_number_check CHECK (revision_no >= 1)
);
ALTER TABLE public.articles ADD CONSTRAINT articles_published_revision_fkey
 FOREIGN KEY (id,published_revision_id) REFERENCES public.article_revisions(article_id,id);
CREATE TABLE public.public_routes (
 language_code text COLLATE "C" NOT NULL,
 slug text COLLATE "C" NOT NULL,
 article_id uuid NOT NULL,
 CONSTRAINT public_routes_pkey PRIMARY KEY (language_code,slug),
 CONSTRAINT public_routes_owner_key UNIQUE (article_id,language_code,slug),
 CONSTRAINT public_routes_article_fkey FOREIGN KEY (article_id) REFERENCES public.articles(id),
 CONSTRAINT public_routes_language_fkey FOREIGN KEY (language_code) REFERENCES public.languages(code),
 CONSTRAINT public_routes_slug_check CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 200)
);
CREATE TABLE public.article_revision_translations (
 article_id uuid NOT NULL,
 revision_id uuid NOT NULL,
 language_code text COLLATE "C" NOT NULL,
 title text NOT NULL,
 slug text COLLATE "C" NOT NULL,
 body_markdown text NOT NULL,
 CONSTRAINT article_revision_translations_pkey PRIMARY KEY (revision_id,language_code),
 CONSTRAINT article_revision_translations_revision_fkey FOREIGN KEY (article_id,revision_id) REFERENCES public.article_revisions(article_id,id),
 CONSTRAINT article_revision_translations_language_fkey FOREIGN KEY (language_code) REFERENCES public.languages(code),
 CONSTRAINT article_revision_translations_route_fkey FOREIGN KEY (article_id,language_code,slug) REFERENCES public.public_routes(article_id,language_code,slug),
 CONSTRAINT article_revision_translations_title_check CHECK (btrim(title, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> ''),
 CONSTRAINT article_revision_translations_body_check CHECK (btrim(body_markdown, E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000') <> '')
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
CREATE TRIGGER article_drafts_reset_ready BEFORE UPDATE ON public.article_draft_translations FOR EACH ROW EXECUTE FUNCTION public.reset_draft_ready();
-- A row lock serializes inserts into a snapshot with its sealing transition.
-- +goose StatementBegin
CREATE FUNCTION public.guard_revision_translation() RETURNS trigger LANGUAGE plpgsql
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
CREATE TRIGGER article_revision_translations_guard BEFORE INSERT OR UPDATE OR DELETE ON public.article_revision_translations FOR EACH ROW EXECUTE FUNCTION public.guard_revision_translation();
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
CREATE TRIGGER article_revisions_guard BEFORE INSERT OR UPDATE OR DELETE ON public.article_revisions FOR EACH ROW EXECUTE FUNCTION public.guard_revision();
-- +goose StatementBegin
CREATE FUNCTION public.require_closed_revision() RETURNS trigger LANGUAGE plpgsql
 SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM public.article_revisions WHERE id = NEW.id AND NOT sealed) THEN
  RAISE EXCEPTION 'нельзя зафиксировать незакрытую ревизию' USING ERRCODE = '23514';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER article_revisions_closed AFTER INSERT OR UPDATE ON public.article_revisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.require_closed_revision();
-- +goose StatementBegin
CREATE FUNCTION public.guard_published_revision() RETURNS trigger LANGUAGE plpgsql
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
CREATE TRIGGER articles_published_guard BEFORE INSERT OR UPDATE OF published_revision_id,id ON public.articles FOR EACH ROW EXECUTE FUNCTION public.guard_published_revision();
-- +goose StatementBegin
CREATE FUNCTION public.keep_route_reservation() RETURNS trigger LANGUAGE plpgsql
 SET search_path = pg_catalog, pg_temp AS $$
BEGIN
 RAISE EXCEPTION 'резервирования публичных адресов постоянны' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER public_routes_permanent BEFORE UPDATE OR DELETE ON public.public_routes FOR EACH ROW EXECUTE FUNCTION public.keep_route_reservation();
-- Publisher cannot update language metadata. PostgreSQL FOR SHARE needs UPDATE;
-- this single static function supplies the lock without granting that privilege.
-- +goose StatementBegin
CREATE FUNCTION public.lock_languages(codes text[]) RETURNS SETOF public.languages
 LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
 SELECT l.* FROM public.languages AS l WHERE l.code = ANY(codes)
 ORDER BY l.code COLLATE "C" FOR SHARE OF l;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION public.lock_languages(text[]) FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON public.languages, public.articles, public.article_draft_translations, public.article_revisions, public.article_revision_translations, public.public_routes FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO sp_publisher, sp_language_importer;
GRANT SELECT ON public.languages, public.articles, public.article_draft_translations, public.article_revisions, public.article_revision_translations, public.public_routes TO sp_publisher;
GRANT INSERT, UPDATE, DELETE ON public.articles, public.article_draft_translations TO sp_publisher;
GRANT INSERT ON public.article_revisions, public.article_revision_translations, public.public_routes TO sp_publisher;
GRANT UPDATE(sealed) ON public.article_revisions TO sp_publisher;
GRANT EXECUTE ON FUNCTION public.lock_languages(text[]) TO sp_publisher;
GRANT SELECT ON public.languages TO sp_language_importer;
GRANT INSERT(code,name,native_name,direction), UPDATE(name,native_name,direction) ON public.languages TO sp_language_importer;

-- +goose Down
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
