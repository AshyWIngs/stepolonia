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
    ) REFERENCES public.ui_message_keys (message_key),
    CONSTRAINT initial_ui_messages_value_check CHECK (
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

CREATE TABLE public.initial_site_defaults (
    singleton boolean CONSTRAINT initial_site_defaults_pkey PRIMARY KEY,
    accent text NOT NULL,
    CONSTRAINT initial_site_defaults_singleton_check CHECK (singleton),
    CONSTRAINT initial_site_defaults_accent_check CHECK (
        accent ~ '^#[0-9a-fA-F]{6}$'
    )
);

INSERT INTO public.initial_site_defaults (singleton, accent)
VALUES (true, '#2563eb');

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

-- Derive the registered placeholders from the initial catalog.
WITH initial_messages (message_key, en, pl, ru, kk) AS (
    VALUES
    (
        'articles',
        'Articles',
        'Artykuły',
        'Статьи',
        'Мақалалар'
    ),
    (
        'events',
        'Events',
        'Wydarzenia',
        'События',
        'Іс-шаралар'
    ),
    (
        'empty',
        'No results',
        'Brak wyników',
        'Нет результатов',
        'Нәтиже табылмады'
    ),
    (
        'pagination',
        'Pages',
        'Strony',
        'Страницы',
        'Беттер'
    ),
    (
        'previous',
        'Previous',
        'Poprzednia',
        'Предыдущая',
        'Алдыңғы'
    ),
    (
        'next',
        'Next',
        'Następna',
        'Следующая',
        'Келесі'
    ),
    (
        'navigation',
        'Navigation',
        'Nawigacja',
        'Навигация',
        'Навигация'
    ),
    (
        'related_content',
        'Related content',
        'Powiązane materiały',
        'Связанные материалы',
        'Байланысты материалдар'
    ),
    (
        'all',
        'All',
        'Wszystkie',
        'Все',
        'Барлығы'
    ),
    (
        'languages',
        'Languages',
        'Języki',
        'Языки',
        'Тілдер'
    ),
    (
        'errors.400',
        'Invalid request',
        'Nieprawidłowe żądanie',
        'Некорректный запрос',
        'Жарамсыз сұрау'
    ),
    (
        'errors.404',
        'Page not found',
        'Nie znaleziono',
        'Страница не найдена',
        'Бет табылмады'
    ),
    (
        'errors.503',
        'Service unavailable',
        'Usługa niedostępna',
        'Сервис недоступен',
        'Қызмет қолжетімсіз'
    ),
    (
        'errors.other',
        'Something went wrong',
        'Wystąpił błąd',
        'Произошла ошибка',
        'Қате орын алды'
    ),
    (
        'errors.retry',
        'Try again',
        'Spróbuj ponownie',
        'Повторить',
        'Қайталап көру'
    ),
    (
        'errors.home',
        'Home',
        'Strona główna',
        'Главная',
        'Басты бет'
    ),
    (
        'admin.name',
        'Administration',
        'Administracja',
        'Администрирование',
        'Әкімшілендіру'
    ),
    (
        'admin.language',
        'Language',
        'Język',
        'Язык',
        'Тіл'
    ),
    (
        'admin.theme',
        'Theme',
        'Motyw',
        'Тема',
        'Тақырып'
    ),
    (
        'admin.system',
        'System',
        'Systemowy',
        'Системная',
        'Жүйелік'
    ),
    (
        'admin.light',
        'Light',
        'Jasny',
        'Светлая',
        'Ашық'
    ),
    (
        'admin.dark',
        'Dark',
        'Ciemny',
        'Тёмная',
        'Қараңғы'
    ),
    (
        'admin.sign_in',
        'Sign in',
        'Zaloguj się',
        'Вход',
        'Кіру'
    ),
    (
        'admin.login',
        'Login',
        'Login',
        'Имя пользователя',
        'Пайдаланушы аты'
    ),
    (
        'admin.password',
        'Password',
        'Hasło',
        'Пароль',
        'Құпиясөз'
    ),
    (
        'admin.continue',
        'Continue',
        'Dalej',
        'Продолжить',
        'Жалғастыру'
    ),
    (
        'admin.two_step',
        'Two-step verification',
        'Weryfikacja dwuetapowa',
        'Двухэтапная проверка',
        'Екі қадамдық тексеру'
    ),
    (
        'admin.code_help',
        'Enter the six-digit code from your authenticator app.',
        'Wpisz sześciocyfrowy kod z aplikacji uwierzytelniającej.',
        'Введите шестизначный код из приложения для аутентификации.',
        'Аутентификация қолданбасындағы алты таңбалы кодты енгізіңіз.'
    ),
    (
        'admin.code',
        'Verification code',
        'Kod weryfikacyjny',
        'Код подтверждения',
        'Растау коды'
    ),
    (
        'admin.verify',
        'Verify',
        'Zweryfikuj',
        'Подтвердить',
        'Растау'
    ),
    (
        'admin.restart',
        'Start over',
        'Zacznij od nowa',
        'Начать заново',
        'Қайта бастау'
    ),
    (
        'admin.signed_in',
        'Signed in',
        'Zalogowano',
        'Вход выполнен',
        'Жүйеге кірдіңіз'
    ),
    (
        'admin.sign_out',
        'Sign out',
        'Wyloguj się',
        'Выйти',
        'Шығу'
    ),
    (
        'admin.checking',
        'Checking your session…',
        'Sprawdzanie sesji…',
        'Проверяем сессию…',
        'Сессия тексеріліп жатыр…'
    ),
    (
        'admin.working',
        'Working…',
        'Przetwarzanie…',
        'Выполняем запрос…',
        'Сұрау орындалып жатыр…'
    ),
    (
        'admin.retry',
        'Retry',
        'Spróbuj ponownie',
        'Повторить',
        'Қайталап көру'
    ),
    (
        'admin.invalid_credentials',
        'We could not verify your credentials. Check your entries and try '
        'again.',
        'Nie udało się zweryfikować danych logowania. Sprawdź je i spróbuj '
        'ponownie.',
        'Не удалось проверить данные для входа. Проверьте их и повторите '
        'попытку.',
        'Кіру деректері расталмады. Деректерді тексеріп, қайталап көріңіз.'
    ),
    (
        'admin.invalid_code',
        'We could not verify the code. Try a fresh code.',
        'Nie udało się zweryfikować kodu. Spróbuj użyć nowego kodu.',
        'Не удалось проверить код. Попробуйте новый код.',
        'Код расталмады. Жаңа кодты енгізіңіз.'
    ),
    (
        'admin.unavailable',
        'Administration is temporarily unavailable. Try again.',
        'Panel administracyjny jest chwilowo niedostępny. Spróbuj ponownie.',
        'Администрирование временно недоступно. Повторите попытку.',
        'Әкімшілендіру уақытша қолжетімсіз. Қайталап көріңіз.'
    ),
    (
        'admin.limited',
        'Too many attempts. Try again when the button is available.',
        'Zbyt wiele prób. Spróbuj ponownie, gdy przycisk będzie dostępny.',
        'Слишком много попыток. Повторите, когда кнопка станет доступна.',
        'Әрекет тым көп болды. Түйме қайта қолжетімді болғанда қайталап '
        'көріңіз.'
    ),
    (
        'admin.changed',
        'Your session changed. Check your session to continue.',
        'Sesja się zmieniła. Sprawdź sesję, aby kontynuować.',
        'Сессия изменилась. Проверьте её, чтобы продолжить.',
        'Сессия өзгерді. Жалғастыру үшін сессияны тексеріңіз.'
    ),
    (
        'admin.restricted',
        'Account setup or recovery is in progress. You can end that session '
        'and start over.',
        'Trwa konfiguracja lub odzyskiwanie konta. Możesz zakończyć tę sesję '
        'i zacząć od nowa.',
        'Идёт настройка или восстановление аккаунта. Можно завершить эту '
        'сессию и начать заново.',
        'Аккаунт бапталып немесе қалпына келтіріліп жатыр. Сессияны аяқтап, '
        'қайта бастай аласыз.'
    ),
    (
        'admin.signed_out',
        'You’re signed out.',
        'Wylogowano.',
        'Вы вышли из системы.',
        'Жүйеден шықтыңыз.'
    ),
    (
        'admin.settings',
        'Portal settings',
        'Ustawienia portalu',
        'Настройки портала',
        'Портал параметрлері'
    ),
    (
        'admin.portal_name',
        'Portal name',
        'Nazwa portalu',
        'Название портала',
        'Портал атауы'
    ),
    (
        'admin.tagline',
        'Tagline',
        'Hasło portalu',
        'Слоган',
        'Ұран'
    ),
    (
        'admin.description',
        'Description',
        'Opis',
        'Описание',
        'Сипаттама'
    ),
    (
        'admin.notice',
        'Notice',
        'Komunikat',
        'Объявление',
        'Хабарландыру'
    ),
    (
        'admin.accent',
        'Accent color',
        'Kolor akcentu',
        'Цвет акцента',
        'Екпін түсі'
    ),
    (
        'admin.default_language',
        'Default content language',
        'Domyślny język treści',
        'Язык контента по умолчанию',
        'Мазмұнның әдепкі тілі'
    ),
    (
        'admin.fallback_language',
        'Fallback interface language',
        'Zapasowy język interfejsu',
        'Резервный язык интерфейса',
        'Интерфейстің резервтік тілі'
    ),
    (
        'admin.interface_texts',
        'Interface texts',
        'Teksty interfejsu',
        'Тексты интерфейса',
        'Интерфейс мәтіндері'
    ),
    (
        'admin.save_draft',
        'Save draft',
        'Zapisz szkic',
        'Сохранить черновик',
        'Нобайды сақтау'
    ),
    (
        'admin.publish_draft',
        'Publish draft',
        'Opublikuj szkic',
        'Опубликовать черновик',
        'Нобайды жариялау'
    ),
    (
        'admin.confirm_publish',
        'Publish the saved settings and interface texts?',
        'Opublikować zapisane ustawienia i teksty interfejsu?',
        'Опубликовать сохранённые настройки и тексты интерфейса?',
        'Сақталған параметрлер мен интерфейс мәтіндерін жариялау керек пе?'
    ),
    (
        'admin.draft_saved',
        'Draft saved.',
        'Zapisano szkic.',
        'Черновик сохранён.',
        'Нобай сақталды.'
    ),
    (
        'admin.published',
        'Settings published.',
        'Ustawienia opublikowane.',
        'Настройки опубликованы.',
        'Параметрлер жарияланды.'
    ),
    (
        'admin.access_denied',
        'You do not have permission for this action.',
        'Nie masz uprawnień do tego działania.',
        'У вас нет разрешения на это действие.',
        'Бұл әрекетті орындауға рұқсатыңыз жоқ.'
    ),
    (
        'admin.settings_conflict',
        'Settings changed in another session. Reload before saving again.',
        'Ustawienia zmieniono w innej sesji. Wczytaj je ponownie przed '
        'zapisem.',
        'Настройки изменились в другой сессии. Загрузите их заново перед '
        'сохранением.',
        'Параметрлер басқа сессияда өзгерді. Сақтау алдында қайта жүктеңіз.'
    ),
    (
        'admin.settings_invalid',
        'Check the portal fields and complete each selected translation.',
        'Sprawdź pola portalu i uzupełnij wszystkie wybrane tłumaczenia.',
        'Проверьте поля портала и заполните все выбранные переводы.',
        'Портал өрістерін тексеріп, таңдалған аудармаларды толтырыңыз.'
    ),
    (
        'admin.reload_settings',
        'Discard local edits and reload',
        'Odrzuć lokalne zmiany i wczytaj ponownie',
        'Отбросить локальные правки и загрузить заново',
        'Жергілікті өзгерістерді алып тастап, қайта жүктеу'
    ),
    (
        'admin.cancel',
        'Cancel',
        'Anuluj',
        'Отмена',
        'Бас тарту'
    ),
    (
        'admin.close',
        'Close',
        'Zamknij',
        'Закрыть',
        'Жабу'
    ),
    (
        'admin.fresh_auth',
        'Confirm your identity',
        'Potwierdź tożsamość',
        'Подтвердите личность',
        'Жеке басыңызды растаңыз'
    ),
    (
        'admin.unlock',
        'Confirm identity',
        'Potwierdź tożsamość',
        'Подтвердить личность',
        'Жеке басты растау'
    ),
    (
        'admin.add_catalog',
        'Add interface language',
        'Dodaj język interfejsu',
        'Добавить язык интерфейса',
        'Интерфейс тілін қосу'
    ),
    (
        'admin.step_up_required',
        'Confirm your identity to continue.',
        'Potwierdź tożsamość, aby wykonać tę czynność.',
        'Подтвердите личность, чтобы продолжить.',
        'Жалғастыру үшін жеке басыңызды растаңыз.'
    ),
    (
        'admin.remove_catalog',
        'Remove catalog from draft',
        'Usuń katalog ze szkicu',
        'Убрать каталог из черновика',
        'Каталогты нобайдан алып тастау'
    ),
    (
        'admin.content',
        'Content',
        'Materiały',
        'Материалы',
        'Материалдар'
    ),
    (
        'admin.content_collections',
        'Collections',
        'Kolekcje',
        'Коллекции',
        'Жинақтар'
    ),
    (
        'admin.content_collection',
        'Collection',
        'Kolekcja',
        'Коллекция',
        'Жинақ'
    ),
    (
        'admin.content_no_collections',
        'No collections are available.',
        'Brak dostępnych kolekcji.',
        'Нет доступных коллекций.',
        'Қолжетімді жинақтар жоқ.'
    ),
    (
        'admin.content_more_collections',
        'Load more collections',
        'Wczytaj więcej kolekcji',
        'Загрузить ещё коллекции',
        'Қосымша жинақтарды жүктеу'
    ),
    (
        'admin.content_create',
        'Create draft',
        'Utwórz szkic',
        'Создать черновик',
        'Нобай жасау'
    ),
    (
        'admin.content_no_drafts',
        'No drafts are available.',
        'Brak dostępnych szkiców.',
        'Нет доступных черновиков.',
        'Қолжетімді нобайлар жоқ.'
    ),
    (
        'admin.content_more_drafts',
        'Load more drafts',
        'Wczytaj więcej szkiców',
        'Загрузить ещё черновики',
        'Қосымша нобайларды жүктеу'
    ),
    (
        'admin.content_editor',
        'Draft editor',
        'Edytor szkicu',
        'Редактор черновика',
        'Нобай редакторы'
    ),
    (
        'admin.content_back',
        'Back to drafts',
        'Wróć do szkiców',
        'К списку черновиков',
        'Нобайлар тізіміне қайту'
    ),
    (
        'admin.content_shared',
        'Shared fields',
        'Pola wspólne',
        'Общие поля',
        'Ортақ өрістер'
    ),
    (
        'admin.content_translation',
        'Translation',
        'Tłumaczenie',
        'Перевод',
        'Аударма'
    ),
    (
        'admin.content_required',
        'required for readiness',
        'wymagane do zatwierdzenia',
        'нужно для готовности',
        'дайындық үшін міндетті'
    ),
    (
        'admin.content_empty_value',
        'No value',
        'Brak wartości',
        'Нет значения',
        'Мән жоқ'
    ),
    (
        'admin.content_yes',
        'Yes',
        'Tak',
        'Да',
        'Иә'
    ),
    (
        'admin.content_no',
        'No',
        'Nie',
        'Нет',
        'Жоқ'
    ),
    (
        'admin.content_relation_help',
        'Enter the ID of an accessible target draft. Clear the value to '
        'remove the link.',
        'Wpisz identyfikator dostępnego szkicu. Wyczyść pole, aby usunąć '
        'powiązanie.',
        'Введите идентификатор доступного черновика. Очистите поле, чтобы '
        'убрать связь.',
        'Қолжетімді нобайдың идентификаторын енгізіңіз. Байланысты жою үшін '
        'өрісті тазалаңыз.'
    ),
    (
        'admin.content_conflict',
        'The draft has changed. Reload the current version to continue.',
        'Szkic został zmieniony. Wczytaj aktualną wersję.',
        'Черновик изменился. Загрузите актуальную версию.',
        'Нобай өзгерді. Ағымдағы нұсқаны қайта жүктеңіз.'
    ),
    (
        'admin.content_invalid',
        'Check the draft values and try again.',
        'Sprawdź wartości w szkicu i spróbuj ponownie.',
        'Проверьте значения черновика и повторите попытку.',
        'Нобайдағы мәндерді тексеріп, қайта көріңіз.'
    ),
    (
        'admin.content_transition_failed',
        'The action could not be completed. Check readiness, languages and '
        'relations.',
        'Nie udało się wykonać operacji. Sprawdź gotowość, języki i '
        'powiązania.',
        'Действие не выполнено. Проверьте готовность, языки и связи.',
        'Әрекет орындалмады. Дайындықты, тілдер мен байланыстарды тексеріңіз.'
    ),
    (
        'admin.content_required_missing',
        'Complete the highlighted fields before marking the draft ready.',
        'Uzupełnij oznaczone pola przed zatwierdzeniem szkicu.',
        'Заполните отмеченные поля, чтобы подтвердить готовность.',
        'Дайын деп белгілеу үшін көрсетілген өрістерді толтырыңыз.'
    ),
    (
        'admin.content_ready_saved',
        'Readiness saved.',
        'Zapisano gotowość.',
        'Готовность сохранена.',
        'Дайындық сақталды.'
    ),
    (
        'admin.content_shared_ready',
        'Shared fields are ready.',
        'Pola wspólne są gotowe.',
        'Общие поля готовы.',
        'Ортақ өрістер дайын.'
    ),
    (
        'admin.content_locale_ready',
        'This translation is ready.',
        'To tłumaczenie jest gotowe.',
        'Перевод готов.',
        'Бұл аударма дайын.'
    ),
    (
        'admin.content_mark_shared',
        'Mark shared fields ready',
        'Zatwierdź pola wspólne',
        'Подтвердить готовность общих полей',
        'Ортақ өрістерді дайын деп белгілеу'
    ),
    (
        'admin.content_mark_locale',
        'Mark translation ready',
        'Zatwierdź tłumaczenie',
        'Подтвердить готовность перевода',
        'Аударманы дайын деп белгілеу'
    ),
    (
        'admin.content_language_disabled',
        'This language is disabled and its translation cannot be published.',
        'Ten język jest wyłączony. Publikacja tłumaczenia jest niedostępna.',
        'Язык отключён. Опубликовать этот перевод нельзя.',
        'Тіл өшірілген. Бұл аударманы жариялау мүмкін емес.'
    ),
    (
        'admin.content_public_links',
        'Published translations',
        'Opublikowane tłumaczenia',
        'Опубликованные переводы',
        'Жарияланған аудармалар'
    ),
    (
        'admin.content_not_published',
        'No translations are published.',
        'Brak opublikowanych tłumaczeń.',
        'Нет опубликованных переводов.',
        'Жарияланған аударма жоқ.'
    ),
    (
        'admin.content_publish',
        'Publish translations',
        'Opublikuj tłumaczenia',
        'Опубликовать переводы',
        'Аудармаларды жариялау'
    ),
    (
        'admin.content_unpublish',
        'Unpublish',
        'Wycofaj publikację',
        'Снять с публикации',
        'Жарияланымды алып тастау'
    ),
    (
        'admin.content_published',
        'Translations published.',
        'Opublikowano tłumaczenia.',
        'Переводы опубликованы.',
        'Аудармалар жарияланды.'
    ),
    (
        'admin.content_unpublished',
        'Publication removed.',
        'Wycofano publikację.',
        'Материал снят с публикации.',
        'Жарияланым алып тасталды.'
    ),
    (
        'admin.content_adopt_schema',
        'Adopt current schema',
        'Zastosuj aktualny schemat',
        'Принять актуальную схему',
        'Ағымдағы схеманы қабылдау'
    ),
    (
        'admin.content_schema_adopted',
        'Current schema adopted.',
        'Zastosowano aktualny schemat.',
        'Актуальная схема принята.',
        'Ағымдағы схема қабылданды.'
    ),
    (
        'admin.content_schema_outdated',
        'A newer schema is available. Adopting it resets readiness.',
        'Dostępny jest nowszy schemat. Jego zastosowanie zeruje gotowość.',
        'Доступна новая схема. Её принятие сбросит готовность.',
        'Жаңа схема қолжетімді. Оны қабылдау дайындық күйін тазартады.'
    ),
    (
        'admin.content_reload',
        'Reload draft',
        'Wczytaj szkic ponownie',
        'Перезагрузить черновик',
        'Нобайды қайта жүктеу'
    ),
    (
        'admin.content_confirm',
        'Confirm',
        'Potwierdź',
        'Подтвердить',
        'Растау'
    ),
    (
        'admin.content_confirm_publish',
        'Publish the selected translations?',
        'Opublikować wybrane tłumaczenia?',
        'Опубликовать выбранные переводы?',
        'Таңдалған аудармалар жариялансын ба?'
    ),
    (
        'admin.content_confirm_unpublish',
        'Remove this publication and clear readiness?',
        'Wycofać publikację i wyzerować gotowość?',
        'Снять материал с публикации и сбросить готовность?',
        'Жарияланым алынып, дайындық күйі тазартылсын ба?'
    ),
    (
        'admin.content_confirm_reload',
        'Discard unsaved changes and reload?',
        'Odrzucić niezapisane zmiany i wczytać ponownie?',
        'Отбросить несохранённые изменения и перечитать черновик?',
        'Сақталмаған өзгерістер тасталып, нобай қайта жүктелсін бе?'
    ),
    (
        'admin.content_confirm_close',
        'Close the draft and discard unsaved changes?',
        'Zamknąć szkic i odrzucić niezapisane zmiany?',
        'Закрыть черновик и отбросить несохранённые изменения?',
        'Нобай жабылып, сақталмаған өзгерістер тасталсын ба?'
    ),
    (
        'admin.content_publish_languages',
        'Languages to publish',
        'Języki do publikacji',
        'Языки публикации',
        'Жариялау тілдері'
    ),
    (
        'admin.content_clear_relation',
        'Clear relation',
        'Usuń powiązanie',
        'Удалить связь',
        'Байланысты жою'
    ),
    (
        'admin.content_links_new_tab',
        'Published links open in a new tab.',
        'Linki do publikacji otwierają się w nowej karcie.',
        'Ссылки на публикации открываются в новой вкладке.',
        'Жарияланым сілтемелері жаңа қойындыда ашылады.'
    ),
    (
        'admin.registry',
        'Type Builder',
        'Kreator typów',
        'Конструктор типов',
        'Түрлер құрастырғышы'
    ),
    (
        'admin.registry_choose',
        'Renderer and collection',
        'Moduł prezentacji i kolekcja',
        'Компонент отображения и коллекция',
        'Көрсету модулі мен жинақ'
    ),
    (
        'admin.registry_schema',
        'Current schema',
        'Aktualny schemat',
        'Текущая схема',
        'Ағымдағы схема'
    ),
    (
        'admin.registry_field_key',
        'Field key',
        'Klucz pola',
        'Ключ поля',
        'Өріс кілті'
    ),
    (
        'admin.registry_kind',
        'Field type',
        'Typ pola',
        'Тип поля',
        'Өріс түрі'
    ),
    (
        'admin.registry_scope',
        'Field scope',
        'Zakres pola',
        'Область поля',
        'Өріс ауқымы'
    ),
    (
        'admin.registry_visibility',
        'Visibility',
        'Widoczność',
        'Видимость',
        'Көрінуі'
    ),
    (
        'admin.registry_private',
        'Private',
        'Prywatne',
        'Закрытое',
        'Жеке'
    ),
    (
        'admin.registry_public',
        'Public',
        'Publiczne',
        'Публичное',
        'Жария'
    ),
    (
        'admin.registry_codes',
        'Allowed values, separated by commas',
        'Dozwolone wartości oddzielone przecinkami',
        'Разрешённые значения через запятую',
        'Үтірмен бөлінген рұқсатты мәндер'
    ),
    (
        'admin.registry_target',
        'Target collection',
        'Kolekcja docelowa',
        'Целевая коллекция',
        'Мақсатты жинақ'
    ),
    (
        'admin.registry_new_field',
        'New optional field',
        'Nowe pole opcjonalne',
        'Новое необязательное поле',
        'Жаңа міндетті емес өріс'
    ),
    (
        'admin.registry_remove_field',
        'Remove new field',
        'Usuń nowe pole',
        'Удалить новое поле',
        'Жаңа өрісті жою'
    ),
    (
        'admin.registry_label',
        'Field label',
        'Etykieta pola',
        'Подпись поля',
        'Өріс атауы'
    ),
    (
        'admin.registry_help',
        'Field help text',
        'Podpowiedź dla pola',
        'Подсказка к полю',
        'Өріске арналған кеңес'
    ),
    (
        'admin.registry_add_field',
        'Add optional field',
        'Dodaj pole opcjonalne',
        'Добавить необязательное поле',
        'Міндетті емес өрісті қосу'
    ),
    (
        'admin.registry_initial_access',
        'Grant me access to this new collection',
        'Przyznaj mi dostęp do nowej kolekcji',
        'Выдать мне доступ к новой коллекции',
        'Маған жаңа жинаққа қолжетімділік беру'
    ),
    (
        'admin.registry_new_field_access',
        'Extend my existing access to the new fields',
        'Rozszerz mój dostęp na nowe pola',
        'Распространить мои текущие права на новые поля',
        'Қолданыстағы қолжетімділігімді жаңа өрістерге кеңейту'
    ),
    (
        'admin.registry_save',
        'Save schema changes',
        'Zapisz zmiany schematu',
        'Сохранить изменения схемы',
        'Схема өзгерістерін сақтау'
    ),
    (
        'admin.registry_create',
        'Create collection',
        'Utwórz kolekcję',
        'Создать коллекцию',
        'Жинақ жасау'
    ),
    (
        'admin.registry_created',
        'Collection created.',
        'Utworzono kolekcję.',
        'Коллекция создана.',
        'Жинақ жасалды.'
    ),
    (
        'admin.registry_revised',
        'Collection changes saved.',
        'Zapisano zmiany kolekcji.',
        'Изменения коллекции сохранены.',
        'Жинақ өзгерістері сақталды.'
    ),
    (
        'admin.registry_access_applied',
        'Granted permissions',
        'Przyznane uprawnienia',
        'Выданные разрешения',
        'Берілген рұқсаттар'
    ),
    (
        'admin.registry_reload',
        'Reload collection',
        'Wczytaj kolekcję ponownie',
        'Перечитать коллекцию',
        'Жинақты қайта жүктеу'
    ),
    (
        'admin.registry_conflict',
        'The collection has changed. Reload it before saving.',
        'Kolekcja została zmieniona. Wczytaj ją ponownie przed zapisem.',
        'Коллекция изменилась. Перечитайте её перед сохранением.',
        'Жинақ өзгерді. Сақтау алдында оны қайта жүктеңіз.'
    ),
    (
        'admin.registry_invalid',
        'Check the field definitions and try again.',
        'Sprawdź definicje pól i spróbuj ponownie.',
        'Проверьте определения полей и повторите попытку.',
        'Өріс анықтамаларын тексеріп, қайталап көріңіз.'
    ),
    (
        'admin.account',
        'My account',
        'Moje konto',
        'Моя учётная запись',
        'Менің тіркелгім'
    ),
    (
        'admin.account_codes',
        'Recovery codes',
        'Kody odzyskiwania',
        'Коды восстановления',
        'Қалпына келтіру кодтары'
    ),
    (
        'admin.account_codes_help',
        'Save these codes somewhere secure. Sign in again after closing this '
        'screen.',
        'Zapisz kody w bezpiecznym miejscu. Po zamknięciu zaloguj się '
        'ponownie.',
        'Сохраните коды в безопасном месте. После закрытия войдите снова.',
        'Кодтарды қауіпсіз жерде сақтаңыз. Осы бетті жапқан соң қайта кіріңіз.'
    ),
    (
        'admin.account_done',
        'Done',
        'Gotowe',
        'Готово',
        'Дайын'
    ),
    (
        'admin.account_codes_remaining',
        'Unused recovery codes:',
        'Niewykorzystane kody:',
        'Неиспользованные коды:',
        'Пайдаланылмаған кодтар:'
    ),
    (
        'admin.account_change_password',
        'Change password',
        'Zmień hasło',
        'Сменить пароль',
        'Құпиясөзді өзгерту'
    ),
    (
        'admin.account_replace_factor',
        'Replace authenticator',
        'Zmień aplikację uwierzytelniającą',
        'Заменить приложение-аутентификатор',
        'Аутентификаторды ауыстыру'
    ),
    (
        'admin.account_regenerate_codes',
        'Generate new recovery codes',
        'Wygeneruj nowe kody odzyskiwania',
        'Выпустить новые коды восстановления',
        'Жаңа қалпына келтіру кодтарын жасау'
    ),
    (
        'admin.account_replace_codes_warning',
        'Old recovery codes will stop working. All sessions will end.',
        'Stare kody przestaną działać. Wszystkie sesje zostaną zakończone.',
        'Прежние коды перестанут действовать. Все сессии завершатся.',
        'Ескі кодтар жарамсыз болады. Барлық сеанстар аяқталады.'
    ),
    (
        'admin.account_logout_warning',
        'Confirming will end all your sessions.',
        'Po zatwierdzeniu wszystkie sesje zostaną zakończone.',
        'После подтверждения все ваши сессии завершатся.',
        'Растағаннан кейін барлық сеанстарыңыз аяқталады.'
    ),
    (
        'admin.account_new_password',
        'New password',
        'Nowe hasło',
        'Новый пароль',
        'Жаңа құпиясөз'
    ),
    (
        'admin.account_factor_help',
        'Add this key to your replacement authenticator and enter its code.',
        'Dodaj ten klucz do nowego uwierzytelniacza i wpisz wygenerowany kod.',
        'Добавьте ключ в новый аутентификатор и введите полученный код.',
        'Бұл кілтті жаңа аутентификаторға қосып, көрсетілген кодты енгізіңіз.'
    ),
    (
        'admin.account_revoke_confirmation',
        'End the session started at',
        'Zakończyć sesję rozpoczętą:',
        'Завершить сессию, начатую',
        'Басталған сеансты аяқтау:'
    ),
    (
        'admin.account_sessions',
        'Active sessions',
        'Aktywne sesje',
        'Активные сессии',
        'Белсенді сеанстар'
    ),
    (
        'admin.account_current_session',
        'Current session',
        'Bieżąca sesja',
        'Текущая сессия',
        'Ағымдағы сеанс'
    ),
    (
        'admin.account_started',
        'Started',
        'Rozpoczęcie',
        'Начало',
        'Басталуы'
    ),
    (
        'admin.account_last_seen',
        'Last active',
        'Ostatnia aktywność',
        'Последняя активность',
        'Соңғы белсенділік'
    ),
    (
        'admin.account_expires',
        'Expires',
        'Wygaśnięcie',
        'Истекает',
        'Аяқталуы'
    ),
    (
        'admin.account_revoke_session',
        'Revoke session',
        'Zakończ sesję',
        'Завершить сессию',
        'Сеансты аяқтау'
    ),
    (
        'admin.account_revoke_all',
        'Revoke all sessions',
        'Zakończ wszystkie sesje',
        'Завершить все сессии',
        'Барлық сеансты аяқтау'
    ),
    (
        'admin.account_validation',
        'Check your input and try again. Passwords must have 15 to 128 '
        'characters.',
        'Sprawdź dane i spróbuj ponownie. Hasło musi mieć od 15 do 128 znaków.',
        'Проверьте данные и повторите попытку. Длина пароля — от 15 до 128 '
        'символов.',
        'Деректерді тексеріп, қайталап көріңіз. Құпиясөз 15–128 таңбадан '
        'тұруы керек.'
    ),
    (
        'admin.audit',
        'Audit log',
        'Dziennik audytu',
        'Журнал аудита',
        'Аудит журналы'
    ),
    (
        'admin.audit_empty',
        'No events on this page.',
        'Brak zdarzeń na tej stronie.',
        'На этой странице нет событий.',
        'Бұл бетте оқиғалар жоқ.'
    ),
    (
        'admin.audit_success',
        'Succeeded',
        'Powodzenie',
        'Выполнено',
        'Орындалды'
    ),
    (
        'admin.audit_denied',
        'Denied',
        'Odmowa',
        'Отклонено',
        'Қабылданбады'
    ),
    (
        'admin.audit_failure',
        'Failed',
        'Błąd',
        'Ошибка',
        'Қате'
    ),
    (
        'admin.audit_actor',
        'Actor ID',
        'Identyfikator wykonawcy',
        'Идентификатор инициатора',
        'Орындаушы идентификаторы'
    ),
    (
        'admin.audit_target',
        'Target ID',
        'Identyfikator celu',
        'Идентификатор цели',
        'Нысан идентификаторы'
    ),
    (
        'admin.audit_request',
        'Request ID',
        'Identyfikator żądania',
        'Идентификатор запроса',
        'Сұрау идентификаторы'
    ),
    (
        'admin.audit_reason',
        'Reason',
        'Powód',
        'Причина',
        'Себебі'
    ),
    (
        'admin.entry_enrollment',
        'I have an invitation',
        'Mam zaproszenie',
        'У меня есть приглашение',
        'Менде шақыру бар'
    ),
    (
        'admin.entry_recovery',
        'Recover access',
        'Odzyskaj dostęp',
        'Восстановить доступ',
        'Қолжетімділікті қалпына келтіру'
    ),
    (
        'admin.entry_invitation',
        'Invitation token',
        'Token zaproszenia',
        'Токен приглашения',
        'Шақыру токені'
    ),
    (
        'admin.entry_recovery_code',
        'Recovery code',
        'Kod odzyskiwania',
        'Код восстановления',
        'Қалпына келтіру коды'
    ),
    (
        'admin.entry_invalid',
        'These credentials could not be verified. Check them and try again.',
        'Nie można potwierdzić tych danych. Sprawdź je i spróbuj ponownie.',
        'Не удалось проверить данные. Проверьте их и повторите попытку.',
        'Деректер расталмады. Тексеріп, қайталап көріңіз.'
    ),
    (
        'admin.entry_interrupted',
        'Setup was interrupted. Request a new token if you were using an '
        'invitation.',
        'Konfiguracja została przerwana. Aby użyć zaproszenia, poproś o nowy '
        'token.',
        'Настройка прервана. Если вы использовали приглашение, запросите '
        'новый токен.',
        'Баптау үзілді. Шақыру пайдаланылса, жаңа токен сұраңыз.'
    ),
    (
        'admin.entry_activation',
        'Save the codes somewhere secure. An administrator still needs to '
        'activate your account.',
        'Zapisz kody w bezpiecznym miejscu. Administrator musi jeszcze '
        'aktywować konto.',
        'Сохраните коды в безопасном месте. Администратор должен активировать'
        ' вашу учётную запись.',
        'Кодтарды қауіпсіз жерде сақтаңыз. Әкімші тіркелгіңізді әлі '
        'белсендіруі керек.'
    ),
    (
        'admin.access',
        'Access',
        'Dostęp',
        'Доступ',
        'Қолжетімділік'
    ),
    (
        'admin.access_saved',
        'Changes saved.',
        'Zapisano zmiany.',
        'Изменения сохранены.',
        'Өзгерістер сақталды.'
    ),
    (
        'admin.access_reload',
        'Reload',
        'Wczytaj ponownie',
        'Перезагрузить',
        'Қайта жүктеу'
    ),
    (
        'admin.access_version_conflict',
        'This record changed. Reload it and review your changes before '
        'retrying.',
        'Dane zostały zmienione. Wczytaj je ponownie i sprawdź swój wybór.',
        'Запись изменилась. Перезагрузите её и проверьте изменения перед '
        'повтором.',
        'Жазба өзгерді. Қайта жүктеп, өзгерістерді тексеріңіз.'
    ),
    (
        'admin.access_last_admin',
        'At least one administrator must keep access and be able to recover '
        'their account.',
        'Co najmniej jeden administrator musi zachować dostęp i możliwość '
        'odzyskania konta.',
        'Хотя бы один администратор должен сохранить доступ и возможность '
        'восстановления записи.',
        'Кемінде бір әкімшінің қолжетімділігі мен тіркелгіні қалпына келтіру '
        'мүмкіндігі сақталуы керек.'
    ),
    (
        'admin.access_transition',
        'This action is unavailable in the current state.',
        'Zmiana jest niedostępna w bieżącym stanie.',
        'Действие недоступно при текущем состоянии.',
        'Бұл әрекет ағымдағы күйде қолжетімсіз.'
    ),
    (
        'admin.access_role_in_use',
        'Remove the role’s dependencies before deleting it.',
        'Usuń powiązania roli przed jej usunięciem.',
        'Удалите зависимости роли перед её удалением.',
        'Рөлді жоймас бұрын тәуелділіктерін алып тастаңыз.'
    ),
    (
        'admin.access_unavailable_target',
        'The selected record is unavailable.',
        'Wybrany rekord jest niedostępny.',
        'Выбранная запись недоступна.',
        'Таңдалған жазба қолжетімсіз.'
    ),
    (
        'admin.access_input',
        'Check the fields and try again.',
        'Sprawdź pola i spróbuj ponownie.',
        'Проверьте поля и повторите попытку.',
        'Өрістерді тексеріп, қайталап көріңіз.'
    ),
    (
        'admin.access_state_pending',
        'Pending',
        'Oczekujące',
        'Ожидает настройки',
        'Баптауды күтуде'
    ),
    (
        'admin.access_state_disabled',
        'Disabled',
        'Wyłączone',
        'Отключено',
        'Өшірілген'
    ),
    (
        'admin.access_state_active',
        'Active',
        'Aktywne',
        'Активно',
        'Белсенді'
    ),
    (
        'admin.access_state_archived',
        'Archived',
        'Zarchiwizowane',
        'В архиве',
        'Мұрағатталған'
    ),
    (
        'admin.access_enable',
        'Enable',
        'Włącz',
        'Включить',
        'Қосу'
    ),
    (
        'admin.access_disable',
        'Disable',
        'Wyłącz',
        'Отключить',
        'Өшіру'
    ),
    (
        'admin.access_archive',
        'Archive',
        'Archiwizuj',
        'Архивировать',
        'Мұрағаттау'
    ),
    (
        'admin.access_restore',
        'Restore',
        'Przywróć',
        'Восстановить',
        'Қалпына келтіру'
    ),
    (
        'admin.access_delete',
        'Delete',
        'Usuń',
        'Удалить',
        'Жою'
    ),
    (
        'admin.users',
        'Accounts',
        'Konta',
        'Учётные записи',
        'Тіркелгілер'
    ),
    (
        'admin.users_create',
        'Create account',
        'Utwórz konto',
        'Создать учётную запись',
        'Тіркелгі жасау'
    ),
    (
        'admin.users_created',
        'Pending account created.',
        'Utworzono oczekujące konto.',
        'Создана ожидающая учётная запись.',
        'Баптауды күтетін тіркелгі жасалды.'
    ),
    (
        'admin.users_issue_invite',
        'Issue invitation',
        'Wydaj zaproszenie',
        'Выдать приглашение',
        'Шақыру беру'
    ),
    (
        'admin.users_manage_memberships',
        'Manage role memberships',
        'Zarządzaj członkostwem w rolach',
        'Управлять членством в ролях',
        'Рөл мүшелігін басқару'
    ),
    (
        'admin.users_activate',
        'Activate account',
        'Aktywuj konto',
        'Активировать учётную запись',
        'Тіркелгіні белсендіру'
    ),
    (
        'admin.users_memberships',
        'Account roles',
        'Role konta',
        'Роли учётной записи',
        'Тіркелгі рөлдері'
    ),
    (
        'admin.users_save_memberships',
        'Review role changes',
        'Sprawdź zmiany ról',
        'Проверить изменения ролей',
        'Рөл өзгерістерін тексеру'
    ),
    (
        'admin.users_confirm_invite',
        'A new token replaces the previous invitation and interrupts any '
        'enrollment already in progress.',
        'Nowy token unieważni poprzednie zaproszenie i przerwie rozpoczętą '
        'konfigurację.',
        'Новый токен заменит прежнее приглашение и прервёт начатую настройку.',
        'Жаңа токен бұрынғы шақыруды ауыстырып, басталған баптауды тоқтатады.'
    ),
    (
        'admin.users_confirm_memberships',
        'Replace the complete membership set with this selection?',
        'Zastąpić cały zestaw ról zgodnie z wyborem?',
        'Заменить весь список ролей выбранным набором?',
        'Рөлдердің толық тізімін осы таңдаумен ауыстыру керек пе?'
    ),
    (
        'admin.users_confirm_activate',
        'Activate this account and enable its active role grants?',
        'Aktywować konto i przywrócić uprawnienia jego aktywnych ról?',
        'Активировать запись и применить разрешения её активных ролей?',
        'Тіркелгіні және оның белсенді рөлдерінің рұқсаттарын қосу керек пе?'
    ),
    (
        'admin.users_confirm_disable',
        'Disable this account and revoke its sessions?',
        'Wyłączyć konto i zakończyć jego sesje?',
        'Отключить запись и завершить её сессии?',
        'Тіркелгіні өшіріп, оның сеанстарын аяқтау керек пе?'
    ),
    (
        'admin.users_invite_delivery',
        'Send this one-time token through a verified channel.',
        'Przekaż ten jednorazowy token zweryfikowanym kanałem.',
        'Передайте одноразовый токен по проверенному каналу.',
        'Бір реттік токенді тексерілген арнамен жіберіңіз.'
    ),
    (
        'admin.roles',
        'Roles',
        'Role',
        'Роли',
        'Рөлдер'
    ),
    (
        'admin.roles_create',
        'Create role',
        'Utwórz rolę',
        'Создать роль',
        'Рөл жасау'
    ),
    (
        'admin.roles_edit',
        'Edit role policy',
        'Edytuj politykę roli',
        'Изменить политику роли',
        'Рөл саясатын өңдеу'
    ),
    (
        'admin.roles_copy',
        'Copy role',
        'Skopiuj rolę',
        'Скопировать роль',
        'Рөлді көшіру'
    ),
    (
        'admin.roles_name',
        'Role name',
        'Nazwa roli',
        'Название роли',
        'Рөл атауы'
    ),
    (
        'admin.roles_grants',
        'Role grants',
        'Uprawnienia roli',
        'Разрешения роли',
        'Рөл рұқсаттары'
    ),
    (
        'admin.roles_remove_grant',
        'Remove grant',
        'Usuń uprawnienie',
        'Удалить разрешение',
        'Рұқсатты алып тастау'
    ),
    (
        'admin.roles_operation',
        'Operation and scope',
        'Operacja i zakres',
        'Операция и область',
        'Операция және ауқым'
    ),
    (
        'admin.roles_choose_operation',
        'Choose a grant',
        'Wybierz uprawnienie',
        'Выберите разрешение',
        'Рұқсатты таңдаңыз'
    ),
    (
        'admin.roles_fields',
        'Fields',
        'Pola',
        'Поля',
        'Өрістер'
    ),
    (
        'admin.roles_delegated_operations',
        'Delegated operations',
        'Operacje do delegowania',
        'Делегируемые операции',
        'Тапсырылатын операциялар'
    ),
    (
        'admin.roles_allow_delegation',
        'Allow this grant to be delegated',
        'Zezwól na przekazywanie tego uprawnienia',
        'Разрешить делегирование этого разрешения',
        'Осы рұқсатты тапсыруға рұқсат беру'
    ),
    (
        'admin.roles_add_grant',
        'Add grant',
        'Dodaj uprawnienie',
        'Добавить разрешение',
        'Рұқсат қосу'
    ),
    (
        'admin.roles_save',
        'Save role',
        'Zapisz rolę',
        'Сохранить роль',
        'Рөлді сақтау'
    ),
    (
        'admin.roles_duplicate',
        'A grant for this operation and scope already exists.',
        'Istnieje już uprawnienie dla tej operacji i zakresu.',
        'Разрешение для этой операции и области уже существует.',
        'Осы операция мен ауқымға арналған рұқсат бар.'
    ),
    (
        'admin.roles_confirm_enable',
        'Enable this role’s grants for its members?',
        'Włączyć uprawnienia tej roli dla jej członków?',
        'Включить разрешения этой роли для её участников?',
        'Рөл мүшелері үшін рұқсаттарды қосу керек пе?'
    ),
    (
        'admin.roles_confirm_disable',
        'Disable this role’s grants for its members?',
        'Wyłączyć uprawnienia tej roli dla jej członków?',
        'Отключить разрешения этой роли для её участников?',
        'Рөл мүшелері үшін рұқсаттарды өшіру керек пе?'
    ),
    (
        'admin.roles_confirm_archive',
        'Archive this disabled role?',
        'Przenieść tę wyłączoną rolę do archiwum?',
        'Архивировать отключённую роль?',
        'Өшірілген рөлді мұрағаттау керек пе?'
    ),
    (
        'admin.roles_confirm_restore',
        'Restore this role as disabled?',
        'Przywrócić rolę jako wyłączoną?',
        'Восстановить роль в отключённом состоянии?',
        'Рөлді өшірілген күйде қалпына келтіру керек пе?'
    ),
    (
        'admin.roles_confirm_delete',
        'Permanently delete this role?',
        'Trwale usunąć tę rolę?',
        'Удалить эту роль безвозвратно?',
        'Бұл рөлді біржола жою керек пе?'
    ),
    (
        'admin.assignees',
        'Assignees',
        'Przypisane osoby',
        'Назначенные пользователи',
        'Тағайындалған пайдаланушылар'
    ),
    (
        'admin.assignees_choose',
        'Accounts assigned to this item',
        'Konta przypisane do tego materiału',
        'Учётные записи, назначенные на этот материал',
        'Осы материалға тағайындалған тіркелгілер'
    ),
    (
        'admin.assignees_confirm',
        'Save the complete assignment set?',
        'Zapisać cały zestaw przypisań?',
        'Сохранить полный список назначений?',
        'Тағайындаулардың толық тізімін сақтау керек пе?'
    ),
    (
        'admin.assignees_save',
        'Review assignments',
        'Sprawdź przypisania',
        'Проверить назначения',
        'Тағайындауларды тексеру'
    ),
    (
        'admin.account_no_sessions',
        'No active sessions.',
        'Brak aktywnych sesji.',
        'Активных сессий нет.',
        'Белсенді сеанстар жоқ.'
    ),
    (
        'admin.roles_allow_delegable_content',
        'Allow issuing delegable content grants',
        'Zezwól na nadawanie uprawnień do treści z prawem dalszego delegowania',
        'Разрешить выдачу делегируемых разрешений на контент',
        'Тапсыруға болатын контент рұқсаттарын беруге рұқсат беру'
    ),
    (
        'admin.roles_delegation_scope',
        'Delegation scope',
        'Zakres delegowania',
        'Область делегирования',
        'Тапсыру ауқымы'
    ),
    (
        'admin.roles_scope_assigned',
        'Assigned items only',
        'Tylko przypisane materiały',
        'Только назначенные материалы',
        'Тек тағайындалған материалдар'
    ),
    (
        'admin.roles_scope_any',
        'Any item',
        'Wszystkie materiały',
        'Любые материалы',
        'Кез келген материал'
    ),
    (
        'admin.draft_untitled',
        'Untitled',
        'Bez tytułu',
        'Без названия',
        'Атаусыз'
    ),
    (
        'admin.draft_status_published',
        'Published:',
        'Opublikowano:',
        'Опубликовано:',
        'Жарияланған:'
    ),
    (
        'admin.draft_status_unpublished',
        'Unpublished',
        'Brak publikacji',
        'Не опубликовано',
        'Жарияланбаған'
    ),
    (
        'admin.content_field_title',
        'Title',
        'Tytuł',
        'Заголовок',
        'Тақырып'
    ),
    (
        'admin.content_field_slug',
        'URL slug',
        'Adres URL',
        'Адрес страницы',
        'Бет мекенжайы'
    ),
    (
        'admin.content_field_body',
        'Body',
        'Treść',
        'Текст',
        'Мәтін'
    ),
    (
        'admin.content_field_slug_help',
        'Use lowercase Latin letters, numbers and hyphens.',
        'Użyj małych liter łacińskich, cyfr i łączników.',
        'Используйте строчные латинские буквы, цифры и дефисы.',
        'Кіші латын әріптерін, сандарды және дефистерді қолданыңыз.'
    ),
    (
        'admin.leave_unsaved',
        'Leave this section without saving your changes?',
        'Opuścić sekcję bez zapisania zmian?',
        'Покинуть раздел без сохранения изменений?',
        'Өзгерістерді сақтамай бөлімнен шығу керек пе?'
    ),
    (
        'admin.stay_editing',
        'Keep editing',
        'Wróć do edycji',
        'Продолжить редактирование',
        'Өңдеуді жалғастыру'
    ),
    (
        'admin.discard_changes',
        'Discard changes',
        'Odrzuć zmiany',
        'Отменить изменения',
        'Өзгерістерден бас тарту'
    ),
    (
        'admin.settings_identity',
        'Portal details',
        'Informacje o portalu',
        'О портале',
        'Портал туралы'
    ),
    (
        'admin.settings_appearance',
        'Appearance',
        'Wygląd',
        'Оформление',
        'Безендіру'
    ),
    (
        'admin.settings_languages',
        'Languages',
        'Języki',
        'Языки',
        'Тілдер'
    ),
    (
        'admin.settings_search_messages',
        'Search texts',
        'Szukaj tekstów',
        'Поиск текстов',
        'Мәтіндерді іздеу'
    ),
    (
        'admin.settings_message_group',
        'Section',
        'Sekcja',
        'Раздел',
        'Бөлім'
    ),
    (
        'admin.settings_select_message',
        'Text to edit',
        'Tekst do edycji',
        'Текст для редактирования',
        'Өңделетін мәтін'
    ),
    (
        'admin.settings_message_value',
        'Translation',
        'Tłumaczenie',
        'Перевод',
        'Аударма'
    ),
    (
        'admin.settings_no_messages',
        'No matching texts.',
        'Nie znaleziono pasujących tekstów.',
        'Подходящих текстов нет.',
        'Сәйкес мәтіндер табылмады.'
    ),
    (
        'admin.settings_public_texts',
        'Public portal',
        'Portal publiczny',
        'Публичный портал',
        'Ашық портал'
    ),
    (
        'admin.settings_missing_only',
        'Only untranslated texts',
        'Tylko brakujące tłumaczenia',
        'Только строки без перевода',
        'Тек аударылмаған мәтіндер'
    ),
    (
        'admin.settings_confirm_remove_catalog',
        'Remove this language and its translations from the draft?',
        'Usunąć ten język i jego tłumaczenia ze szkicu?',
        'Удалить этот язык и его переводы из черновика?',
        'Бұл тілді және оның аудармаларын нобайдан алып тастау керек пе?'
    ),
    (
        'admin.settings_complete_catalog',
        'Fill in the missing texts or remove this language.',
        'Przed publikacją uzupełnij brakujące tłumaczenia lub usuń ten język.',
        'Заполните пропуски или удалите этот язык перед публикацией.',
        'Бос аудармаларды толтырыңыз немесе тілді алып тастаңыз.'
    ),
    (
        'admin.workspace_structure',
        'Structure',
        'Struktura',
        'Структура',
        'Құрылым'
    ),
    (
        'admin.settings_translation_language',
        'Language to edit',
        'Język do edycji',
        'Язык перевода',
        'Аударма тілі'
    ),
    (
        'admin.settings_reference_text',
        'Reference text',
        'Tekst odniesienia',
        'Исходный текст',
        'Бастапқы мәтін'
    ),
    (
        'admin.settings_back_to_texts',
        'Back to texts',
        'Wróć do tekstów',
        'К списку текстов',
        'Мәтіндер тізіміне оралу'
    ),
    (
        'admin.settings_choose_accent',
        'Choose accent color',
        'Wybierz kolor akcentu',
        'Выбрать цвет акцента',
        'Акцент түсін таңдау'
    )
),

keys AS (
    INSERT INTO public.ui_message_keys (message_key, placeholders)
    SELECT
        message_key,
        public.ui_placeholders(en) AS placeholders
    FROM initial_messages
    RETURNING message_key
)

INSERT INTO public.initial_ui_messages (
    language_code, message_key, message_value
)
SELECT
    translations.language_code,
    initial_messages.message_key,
    translations.message_value
FROM initial_messages
INNER JOIN keys ON initial_messages.message_key = keys.message_key
CROSS JOIN LATERAL (
    VALUES
    ('en', initial_messages.en),
    ('pl', initial_messages.pl),
    ('ru', initial_messages.ru),
    ('kk', initial_messages.kk)
) AS translations (language_code, message_value);

-- +goose StatementBegin
DO $$
BEGIN
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

COMMENT ON TABLE public.site_revision_messages IS
'Editor-authored messages for a draft or sealed UI catalog.';

COMMENT ON COLUMN public.site_revision_messages.message_value IS
'Plain text with optional {name} placeholders.';

COMMENT ON VIEW public.public_site IS
'Current sealed revision with both configured languages enabled.';

COMMENT ON VIEW public.public_ui_languages IS
'Enabled UI languages with metadata from the current publication.';

COMMENT ON VIEW public.public_ui_messages IS
'Messages for enabled UI languages in the current publication.';

COMMENT ON FUNCTION public.ui_placeholders(
    text
) IS
'Extracts ordered, unique placeholder names from plain text.';

REVOKE ALL ON FUNCTION public.ui_placeholders(text) FROM public;

GRANT EXECUTE ON FUNCTION public.ui_placeholders(text) TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_child() IS
'Locks the parent revision and prevents changes to sealed catalogs.';

REVOKE ALL ON FUNCTION public.guard_site_child() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_child() TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_revision() IS
'Validates the complete catalog at sealing and protects sealed revisions.';

REVOKE ALL ON FUNCTION public.guard_site_revision() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_revision() TO sp_publisher;

COMMENT ON FUNCTION public.guard_site_state() IS
'Protects the singleton version and enforces draft and publication states.';

REVOKE ALL ON FUNCTION public.guard_site_state() FROM public;

GRANT EXECUTE ON FUNCTION public.guard_site_state() TO sp_publisher;

REVOKE ALL ON public.initial_site_defaults,
public.initial_ui_messages,
public.ui_message_keys,
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

COMMENT ON TABLE public.initial_site_defaults IS
'Portal settings defaults used before the first site publication.';

COMMENT ON TABLE public.initial_ui_messages IS
'UI messages for the initial site publication, readable only by the owner.';

COMMENT ON COLUMN public.site_revisions.notice IS
'An empty value hides the public notice.';

COMMENT ON COLUMN public.site_revisions.accent IS
'Theme accent as a hexadecimal RGB color (#RRGGBB).';

-- +goose Down
DROP VIEW public.public_ui_messages;
DROP VIEW public.public_ui_languages;
DROP VIEW public.public_site;
DROP TABLE public.site_state;
DROP TABLE public.site_revision_messages;
DROP TABLE public.site_revision_languages;
DROP TABLE public.site_revision_keys;
DROP TABLE public.site_revisions;
DROP TABLE public.initial_site_defaults;
DROP TABLE public.initial_ui_messages;
DROP TABLE public.ui_message_keys;
DROP FUNCTION public.guard_site_state();
DROP FUNCTION public.guard_site_revision();
DROP FUNCTION public.guard_site_child();
DROP FUNCTION public.ui_placeholders(text);
