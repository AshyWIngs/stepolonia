-- +goose Up
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

-- +goose Down
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
