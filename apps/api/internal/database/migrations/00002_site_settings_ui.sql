-- +goose Up
CREATE TABLE public.ui_message_keys (
    message_key text CONSTRAINT ui_message_keys_pkey PRIMARY KEY,
    placeholders text[] NOT NULL DEFAULT '{}',
    CONSTRAINT ui_message_keys_key_check CHECK (
        message_key ~ '^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$'
    )
);

CREATE TABLE public.initial_ui_messages (
    language_code text NOT NULL,
    message_key text NOT NULL,
    message_value text NOT NULL,
    CONSTRAINT initial_ui_messages_pkey PRIMARY KEY (
        language_code, message_key
    ),
    CONSTRAINT initial_ui_messages_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT initial_ui_messages_key_fkey FOREIGN KEY (
        message_key
    ) REFERENCES public.ui_message_keys (message_key)
);

CREATE TABLE public.initial_site_defaults (
    singleton boolean CONSTRAINT initial_site_defaults_pkey PRIMARY KEY,
    accent text NOT NULL,
    CONSTRAINT initial_site_defaults_singleton_check CHECK (singleton),
    CONSTRAINT initial_site_defaults_accent_check CHECK (
        accent ~ '^#[0-9a-fA-F]{6}$'
    )
);

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

CREATE TABLE public.site_revision_language_flags (
    revision_id uuid NOT NULL REFERENCES public.site_revisions (id),
    language_code text COLLATE "C" NOT NULL REFERENCES public.languages (code),
    svg text NOT NULL,
    CONSTRAINT site_revision_language_flags_pkey PRIMARY KEY (
        revision_id, language_code
    ),
    CONSTRAINT site_revision_language_flags_size_check CHECK (
        octet_length(svg) <= 4096
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
    ) REFERENCES public.site_revision_keys (revision_id, message_key)
);

-- +goose StatementBegin
CREATE FUNCTION public.valid_slug_rule(source_character text, replacement text)
RETURNS boolean LANGUAGE sql IMMUTABLE PARALLEL SAFE
SET search_path = pg_catalog, pg_temp AS $$
 SELECT length(source_character) = 1 AND ascii(source_character) > 127
  AND source_character = normalize(source_character)
  AND source_character = lower(source_character COLLATE pg_catalog.pg_unicode_fast)
  AND source_character COLLATE pg_catalog.unicode ~ '^[[:graph:]]$'
  -- ICU's graphical class includes Unicode private-use areas.
  AND ascii(source_character) NOT BETWEEN 57344 AND 63743
  AND ascii(source_character) NOT BETWEEN 983040 AND 1048573
  AND ascii(source_character) NOT BETWEEN 1048576 AND 1114109
  AND length(replacement) <= 16 AND replacement COLLATE "C" !~ '[^a-z0-9-]'
$$;
-- +goose StatementEnd

CREATE TABLE public.initial_slug_rules (
    language_code text COLLATE "C" NOT NULL,
    source_character text COLLATE "C" NOT NULL,
    replacement text NOT NULL,
    CONSTRAINT initial_slug_rules_pkey PRIMARY KEY (
        language_code, source_character
    ),
    CONSTRAINT initial_slug_rules_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT initial_slug_rules_rule_check CHECK (
        public.valid_slug_rule(source_character, replacement)
    )
);

CREATE TABLE public.site_revision_slug_rules (
    revision_id uuid NOT NULL,
    language_code text COLLATE "C" NOT NULL,
    source_character text COLLATE "C" NOT NULL,
    replacement text NOT NULL,
    CONSTRAINT site_revision_slug_rules_pkey PRIMARY KEY (
        revision_id, language_code, source_character
    ),
    CONSTRAINT site_revision_slug_rules_revision_fkey FOREIGN KEY (
        revision_id
    ) REFERENCES public.site_revisions (id),
    CONSTRAINT site_revision_slug_rules_language_fkey FOREIGN KEY (
        language_code
    ) REFERENCES public.languages (code),
    CONSTRAINT site_revision_slug_rules_rule_check CHECK (
        public.valid_slug_rule(source_character, replacement)
    )
);

-- +goose StatementBegin
DO $$
DECLARE
    -- Match Go strings.TrimSpace independently of the database locale.
    whitespace CONSTANT text := E' \t\n\r\f\u000b\u0085\u00a0\u1680\u2000\u2001'
        '\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009'
        '\u200a\u2028\u2029\u202f\u205f\u3000';
    field record;
BEGIN
    FOR field IN
        SELECT *
        FROM (VALUES
            ('initial_ui_messages', 'initial_ui_messages_value_check', 'message_value', 4000),
            ('site_revisions', 'site_revisions_title_check', 'title', 200),
            ('site_revision_messages', 'site_revision_messages_value_check', 'message_value', 4000)
        ) AS checks (table_name, constraint_name, column_name, max_chars)
    LOOP
        EXECUTE format(
            'ALTER TABLE public.%1$I ADD CONSTRAINT %2$I CHECK '
            '(length(%3$I) <= %4$s AND btrim(%3$I, %5$L) <> '''')',
            field.table_name, field.constraint_name, field.column_name,
            field.max_chars, whitespace
        );
    END LOOP;
END;
$$;
-- +goose StatementEnd

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
  RAISE EXCEPTION 'snapshot owner is immutable' USING ERRCODE='23514';
 END IF;
 SELECT sealed INTO closed FROM public.site_revisions WHERE id=owner_id FOR UPDATE;
 IF closed IS DISTINCT FROM false THEN
  RAISE EXCEPTION 'published snapshot is immutable' USING ERRCODE='23514';
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
  IF NEW.sealed THEN RAISE EXCEPTION 'new snapshot must be a draft' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.sealed OR (TG_OP='UPDATE' AND NEW.id<>OLD.id) THEN
  RAISE EXCEPTION 'published snapshot is immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.sealed THEN
  IF (SELECT count(*) FROM public.site_revision_slug_rules WHERE revision_id=NEW.id)>512 THEN
   RAISE EXCEPTION 'too many slug rules to publish' USING ERRCODE='23514';
  END IF;
  -- Hold language locks through publication so eligible languages cannot change.
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
   RAISE EXCEPTION 'incomplete or invalid UI publication' USING ERRCODE='23514';
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
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'installation state is required' USING ERRCODE='23514'; END IF;
 IF NEW.version<>OLD.version+1 THEN RAISE EXCEPTION 'invalid installation version' USING ERRCODE='23514'; END IF;
 IF NEW.published_id IS NOT NULL THEN
  PERFORM 1 FROM public.site_revisions WHERE id=NEW.published_id AND sealed FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION 'publication is not sealed' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.draft_id IS NOT NULL AND EXISTS(SELECT 1 FROM public.site_revisions WHERE id=NEW.draft_id AND sealed) THEN
  RAISE EXCEPTION 'draft is already sealed' USING ERRCODE='23514';
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

CREATE TRIGGER site_revision_language_flags_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_language_flags
FOR EACH ROW EXECUTE FUNCTION public.guard_site_child();

CREATE TRIGGER site_revision_messages_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_messages
FOR EACH ROW EXECUTE FUNCTION public.guard_site_child();

CREATE TRIGGER site_revision_slug_rules_guard
BEFORE INSERT OR UPDATE OR DELETE
ON public.site_revision_slug_rules
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

CREATE VIEW public.public_slug_rules WITH (security_barrier = true) AS
SELECT
    r.revision_id,
    r.language_code,
    r.source_character,
    r.replacement
FROM public.site_revision_slug_rules AS r
INNER JOIN public.public_site AS s ON r.revision_id = s.id;

CREATE VIEW public.public_language_flags WITH (security_barrier = true) AS
SELECT
    f.revision_id,
    f.language_code,
    f.svg
FROM public.site_revision_language_flags AS f
INNER JOIN public.public_site AS s ON f.revision_id = s.id
INNER JOIN public.languages AS l ON f.language_code = l.code AND l.is_enabled
WHERE f.svg <> '';

COMMENT ON TABLE public.ui_message_keys IS
'Application-required message keys with their placeholder signatures.';

COMMENT ON COLUMN public.ui_message_keys.message_key IS
'Required message identifier. Dots separate nested keys.';

COMMENT ON COLUMN public.ui_message_keys.placeholders IS
'Ordered, unique placeholder names. An empty array permits no placeholders.';

COMMENT ON TABLE public.site_revisions IS
'Draft or sealed snapshot of site settings and the UI catalog.';

COMMENT ON COLUMN public.site_revisions.default_language IS
'Default language for content.';

COMMENT ON COLUMN public.site_revisions.fallback_language IS
'Language of the fallback UI catalog.';

COMMENT ON COLUMN public.site_revisions.sealed IS
'True after validation seals the revision against further changes.';

COMMENT ON TABLE public.site_state IS
'Singleton holding the current draft and publication pointers.';

COMMENT ON COLUMN public.site_state.version IS
'Optimistic version incremented on draft saves and publication.';

COMMENT ON COLUMN public.site_state.draft_id IS
'NULL before the first save or after publication.';

COMMENT ON COLUMN public.site_state.published_id IS
'NULL until the first publication.';

COMMENT ON TABLE public.site_revision_keys IS
'Required UI keys and placeholder signatures captured for this revision.';

COMMENT ON TABLE public.site_revision_languages IS
'UI language metadata captured for this revision.';

COMMENT ON TABLE public.site_revision_language_flags IS
'Language flags captured for one settings revision.';

COMMENT ON COLUMN public.site_revision_language_flags.svg IS
'Static SVG image. An empty value uses the standard globe.';

COMMENT ON VIEW public.public_language_flags IS
'Flags for enabled languages in the active publication.';

COMMENT ON TABLE public.site_revision_messages IS
'Editor-authored messages for a draft or sealed UI catalog.';

COMMENT ON TABLE public.site_revision_slug_rules IS
'Character replacement for slug suggestions in one settings revision.';

COMMENT ON COLUMN public.site_revision_slug_rules.source_character IS
'Source stored as one lowercase NFC Unicode character.';

COMMENT ON COLUMN public.site_revision_slug_rules.replacement IS
'An empty replacement removes the source character from the suggestion.';

COMMENT ON COLUMN public.site_revision_messages.message_value IS
'Plain text with optional {name} placeholders.';

COMMENT ON VIEW public.public_site IS
'Current sealed revision with both configured languages enabled.';

COMMENT ON VIEW public.public_ui_languages IS
'Enabled UI languages with metadata from the current publication.';

COMMENT ON VIEW public.public_ui_messages IS
'Messages for enabled UI languages in the current publication.';

COMMENT ON VIEW public.public_slug_rules IS
'Language-specific rules from the current sealed settings publication.';

COMMENT ON FUNCTION public.valid_slug_rule(text, text) IS
'Validates a canonical Unicode source character and its ASCII replacement.';

REVOKE ALL ON FUNCTION public.valid_slug_rule(text, text) FROM public;

GRANT EXECUTE ON FUNCTION public.valid_slug_rule(text, text) TO sp_publisher;

COMMENT ON FUNCTION public.ui_placeholders(
    text
) IS
'Extracts ordered, unique placeholder names from plain text.';

REVOKE ALL ON FUNCTION public.ui_placeholders(text) FROM public;

GRANT EXECUTE ON FUNCTION public.ui_placeholders(text) TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_child() IS
'Locks the parent revision and prevents changes to sealed settings data.';

REVOKE ALL ON FUNCTION public.guard_site_child() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_child() TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_revision() IS
'Validates the catalog and slug rule limit and protects sealed revisions.';

REVOKE ALL ON FUNCTION public.guard_site_revision() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_revision() TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_state() IS
'Protects the singleton version and enforces draft and publication states.';

REVOKE ALL ON FUNCTION public.guard_site_state() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_state() TO sp_publisher;

REVOKE ALL ON public.initial_site_defaults,
public.initial_ui_messages,
public.initial_slug_rules,
public.ui_message_keys,
public.site_revisions,
public.site_state,
public.site_revision_keys,
public.site_revision_languages,
public.site_revision_language_flags,
public.site_revision_messages,
public.site_revision_slug_rules FROM public;

GRANT SELECT ON public.ui_message_keys TO sp_publisher;

GRANT SELECT ON public.initial_slug_rules TO sp_publisher;

GRANT SELECT,
INSERT,
UPDATE,
DELETE ON public.site_revisions,
public.site_state,
public.site_revision_keys,
public.site_revision_languages,
public.site_revision_language_flags,
public.site_revision_messages,
public.site_revision_slug_rules TO sp_publisher;

REVOKE ALL ON public.public_site,
public.public_ui_languages,
public.public_ui_messages,
public.public_language_flags,
public.public_slug_rules FROM public;

COMMENT ON TABLE public.initial_site_defaults IS
'Portal settings defaults used before the first site publication.';

COMMENT ON TABLE public.initial_ui_messages IS
'UI messages for the initial site publication, readable only by the owner.';

COMMENT ON TABLE public.initial_slug_rules IS
'Language-specific slug rules for the first settings revision.';

COMMENT ON COLUMN public.site_revisions.notice IS
'An empty value hides the public notice.';

COMMENT ON COLUMN public.site_revisions.accent IS
'Theme accent as a hexadecimal RGB color (#RRGGBB).';

-- +goose Down
DROP VIEW public.public_language_flags;
DROP VIEW public.public_slug_rules;
DROP VIEW public.public_ui_messages;
DROP VIEW public.public_ui_languages;
DROP VIEW public.public_site;
DROP TABLE public.site_state;
DROP TABLE public.site_revision_slug_rules;
DROP TABLE public.site_revision_messages;
DROP TABLE public.site_revision_language_flags;
DROP TABLE public.site_revision_languages;
DROP TABLE public.site_revision_keys;
DROP TABLE public.site_revisions;
DROP TABLE public.initial_site_defaults;
DROP TABLE public.initial_ui_messages;
DROP TABLE public.initial_slug_rules;
DROP TABLE public.ui_message_keys;
DROP FUNCTION public.guard_site_state();
DROP FUNCTION public.guard_site_revision();
DROP FUNCTION public.guard_site_child();
DROP FUNCTION public.ui_placeholders(text);
DROP FUNCTION public.valid_slug_rule(text, text);
