-- +goose Up
INSERT INTO public.initial_site_defaults (singleton, accent)
VALUES (true, '#2563eb');

UPDATE public.languages AS l
SET
    initial_flag_svg = '<svg xmlns="http://www.w3.org/2000/svg" '
    || 'viewBox="0 0 60 36">' || f.shapes || '</svg>'
FROM (
    VALUES
    (
        'en',
        '<path fill="#012169" d="M0 0h60v36H0z"></path>'
        || '<path stroke="#ffffff" stroke-width="7" '
        || 'd="M0 0l60 36M60 0L0 36"></path>'
        || '<path stroke="#c8102e" stroke-width="2.5" '
        || 'd="M0 0l60 36M60 0L0 36"></path>'
        || '<path stroke="#ffffff" stroke-width="12" '
        || 'd="M30 0v36M0 18h60"></path>'
        || '<path stroke="#c8102e" stroke-width="7" '
        || 'd="M30 0v36M0 18h60"></path>'
    ),
    (
        'kk',
        '<rect width="60" height="36" fill="#00afca"></rect>'
        || '<path fill="#fec50c" d="M5 3h2v30H5zM3 8l3-3 3 3-3 3z'
        || 'M3 18l3-3 3 3-3 3zM3 28l3-3 3 3-3 3z"></path>'
        || '<circle cx="34" cy="14" r="6" fill="#fec50c"></circle>'
        || '<path stroke="#fec50c" stroke-width="1.4" '
        || 'd="M34 4v2m0 16v2M24 14h2m16 0h2M27 7l1 1m12 12 1 1'
        || 'M41 7l-1 1M28 20l-1 1"></path>'
        || '<path fill="#fec50c" d="M19 22l10 3 5 2 5-2 10-3-5 5'
        || '-8 1-2 3-2-3-8-1z"></path>'
    ),
    (
        'pl',
        '<rect width="60" height="18" fill="#ffffff"></rect>'
        || '<rect y="18" width="60" height="18" fill="#dc143c"></rect>'
    ),
    (
        'ru',
        '<rect width="60" height="12" fill="#ffffff"></rect>'
        || '<rect y="12" width="60" height="12" fill="#0039a6"></rect>'
        || '<rect y="24" width="60" height="12" fill="#d52b1e"></rect>'
    )
) AS f (code, shapes)
WHERE l.code = f.code;

-- +goose StatementBegin
DO $$
DECLARE
    -- The application embeds the domain catalogs into this literal before Goose runs the seed.
    catalog CONSTANT text := $catalog$
<embedded UI catalog>
$catalog$;
    entry text[];
BEGIN
    FOR entry IN
        SELECT string_to_array(block, E'\n')
        FROM string_to_table(btrim(catalog, E'\n'), E'\n\n') AS blocks (block)
    LOOP
        IF cardinality(entry) <> 5 THEN
            RAISE EXCEPTION 'initial UI message % requires four translations',
                entry[1];
        END IF;

        INSERT INTO public.ui_message_keys (message_key, placeholders)
        VALUES (entry[1], public.ui_placeholders(entry[2]));

        INSERT INTO public.initial_ui_messages (
            language_code, message_key, message_value
        ) VALUES
            ('en', entry[1], entry[2]),
            ('pl', entry[1], entry[3]),
            ('ru', entry[1], entry[4]),
            ('kk', entry[1], entry[5]);
    END LOOP;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'initial UI catalog is empty';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.initial_ui_messages AS message
        JOIN public.ui_message_keys AS key USING (message_key)
        WHERE public.ui_placeholders(message.message_value) <> key.placeholders
           OR regexp_replace(message.message_value, '\{[a-z][a-z0-9_]*\}', '', 'g') ~ '[{}]'
    ) THEN
        RAISE EXCEPTION 'initial UI messages have invalid placeholders';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Seed the content-language alphabets used by the demo portal.
INSERT INTO public.initial_slug_rules (
    language_code, source_character, replacement
)
VALUES
('kk', 'а', 'a'),
('kk', 'б', 'b'),
('kk', 'в', 'v'),
('kk', 'г', 'g'),
('kk', 'д', 'd'),
('kk', 'е', 'e'),
('kk', 'ж', 'zh'),
('kk', 'з', 'z'),
('kk', 'и', 'i'),
('kk', 'й', 'y'),
('kk', 'к', 'k'),
('kk', 'л', 'l'),
('kk', 'м', 'm'),
('kk', 'н', 'n'),
('kk', 'о', 'o'),
('kk', 'п', 'p'),
('kk', 'р', 'r'),
('kk', 'с', 's'),
('kk', 'т', 't'),
('kk', 'у', 'u'),
('kk', 'ф', 'f'),
('kk', 'х', 'kh'),
('kk', 'ц', 'ts'),
('kk', 'ч', 'ch'),
('kk', 'ш', 'sh'),
('kk', 'щ', 'shch'),
('kk', 'ъ', ''),
('kk', 'ы', 'y'),
('kk', 'ь', ''),
('kk', 'э', 'e'),
('kk', 'ю', 'yu'),
('kk', 'я', 'ya'),
('kk', 'ё', 'yo'),
('kk', 'і', 'i'),
('kk', 'ғ', 'gh'),
('kk', 'қ', 'q'),
('kk', 'ң', 'ng'),
('kk', 'ү', 'u'),
('kk', 'ұ', 'u'),
('kk', 'һ', 'h'),
('kk', 'ә', 'a'),
('kk', 'ө', 'o'),
('pl', 'ą', 'a'),
('pl', 'ć', 'c'),
('pl', 'ę', 'e'),
('pl', 'ł', 'l'),
('pl', 'ń', 'n'),
('pl', 'ó', 'o'),
('pl', 'ś', 's'),
('pl', 'ź', 'z'),
('pl', 'ż', 'z'),
('ru', 'а', 'a'),
('ru', 'б', 'b'),
('ru', 'в', 'v'),
('ru', 'г', 'g'),
('ru', 'д', 'd'),
('ru', 'е', 'e'),
('ru', 'ж', 'zh'),
('ru', 'з', 'z'),
('ru', 'и', 'i'),
('ru', 'й', 'y'),
('ru', 'к', 'k'),
('ru', 'л', 'l'),
('ru', 'м', 'm'),
('ru', 'н', 'n'),
('ru', 'о', 'o'),
('ru', 'п', 'p'),
('ru', 'р', 'r'),
('ru', 'с', 's'),
('ru', 'т', 't'),
('ru', 'у', 'u'),
('ru', 'ф', 'f'),
('ru', 'х', 'kh'),
('ru', 'ц', 'ts'),
('ru', 'ч', 'ch'),
('ru', 'ш', 'sh'),
('ru', 'щ', 'shch'),
('ru', 'ъ', ''),
('ru', 'ы', 'y'),
('ru', 'ь', ''),
('ru', 'э', 'e'),
('ru', 'ю', 'yu'),
('ru', 'я', 'ya'),
('ru', 'ё', 'yo');

-- +goose Down
UPDATE public.languages
SET initial_flag_svg = ''
WHERE code IN ('en', 'kk', 'pl', 'ru');

DELETE FROM public.initial_slug_rules;
DELETE FROM public.initial_ui_messages;
DELETE FROM public.ui_message_keys;
DELETE FROM public.initial_site_defaults;
