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
    -- Separate records with a blank line. Each record lists the key followed
    -- by the English, Polish, Russian and Kazakh translations, one per line.
    catalog CONSTANT text := $catalog$
articles
Articles
Artykuły
Статьи
Мақалалар

events
Events
Wydarzenia
События
Іс-шаралар

empty
No results
Brak wyników
Нет результатов
Нәтиже табылмады

pagination
Pages
Strony
Страницы
Беттер

previous
Previous
Poprzednia
Предыдущая
Алдыңғы

next
Next
Następna
Следующая
Келесі

navigation
Navigation
Nawigacja
Навигация
Навигация

related_content
Related content
Powiązane materiały
Связанные материалы
Байланысты материалдар

all
All
Wszystkie
Все
Барлығы

languages
Languages
Języki
Языки
Тілдер

errors.400
Invalid request
Nieprawidłowe żądanie
Некорректный запрос
Жарамсыз сұрау

errors.404
Page not found
Nie znaleziono
Страница не найдена
Бет табылмады

errors.503
Service unavailable
Usługa niedostępna
Сервис недоступен
Қызмет қолжетімсіз

errors.other
Something went wrong
Wystąpił błąd
Произошла ошибка
Қате орын алды

errors.retry
Try again
Spróbuj ponownie
Повторить
Қайталап көру

errors.home
Home
Strona główna
Главная
Басты бет

admin.name
Administration
Administracja
Администрирование
Әкімшілендіру

admin.language
Language
Język
Язык
Тіл

admin.theme
Theme
Motyw
Тема
Тақырып

admin.system
System
Systemowy
Системная
Жүйелік

admin.light
Light
Jasny
Светлая
Ашық

admin.dark
Dark
Ciemny
Тёмная
Қараңғы

admin.sign_in
Sign in
Zaloguj się
Вход
Кіру

admin.login
Login
Login
Имя пользователя
Пайдаланушы аты

admin.password
Password
Hasło
Пароль
Құпиясөз

admin.continue
Continue
Dalej
Продолжить
Жалғастыру

admin.two_step
Two-step verification
Weryfikacja dwuetapowa
Двухэтапная проверка
Екі қадамдық тексеру

admin.code_help
Enter the six-digit code from your authenticator app.
Wpisz sześciocyfrowy kod z aplikacji uwierzytelniającej.
Введите шестизначный код из приложения для аутентификации.
Аутентификация қолданбасындағы алты таңбалы кодты енгізіңіз.

admin.code
Verification code
Kod weryfikacyjny
Код подтверждения
Растау коды

admin.verify
Verify
Zweryfikuj
Подтвердить
Растау

admin.restart
Start over
Zacznij od nowa
Начать заново
Қайта бастау

admin.signed_in
Signed in
Zalogowano
Вход выполнен
Жүйеге кірдіңіз

admin.sign_out
Sign out
Wyloguj się
Выйти
Шығу

admin.checking
Checking your session…
Sprawdzanie sesji…
Проверяем сессию…
Сессия тексеріліп жатыр…

admin.working
Working…
Przetwarzanie…
Выполняем запрос…
Сұрау орындалып жатыр…

admin.retry
Retry
Spróbuj ponownie
Повторить
Қайталап көру

admin.invalid_credentials
We could not verify your credentials. Check your entries and try again.
Nie udało się zweryfikować danych logowania. Sprawdź je i spróbuj ponownie.
Не удалось проверить данные для входа. Проверьте их и повторите попытку.
Кіру деректері расталмады. Деректерді тексеріп, қайталап көріңіз.

admin.invalid_code
We could not verify the code. Try a fresh code.
Nie udało się zweryfikować kodu. Spróbuj użyć nowego kodu.
Не удалось проверить код. Попробуйте новый код.
Код расталмады. Жаңа кодты енгізіңіз.

admin.unavailable
Administration is temporarily unavailable. Try again.
Panel administracyjny jest chwilowo niedostępny. Spróbuj ponownie.
Администрирование временно недоступно. Повторите попытку.
Әкімшілендіру уақытша қолжетімсіз. Қайталап көріңіз.

admin.limited
Too many attempts. Try again when the button is available.
Zbyt wiele prób. Spróbuj ponownie, gdy przycisk będzie dostępny.
Слишком много попыток. Повторите, когда кнопка станет доступна.
Әрекет тым көп болды. Түйме қайта қолжетімді болғанда қайталап көріңіз.

admin.changed
Your session changed. Check your session to continue.
Sesja się zmieniła. Sprawdź sesję, aby kontynuować.
Сессия изменилась. Проверьте её, чтобы продолжить.
Сессия өзгерді. Жалғастыру үшін сессияны тексеріңіз.

admin.restricted
Account setup or recovery is in progress. You can end that session and start over.
Trwa konfiguracja lub odzyskiwanie konta. Możesz zakończyć tę sesję i zacząć od nowa.
Идёт настройка или восстановление аккаунта. Можно завершить эту сессию и начать заново.
Аккаунт бапталып немесе қалпына келтіріліп жатыр. Сессияны аяқтап, қайта бастай аласыз.

admin.signed_out
You’re signed out.
Wylogowano.
Вы вышли из системы.
Жүйеден шықтыңыз.

admin.settings
Portal settings
Ustawienia portalu
Настройки портала
Портал параметрлері

admin.localization
Languages and translations
Języki i tłumaczenia
Языки и переводы
Тілдер мен аудармалар

admin.portal_name
Portal name
Nazwa portalu
Название портала
Портал атауы

admin.tagline
Tagline
Hasło portalu
Слоган
Ұран

admin.description
Description
Opis
Описание
Сипаттама

admin.notice
Notice
Komunikat
Объявление
Хабарландыру

admin.accent
Accent color
Kolor akcentu
Цвет акцента
Екпін түсі

admin.default_language
Default content language
Domyślny język treści
Язык контента по умолчанию
Мазмұнның әдепкі тілі

admin.fallback_language
Fallback interface language
Zapasowy język interfejsu
Резервный язык интерфейса
Интерфейстің резервтік тілі

admin.interface_texts
Interface texts
Teksty interfejsu
Тексты интерфейса
Интерфейс мәтіндері

admin.save_draft
Save draft
Zapisz szkic
Сохранить черновик
Нобайды сақтау

admin.publish_draft
Publish settings
Opublikuj ustawienia
Опубликовать настройки
Параметрлерді жариялау

admin.settings_review_publish
Review publication
Sprawdź przed publikacją
Проверить перед публикацией
Жариялау алдында тексеру

admin.confirm_publish
Publish the saved settings and interface texts?
Opublikować zapisane ustawienia i teksty interfejsu?
Опубликовать сохранённые настройки и тексты интерфейса?
Сақталған параметрлер мен интерфейс мәтіндерін жариялау керек пе?

admin.draft_saved
Draft saved.
Zapisano szkic.
Черновик сохранён.
Нобай сақталды.

admin.published
Settings published.
Ustawienia opublikowane.
Настройки опубликованы.
Параметрлер жарияланды.

admin.settings_save_changes
Save changes
Zapisz zmiany
Сохранить изменения
Өзгерістерді сақтау

admin.settings_saved
Settings saved.
Ustawienia zapisane.
Настройки сохранены.
Параметрлер сақталды.

admin.settings_pending
Saving also applies the previously saved changes in these sections.
Zapis zastosuje także wcześniej zapisane zmiany w tych sekcjach.
При сохранении также применятся ранее сохранённые изменения в этих разделах.
Сақтағанда осы бөлімдердегі бұрын сақталған өзгерістер де қолданылады.

admin.settings_remove_catalog
Remove interface translations
Usuń tłumaczenia interfejsu
Убрать переводы интерфейса
Интерфейс аудармаларын алып тастау

admin.access_denied
You do not have permission for this action.
Nie masz uprawnień do tego działania.
У вас нет разрешения на это действие.
Бұл әрекетті орындауға рұқсатыңыз жоқ.

admin.settings_conflict
Settings changed in another session. Reload before saving again.
Ustawienia zmieniono w innej sesji. Wczytaj je ponownie przed zapisem.
Настройки изменились в другой сессии. Загрузите их заново перед сохранением.
Параметрлер басқа сессияда өзгерді. Сақтау алдында қайта жүктеңіз.

admin.settings_invalid
Check the portal fields and complete each selected translation.
Sprawdź pola portalu i uzupełnij wszystkie wybrane tłumaczenia.
Проверьте поля портала и заполните все выбранные переводы.
Портал өрістерін тексеріп, таңдалған аудармаларды толтырыңыз.

admin.reload_settings
Discard local edits and reload
Odrzuć lokalne zmiany i wczytaj ponownie
Отбросить локальные правки и загрузить заново
Жергілікті өзгерістерді алып тастап, қайта жүктеу

admin.cancel
Cancel
Anuluj
Отмена
Бас тарту

admin.close
Close
Zamknij
Закрыть
Жабу

admin.fresh_auth
Confirm your identity
Potwierdź tożsamość
Подтвердите личность
Жеке басыңызды растаңыз

admin.unlock
Confirm identity
Potwierdź tożsamość
Подтвердить личность
Жеке басты растау

admin.identity_confirmed
Identity confirmed. Repeat the action to continue.
Potwierdzono tożsamość. Wykonaj czynność ponownie, aby kontynuować.
Личность подтверждена. Повторите действие, чтобы продолжить.
Жеке басыңыз расталды. Жалғастыру үшін әрекетті қайталаңыз.

admin.add_catalog
Add interface language
Dodaj język interfejsu
Добавить язык интерфейса
Интерфейс тілін қосу

admin.step_up_required
Confirm your identity to continue.
Potwierdź tożsamość, aby wykonać tę czynność.
Подтвердите личность, чтобы продолжить.
Жалғастыру үшін жеке басыңызды растаңыз.

admin.remove_catalog
Remove catalog from draft
Usuń katalog ze szkicu
Убрать каталог из черновика
Каталогты нобайдан алып тастау

admin.content
Content
Materiały
Материалы
Материалдар

admin.content_collections
Collections
Kolekcje
Коллекции
Жинақтар

admin.content_collection
Collection
Kolekcja
Коллекция
Жинақ

admin.content_no_collections
No collections are available.
Brak dostępnych kolekcji.
Нет доступных коллекций.
Қолжетімді жинақтар жоқ.

admin.content_more_collections
Load more collections
Wczytaj więcej kolekcji
Загрузить ещё коллекции
Қосымша жинақтарды жүктеу

admin.content_create
Create draft
Utwórz szkic
Создать черновик
Нобай жасау

admin.content_no_drafts
No drafts are available.
Brak dostępnych szkiców.
Нет доступных черновиков.
Қолжетімді нобайлар жоқ.

admin.content_more_drafts
Load more drafts
Wczytaj więcej szkiców
Загрузить ещё черновики
Қосымша нобайларды жүктеу

admin.content_editor
Draft editor
Edytor szkicu
Редактор черновика
Нобай редакторы

admin.content_back
Back to drafts
Wróć do szkiców
К списку черновиков
Нобайлар тізіміне қайту

admin.content_shared
Shared fields
Pola wspólne
Общие поля
Ортақ өрістер

admin.content_translation
Translation
Tłumaczenie
Перевод
Аударма

admin.content_required
required for readiness
wymagane do zatwierdzenia
нужно для готовности
дайындық үшін міндетті

admin.content_empty_value
No value
Brak wartości
Нет значения
Мән жоқ

admin.content_yes
Yes
Tak
Да
Иә

admin.content_no
No
Nie
Нет
Жоқ

admin.content_relation_help
Enter the ID of an accessible target draft. Clear the value to remove the link.
Wpisz identyfikator dostępnego szkicu. Wyczyść pole, aby usunąć powiązanie.
Введите идентификатор доступного черновика. Очистите поле, чтобы убрать связь.
Қолжетімді нобайдың идентификаторын енгізіңіз. Байланысты жою үшін өрісті тазалаңыз.

admin.content_conflict
The draft has changed. Reload the current version to continue.
Szkic został zmieniony. Wczytaj aktualną wersję.
Черновик изменился. Загрузите актуальную версию.
Нобай өзгерді. Ағымдағы нұсқаны қайта жүктеңіз.

admin.content_invalid
Check the draft values and try again.
Sprawdź wartości w szkicu i spróbuj ponownie.
Проверьте значения черновика и повторите попытку.
Нобайдағы мәндерді тексеріп, қайта көріңіз.

admin.content_transition_failed
The action could not be completed. Check readiness, languages and relations.
Nie udało się wykonać operacji. Sprawdź gotowość, języki i powiązania.
Действие не выполнено. Проверьте готовность, языки и связи.
Әрекет орындалмады. Дайындықты, тілдер мен байланыстарды тексеріңіз.

admin.content_required_missing
Complete the highlighted fields before marking the draft ready.
Uzupełnij oznaczone pola przed zatwierdzeniem szkicu.
Заполните отмеченные поля, чтобы подтвердить готовность.
Дайын деп белгілеу үшін көрсетілген өрістерді толтырыңыз.

admin.content_ready_saved
Readiness saved.
Zapisano gotowość.
Готовность сохранена.
Дайындық сақталды.

admin.content_shared_ready
Shared fields are ready.
Pola wspólne są gotowe.
Общие поля готовы.
Ортақ өрістер дайын.

admin.content_locale_ready
This translation is ready.
To tłumaczenie jest gotowe.
Перевод готов.
Бұл аударма дайын.

admin.content_mark_shared
Mark shared fields ready
Zatwierdź pola wspólne
Подтвердить готовность общих полей
Ортақ өрістерді дайын деп белгілеу

admin.content_mark_locale
Mark translation ready
Zatwierdź tłumaczenie
Подтвердить готовность перевода
Аударманы дайын деп белгілеу

admin.content_language_disabled
This language is disabled and its translation cannot be published.
Ten język jest wyłączony. Publikacja tłumaczenia jest niedostępna.
Язык отключён. Опубликовать этот перевод нельзя.
Тіл өшірілген. Бұл аударманы жариялау мүмкін емес.

admin.content_public_links
Published translations
Opublikowane tłumaczenia
Опубликованные переводы
Жарияланған аудармалар

admin.content_not_published
No translations are published.
Brak opublikowanych tłumaczeń.
Нет опубликованных переводов.
Жарияланған аударма жоқ.

admin.content_publication_heading
Publication
Publikacja
Публикация
Жариялау

admin.content_save_before_publication
Save the draft before checking or publishing it.
Zapisz szkic przed sprawdzeniem lub publikacją.
Сохраните черновик перед проверкой или публикацией.
Тексеру немесе жариялау алдында нобайды сақтаңыз.

admin.content_prepare_translation
Check translation
Sprawdź tłumaczenie
Проверить перевод
Аударманы тексеру

admin.content_publication_publish_hint
Saved translations can be checked and published here.
Tutaj możesz sprawdzić i opublikować zapisane tłumaczenia.
Здесь можно проверить и опубликовать сохранённые переводы.
Сақталған аудармаларды осы жерде тексеріп, жариялауға болады.

admin.content_publication_check_hint
Check the draft to prepare it for publication.
Sprawdź szkic, aby przygotować go do publikacji.
Проверьте черновик перед передачей на публикацию.
Жариялауға дайындау үшін нобайды тексеріңіз.

admin.content_publication_handoff
This translation is ready for someone with publishing access.
Tłumaczenie jest gotowe do publikacji przez osobę z uprawnieniami.
Перевод готов. Его может опубликовать сотрудник с нужными правами.
Аударма дайын. Оны жариялау құқығы бар қызметкер жариялай алады.

admin.content_publication_add_translation
Fill in the translation and save the draft.
Uzupełnij tłumaczenie i zapisz szkic.
Заполните перевод и сохраните черновик.
Аударманы толтырып, нобайды сақтаңыз.

admin.content_publication_await_checks
An editor must check the saved translation and shared fields before you can publish them.
Przed publikacją redaktor musi sprawdzić zapisane tłumaczenie i pola wspólne.
Перед публикацией редактор должен проверить сохранённый перевод и общие поля.
Жариялау алдында редактор сақталған аударманы және ортақ өрістерді тексеруі керек.

admin.content_publication_access
This account cannot check or publish this draft.
To konto nie może sprawdzić ani opublikować szkicu.
У этой учётной записи нет прав на проверку или публикацию черновика.
Бұл тіркелгі нобайды тексере немесе жариялай алмайды.

admin.content_publication_schema_access
The collection structure changed. Ask someone who can edit all fields to update the draft.
Struktura kolekcji się zmieniła. Poproś osobę z prawem edycji wszystkich pól o aktualizację szkicu.
Структура коллекции изменилась. Обновить черновик может сотрудник с правом редактирования всех полей.
Жинақ құрылымы өзгерді. Нобайды барлық өрісті өңдей алатын қызметкер жаңартуы керек.

admin.content_publication_translations_need_checks
When publishing shared changes, include every translation that is already published.
Publikując wspólne zmiany, uwzględnij wszystkie wcześniej opublikowane tłumaczenia.
При публикации изменений в общих полях включите все уже опубликованные переводы.
Ортақ өзгерістерді жариялағанда бұрын жарияланған барлық аударманы қосыңыз.

admin.content_publication_await_published_checks
Have every already published translation checked again before publishing shared changes.
Przed publikacją wspólnych zmian poproś o ponowne sprawdzenie wszystkich opublikowanych tłumaczeń.
Перед публикацией общих изменений нужно заново проверить все опубликованные переводы.
Ортақ өзгерістерді жарияламас бұрын бұрын жарияланған барлық аударманы қайта тексеру керек.

admin.content_publication_unknown
Publication could not be confirmed. Reload the draft and check the published links before trying again.
Nie można potwierdzić publikacji. Wczytaj szkic ponownie i sprawdź linki przed kolejną próbą.
Подтвердить публикацию не удалось. Перед повторной попыткой перезагрузите черновик и проверьте ссылки.
Жарияланымды растау мүмкін болмады. Қайталап көрмес бұрын нобайды жүктеп, сілтемелерді тексеріңіз.

admin.content_publication_reload_links
Reload the draft to view the published links.
Wczytaj szkic ponownie, aby zobaczyć linki do publikacji.
Перезагрузите черновик, чтобы увидеть ссылки на публикации.
Жарияланым сілтемелерін көру үшін нобайды қайта жүктеңіз.

admin.content_publication_route_conflict
This page address is already in use. Change the title and save the draft.
Ten adres strony jest już zajęty. Zmień tytuł i zapisz szkic.
Этот адрес страницы уже занят. Измените заголовок и сохраните черновик.
Бұл бет мекенжайы бос емес. Тақырыпты өзгертіп, нобайды сақтаңыз.

admin.content_publication_relation_unavailable
A linked item is unavailable. Check the linked items for the selected languages.
Powiązany materiał jest niedostępny. Sprawdź powiązania w wybranych językach.
Связанный материал недоступен. Проверьте связи для выбранных языков.
Байланысқан материал қолжетімсіз. Таңдалған тілдердегі байланыстарды тексеріңіз.

admin.content_publication_address_required
Save the draft with a title so its page address can be generated.
Zapisz szkic z tytułem, aby utworzyć adres strony.
Сохраните черновик с заголовком, чтобы создать адрес страницы.
Бет мекенжайын жасау үшін тақырыбы бар нобайды сақтаңыз.

admin.content_publish
Publish translations
Opublikuj tłumaczenia
Опубликовать переводы
Аудармаларды жариялау

admin.content_unpublish
Unpublish
Wycofaj publikację
Снять с публикации
Жарияланымды алып тастау

admin.content_published
Translations published.
Opublikowano tłumaczenia.
Переводы опубликованы.
Аудармалар жарияланды.

admin.content_unpublished
Publication removed.
Wycofano publikację.
Материал снят с публикации.
Жарияланым алып тасталды.

admin.content_adopt_schema
Adopt current schema
Zastosuj aktualny schemat
Принять актуальную схему
Ағымдағы схеманы қабылдау

admin.content_schema_adopted
Current schema adopted.
Zastosowano aktualny schemat.
Актуальная схема принята.
Ағымдағы схема қабылданды.

admin.content_schema_outdated
A newer schema is available. Adopting it resets readiness.
Dostępny jest nowszy schemat. Jego zastosowanie zeruje gotowość.
Доступна новая схема. Её принятие сбросит готовность.
Жаңа схема қолжетімді. Оны қабылдау дайындық күйін тазартады.

admin.content_reload
Reload draft
Wczytaj szkic ponownie
Перезагрузить черновик
Нобайды қайта жүктеу

admin.content_confirm
Confirm
Potwierdź
Подтвердить
Растау

admin.content_confirm_publish
Publish the selected translations?
Opublikować wybrane tłumaczenia?
Опубликовать выбранные переводы?
Таңдалған аудармалар жариялансын ба?

admin.content_confirm_unpublish
Remove this publication and clear readiness?
Wycofać publikację i wyzerować gotowość?
Снять материал с публикации и сбросить готовность?
Жарияланым алынып, дайындық күйі тазартылсын ба?

admin.content_confirm_reload
Discard unsaved changes and reload?
Odrzucić niezapisane zmiany i wczytać ponownie?
Отбросить несохранённые изменения и перечитать черновик?
Сақталмаған өзгерістер тасталып, нобай қайта жүктелсін бе?

admin.content_confirm_close
Close the draft and discard unsaved changes?
Zamknąć szkic i odrzucić niezapisane zmiany?
Закрыть черновик и отбросить несохранённые изменения?
Нобай жабылып, сақталмаған өзгерістер тасталсын ба?

admin.content_publish_languages
Languages to publish
Języki do publikacji
Языки публикации
Жариялау тілдері

admin.content_clear_relation
Clear relation
Usuń powiązanie
Удалить связь
Байланысты жою

admin.content_links_new_tab
Published links open in a new tab.
Linki do publikacji otwierają się w nowej karcie.
Ссылки на публикации открываются в новой вкладке.
Жарияланым сілтемелері жаңа қойындыда ашылады.

admin.registry
Type Builder
Kreator typów
Конструктор типов
Түрлер құрастырғышы

admin.registry_choose
Collection to configure
Kolekcja do skonfigurowania
Коллекция для настройки
Бапталатын жинақ

admin.registry_article
Article
Artykuł
Статья
Мақала

admin.registry_event
Event
Wydarzenie
Событие
Іс-шара

admin.registry_translation_language
Field translation language
Język tłumaczenia pola
Язык перевода поля
Өріс аудармасының тілі

admin.registry_translation_help
Leave a translation blank to use the default when one is available.
Pozostaw puste tłumaczenie, aby użyć tekstu domyślnego, jeśli jest dostępny.
Оставьте перевод пустым, чтобы использовать текст по умолчанию, если он есть.
Әдепкі мәтін бар болса, оны қолдану үшін аударманы бос қалдырыңыз.

admin.registry_schema_help
Adding an optional field creates a new schema version. Editing labels and help text leaves the version unchanged.
Dodanie pola opcjonalnego tworzy nową wersję schematu. Etykiety i podpowiedzi można zmieniać w bieżącej wersji.
Добавление необязательного поля создаёт новую версию структуры. Правка подписей и подсказок сохраняет версию.
Міндетті емес өріс қосылса, құрылымның жаңа нұсқасы жасалады. Атаулар мен кеңестерді өзгерткенде нұсқа сақталады.

admin.registry_field_locked
The field structure is fixed to preserve compatibility with drafts and publications. You can edit its label and help text.
Definicja pola jest stała, aby zachować zgodność ze szkicami i publikacjami. Można zmieniać etykietę i podpowiedź.
Определение поля закреплено за черновиками и публикациями. Подпись и подсказку можно менять.
Өріс анықтамасы нобайлармен және жарияланымдармен байланысты. Атауы мен кеңесін өзгертуге болады.

admin.registry_template_locked
The collection template defines this field. You can edit its label and help text.
To pole pochodzi z szablonu kolekcji. Można zmieniać etykietę i podpowiedź.
Поле задано шаблоном коллекции. Подпись и подсказку можно менять.
Өріс жинақ үлгісінде берілген. Атауы мен кеңесін өзгертуге болады.

admin.registry_scope_localized
Per language
Osobno dla każdego języka
Отдельно для каждого языка
Әр тілге бөлек

admin.registry_scope_shared
Shared across languages
Wspólne dla wszystkich języków
Общее для всех языков
Барлық тілге ортақ

admin.registry_required
Required
Wymagane
Обязательное
Міндетті

admin.registry_optional
Optional
Opcjonalne
Необязательное
Міндетті емес

admin.registry_unnamed_field
Unnamed field
Pole bez nazwy
Поле без имени
Атауы жоқ өріс

admin.registry_kind_string
Short text
Krótki tekst
Короткий текст
Қысқа мәтін

admin.registry_kind_text
Long text
Długi tekst
Длинный текст
Ұзын мәтін

admin.registry_kind_markdown
Markdown
Markdown
Markdown
Markdown

admin.registry_kind_boolean
Yes or no
Tak lub nie
Да или нет
Иә немесе жоқ

admin.registry_kind_integer
Whole number
Liczba całkowita
Целое число
Бүтін сан

admin.registry_kind_decimal
Decimal number
Liczba dziesiętna
Десятичное число
Ондық сан

admin.registry_kind_datetime
Date and time
Data i godzina
Дата и время
Күн мен уақыт

admin.registry_kind_enum
List of values
Lista wartości
Список значений
Мәндер тізімі

admin.registry_kind_relation
Link to another collection
Powiązanie z inną kolekcją
Связь с другой коллекцией
Басқа жинақпен байланыс

admin.registry_schema
Current schema
Wersja struktury
Версия структуры
Құрылым нұсқасы

admin.registry_field_key
Field key
Klucz pola
Ключ поля
Өріс кілті

admin.registry_kind
Field type
Typ pola
Тип поля
Өріс түрі

admin.registry_scope
Field scope
Zakres pola
Область поля
Өріс ауқымы

admin.registry_visibility
Visibility
Widoczność
Видимость
Көрінуі

admin.registry_private
Private
Prywatne
Закрытое
Жеке

admin.registry_public
Public
Publiczne
Публичное
Жария

admin.registry_codes
Allowed values, separated by commas
Dozwolone wartości oddzielone przecinkami
Разрешённые значения через запятую
Үтірмен бөлінген рұқсатты мәндер

admin.registry_target
Target collection
Kolekcja docelowa
Целевая коллекция
Мақсатты жинақ

admin.registry_new_field
New optional field
Nowe pole opcjonalne
Новое необязательное поле
Жаңа міндетті емес өріс

admin.registry_remove_field
Remove new field
Usuń nowe pole
Удалить новое поле
Жаңа өрісті жою

admin.registry_label
Field label
Etykieta pola
Подпись поля
Өріс атауы

admin.registry_help
Field help text
Podpowiedź dla pola
Подсказка к полю
Өріске арналған кеңес

admin.registry_add_field
Add optional field
Dodaj pole opcjonalne
Добавить необязательное поле
Міндетті емес өрісті қосу

admin.registry_initial_access
Grant me access to this new collection
Przyznaj mi dostęp do nowej kolekcji
Выдать мне доступ к новой коллекции
Маған жаңа жинаққа қолжетімділік беру

admin.registry_new_field_access
Extend my existing access to the new fields
Rozszerz mój dostęp na nowe pola
Распространить мои текущие права на новые поля
Қолданыстағы қолжетімділігімді жаңа өрістерге кеңейту

admin.registry_save
Save collection changes
Zapisz zmiany kolekcji
Сохранить изменения коллекции
Жинақ өзгерістерін сақтау

admin.registry_create
Create collection
Utwórz kolekcję
Создать коллекцию
Жинақ жасау

admin.registry_created
Collection created.
Utworzono kolekcję.
Коллекция создана.
Жинақ жасалды.

admin.registry_revised
Collection changes saved.
Zapisano zmiany kolekcji.
Изменения коллекции сохранены.
Жинақ өзгерістері сақталды.

admin.registry_access_applied
Granted permissions
Przyznane uprawnienia
Выданные разрешения
Берілген рұқсаттар

admin.registry_reload
Reload collection
Wczytaj kolekcję ponownie
Перечитать коллекцию
Жинақты қайта жүктеу

admin.registry_conflict
The collection has changed. Reload it before saving.
Kolekcja została zmieniona. Wczytaj ją ponownie przed zapisem.
Коллекция изменилась. Перечитайте её перед сохранением.
Жинақ өзгерді. Сақтау алдында оны қайта жүктеңіз.

admin.registry_invalid
Check the field definitions and try again.
Sprawdź definicje pól i spróbuj ponownie.
Проверьте определения полей и повторите попытку.
Өріс анықтамаларын тексеріп, қайталап көріңіз.

admin.account
My account
Moje konto
Моя учётная запись
Менің тіркелгім

admin.account_codes
Recovery codes
Kody odzyskiwania
Коды восстановления
Қалпына келтіру кодтары

admin.account_codes_help
Save these codes somewhere secure. Sign in again after closing this screen.
Zapisz kody w bezpiecznym miejscu. Po zamknięciu zaloguj się ponownie.
Сохраните коды в безопасном месте. После закрытия войдите снова.
Кодтарды қауіпсіз жерде сақтаңыз. Осы бетті жапқан соң қайта кіріңіз.

admin.account_done
Done
Gotowe
Готово
Дайын

admin.account_codes_remaining
Unused recovery codes:
Niewykorzystane kody:
Неиспользованные коды:
Пайдаланылмаған кодтар:

admin.account_change_password
Change password
Zmień hasło
Сменить пароль
Құпиясөзді өзгерту

admin.account_replace_factor
Replace authenticator
Zmień aplikację uwierzytelniającą
Заменить приложение-аутентификатор
Аутентификаторды ауыстыру

admin.account_regenerate_codes
Generate new recovery codes
Wygeneruj nowe kody odzyskiwania
Выпустить новые коды восстановления
Жаңа қалпына келтіру кодтарын жасау

admin.account_replace_codes_warning
Old recovery codes will stop working. All sessions will end.
Stare kody przestaną działać. Wszystkie sesje zostaną zakończone.
Прежние коды перестанут действовать. Все сессии завершатся.
Ескі кодтар жарамсыз болады. Барлық сеанстар аяқталады.

admin.account_logout_warning
Confirming will end all your sessions.
Po zatwierdzeniu wszystkie sesje zostaną zakończone.
После подтверждения все ваши сессии завершатся.
Растағаннан кейін барлық сеанстарыңыз аяқталады.

admin.account_new_password
New password
Nowe hasło
Новый пароль
Жаңа құпиясөз

admin.account_factor_help
Add this key to your replacement authenticator and enter its code.
Dodaj ten klucz do nowego uwierzytelniacza i wpisz wygenerowany kod.
Добавьте ключ в новый аутентификатор и введите полученный код.
Бұл кілтті жаңа аутентификаторға қосып, көрсетілген кодты енгізіңіз.

admin.account_revoke_confirmation
End the session started at
Zakończyć sesję rozpoczętą:
Завершить сессию, начатую
Басталған сеансты аяқтау:

admin.account_sessions
Active sessions
Aktywne sesje
Активные сессии
Белсенді сеанстар

admin.account_current_session
Current session
Bieżąca sesja
Текущая сессия
Ағымдағы сеанс

admin.account_started
Started
Rozpoczęcie
Начало
Басталуы

admin.account_last_seen
Last active
Ostatnia aktywność
Последняя активность
Соңғы белсенділік

admin.account_expires
Expires
Wygaśnięcie
Истекает
Аяқталуы

admin.account_revoke_session
Revoke session
Zakończ sesję
Завершить сессию
Сеансты аяқтау

admin.account_revoke_all
Revoke all sessions
Zakończ wszystkie sesje
Завершить все сессии
Барлық сеансты аяқтау

admin.account_validation
Check your input and try again. Passwords must have 15 to 128 characters.
Sprawdź dane i spróbuj ponownie. Hasło musi mieć od 15 do 128 znaków.
Проверьте данные и повторите попытку. Длина пароля — от 15 до 128 символов.
Деректерді тексеріп, қайталап көріңіз. Құпиясөз 15–128 таңбадан тұруы керек.

admin.audit
Audit log
Dziennik audytu
Журнал аудита
Аудит журналы

admin.audit_empty
No events on this page.
Brak zdarzeń na tej stronie.
На этой странице нет событий.
Бұл бетте оқиғалар жоқ.

admin.audit_success
Succeeded
Powodzenie
Выполнено
Орындалды

admin.audit_denied
Denied
Odmowa
Отклонено
Қабылданбады

admin.audit_failure
Failed
Błąd
Ошибка
Қате

admin.audit_actor
Actor ID
Identyfikator wykonawcy
Идентификатор инициатора
Орындаушы идентификаторы

admin.audit_target
Target ID
Identyfikator celu
Идентификатор цели
Нысан идентификаторы

admin.audit_request
Request ID
Identyfikator żądania
Идентификатор запроса
Сұрау идентификаторы

admin.audit_reason
Reason
Powód
Причина
Себебі

admin.entry_enrollment
I have an invitation
Mam zaproszenie
У меня есть приглашение
Менде шақыру бар

admin.entry_recovery
Recover access
Odzyskaj dostęp
Восстановить доступ
Қолжетімділікті қалпына келтіру

admin.entry_invitation
Invitation token
Token zaproszenia
Токен приглашения
Шақыру токені

admin.entry_recovery_code
Recovery code
Kod odzyskiwania
Код восстановления
Қалпына келтіру коды

admin.entry_invalid
These credentials could not be verified. Check them and try again.
Nie można potwierdzić tych danych. Sprawdź je i spróbuj ponownie.
Не удалось проверить данные. Проверьте их и повторите попытку.
Деректер расталмады. Тексеріп, қайталап көріңіз.

admin.entry_interrupted
Setup was interrupted. Request a new token if you were using an invitation.
Konfiguracja została przerwana. Aby użyć zaproszenia, poproś o nowy token.
Настройка прервана. Если вы использовали приглашение, запросите новый токен.
Баптау үзілді. Шақыру пайдаланылса, жаңа токен сұраңыз.

admin.entry_activation
Save the codes somewhere secure. An administrator still needs to activate your account.
Zapisz kody w bezpiecznym miejscu. Administrator musi jeszcze aktywować konto.
Сохраните коды в безопасном месте. Администратор должен активировать вашу учётную запись.
Кодтарды қауіпсіз жерде сақтаңыз. Әкімші тіркелгіңізді әлі белсендіруі керек.

admin.access
Access
Dostęp
Доступ
Қолжетімділік

admin.access_saved
Changes saved.
Zapisano zmiany.
Изменения сохранены.
Өзгерістер сақталды.

admin.access_reload
Reload
Wczytaj ponownie
Перезагрузить
Қайта жүктеу

admin.access_version_conflict
This record changed. Reload it and review your changes before retrying.
Dane zostały zmienione. Wczytaj je ponownie i sprawdź swój wybór.
Запись изменилась. Перезагрузите её и проверьте изменения перед повтором.
Жазба өзгерді. Қайта жүктеп, өзгерістерді тексеріңіз.

admin.access_last_admin
At least one administrator must keep access and be able to recover their account.
Co najmniej jeden administrator musi zachować dostęp i możliwość odzyskania konta.
Хотя бы один администратор должен сохранить доступ и возможность восстановления записи.
Кемінде бір әкімшінің қолжетімділігі мен тіркелгіні қалпына келтіру мүмкіндігі сақталуы керек.

admin.access_transition
This action is unavailable in the current state.
Zmiana jest niedostępna w bieżącym stanie.
Действие недоступно при текущем состоянии.
Бұл әрекет ағымдағы күйде қолжетімсіз.

admin.access_role_in_use
Remove the role’s dependencies before deleting it.
Usuń powiązania roli przed jej usunięciem.
Удалите зависимости роли перед её удалением.
Рөлді жоймас бұрын тәуелділіктерін алып тастаңыз.

admin.access_unavailable_target
The selected record is unavailable.
Wybrany rekord jest niedostępny.
Выбранная запись недоступна.
Таңдалған жазба қолжетімсіз.

admin.access_input
Check the fields and try again.
Sprawdź pola i spróbuj ponownie.
Проверьте поля и повторите попытку.
Өрістерді тексеріп, қайталап көріңіз.

admin.access_state_pending
Pending
Oczekujące
Ожидает настройки
Баптауды күтуде

admin.access_state_disabled
Disabled
Wyłączone
Отключено
Өшірілген

admin.access_state_active
Active
Aktywne
Активно
Белсенді

admin.access_state_archived
Archived
Zarchiwizowane
В архиве
Мұрағатталған

admin.access_enable
Enable
Włącz
Включить
Қосу

admin.access_disable
Disable
Wyłącz
Отключить
Өшіру

admin.access_archive
Archive
Archiwizuj
Архивировать
Мұрағаттау

admin.access_restore
Restore
Przywróć
Восстановить
Қалпына келтіру

admin.access_delete
Delete
Usuń
Удалить
Жою

admin.users
Accounts
Konta
Учётные записи
Тіркелгілер

admin.users_create
Create account
Utwórz konto
Создать учётную запись
Тіркелгі жасау

admin.users_created
Pending account created.
Utworzono oczekujące konto.
Создана ожидающая учётная запись.
Баптауды күтетін тіркелгі жасалды.

admin.users_issue_invite
Issue invitation
Wydaj zaproszenie
Выдать приглашение
Шақыру беру

admin.users_view_roles
Manage roles
Zarządzaj rolami
Управлять ролями
Рөлдерді басқару

admin.users_activate
Activate account
Aktywuj konto
Активировать учётную запись
Тіркелгіні белсендіру

admin.users_memberships
Account roles
Role konta
Роли учётной записи
Тіркелгі рөлдері

admin.users_apply_roles
Save role assignments
Zapisz przypisania ról
Сохранить роли
Рөл тағайындауларын сақтау

admin.users_role_code
Role code
Kod roli
Код роли
Рөл коды

admin.users_role_status
Status
Stan
Состояние
Күйі

admin.users_roles_assigned
Assigned
Przypisana
Назначена
Тағайындалған

admin.users_roles_mixed
Multiple domains
Wiele obszarów
Несколько разделов
Бірнеше бөлім

admin.users_roles_other
Other roles
Pozostałe role
Другие роли
Басқа рөлдер

admin.users_roles_none
No roles are available.
Brak dostępnych ról.
Нет доступных ролей.
Қолжетімді рөлдер жоқ.

admin.users_roles_unavailable
Some selected roles are no longer available. Review the remaining selection.
Część wybranych ról nie jest już dostępna. Sprawdź pozostałe role.
Некоторые выбранные роли больше недоступны. Проверьте оставшиеся роли.
Таңдалған кейбір рөлдер енді қолжетімсіз. Қалған рөлдерді тексеріңіз.

admin.users_roles_limit
Uncheck a role to assign another.
Odznacz rolę, aby przypisać inną.
Снимите отметку с одной из ролей, чтобы назначить другую.
Басқа рөлді тағайындау үшін бір рөлдің белгісін алып тастаңыз.

admin.users_confirm_invite
A new token replaces the previous invitation and interrupts any enrollment already in progress.
Nowy token unieważni poprzednie zaproszenie i przerwie rozpoczętą konfigurację.
Новый токен заменит прежнее приглашение и прервёт начатую настройку.
Жаңа токен бұрынғы шақыруды ауыстырып, басталған баптауды тоқтатады.

admin.users_confirm_activate
Activate this account and enable its active role grants?
Aktywować konto i przywrócić uprawnienia jego aktywnych ról?
Активировать запись и применить разрешения её активных ролей?
Тіркелгіні және оның белсенді рөлдерінің рұқсаттарын қосу керек пе?

admin.users_confirm_disable
Disable this account and revoke its sessions?
Wyłączyć konto i zakończyć jego sesje?
Отключить запись и завершить её сессии?
Тіркелгіні өшіріп, оның сеанстарын аяқтау керек пе?

admin.users_invite_delivery
Send this one-time token through a verified channel.
Przekaż ten jednorazowy token zweryfikowanym kanałem.
Передайте одноразовый токен по проверенному каналу.
Бір реттік токенді тексерілген арнамен жіберіңіз.

admin.roles
Roles
Role
Роли
Рөлдер

admin.role_rule_domain
Permission area
Obszar uprawnień
Область доступа
Қолжетімділік саласы

admin.role_rule_field_help
Access applies only to selected fields.
Dostęp obejmuje tylko zaznaczone pola.
Доступны только отмеченные поля.
Тек белгіленген өрістерге қол жеткізуге болады.

admin.role_rule_filter
Collection or installation
Kolekcja lub instalacja
Коллекция или портал
Жинақ немесе портал

admin.role_rule_all_scopes
All scopes
Wszystkie zakresy
Все области
Барлық ауқым

admin.role_rule_scope_installation
Entire portal
Cały portal
Весь портал
Бүкіл портал

admin.role_rule_scope_type
Collection
Kolekcja
Коллекция
Жинақ

admin.role_rule_scope_assigned
Assigned items
Przypisane materiały
Назначенные материалы
Тағайындалған материалдар

admin.role_rule_scope_any
All items
Wszystkie materiały
Все материалы
Барлық материалдар

admin.role_rule_group_content
Content
Treści
Материалы
Материалдар

admin.role_rule_group_structure
Collection structure
Struktura kolekcji
Структура коллекций
Жинақ құрылымы

admin.role_rule_group_accounts
Accounts
Konta
Учётные записи
Тіркелгілер

admin.role_rule_group_roles
Roles and delegation
Role i delegowanie
Роли и делегирование
Рөлдер және өкілеттік беру

admin.role_rule_group_portal
Portal settings
Ustawienia portalu
Настройки портала
Портал баптаулары

admin.role_rule_group_security
Security and audit
Bezpieczeństwo i audyt
Безопасность и аудит
Қауіпсіздік және аудит

admin.role_rule_group_other
Other roles
Pozostałe role
Другие роли
Басқа рөлдер

admin.role_rule_group_mixed
Multiple domains
Kilka obszarów
Несколько областей
Бірнеше сала

admin.role_rule_configure
Configure
Skonfiguruj
Настроить
Баптау

admin.role_rule_coverage
Available grant scope
Dostępny zakres uprawnienia
Доступный охват разрешения
Рұқсаттың қолжетімді ауқымы

admin.role_rule_limit
This role has the maximum number of rules.
Ta rola ma maksymalną liczbę reguł.
В роли уже максимальное число правил.
Бұл рөлде ережелер саны шегіне жетті.

admin.role_rule_choose_delegated_operation
Choose at least one operation for each delegation rule.
Wybierz co najmniej jedną operację dla każdej reguły delegowania.
Выберите хотя бы одну операцию для каждого правила делегирования.
Өкілеттік берудің әр ережесі үшін кемінде бір операцияны таңдаңыз.

admin.permission.content.items.create
Create items
Tworzenie materiałów
Создание материалов
Материал жасау

admin.permission.content.items.read
Read items
Odczyt materiałów
Просмотр материалов
Материалдарды қарау

admin.permission.content.items.update
Edit items
Edycja materiałów
Редактирование материалов
Материалдарды өңдеу

admin.permission.content.items.ready
Mark items ready
Oznaczanie materiałów jako gotowe
Отметка готовности материалов
Материалдарды дайын деп белгілеу

admin.permission.content.items.publish
Publish items
Publikowanie materiałów
Публикация материалов
Материалдарды жариялау

admin.permission.content.items.unpublish
Unpublish items
Wycofywanie publikacji materiałów
Снятие материалов с публикации
Материалдарды жарияланымнан алу

admin.permission.content.items.assign
Assign items to users
Przypisywanie materiałów użytkownikom
Назначение материалов пользователям
Материалдарды пайдаланушыларға тағайындау

admin.permission.content_types.manage
Manage collections and fields
Zarządzanie kolekcjami i polami
Управление коллекциями и полями
Жинақтар мен өрістерді басқару

admin.permission.content_types.initialize_access
Set initial collection access
Ustawianie początkowego dostępu do kolekcji
Настройка начального доступа к коллекциям
Жинақтарға бастапқы қолжетімділікті баптау

admin.permission.content.permissions.delegate
Delegate content permissions
Delegowanie uprawnień do treści
Делегирование прав на материалы
Материалдарға құқықтарды беру

admin.permission.users.create
Create accounts and issue invitations
Tworzenie kont i wysyłanie zaproszeń
Создание учётных записей и выдача приглашений
Тіркелгі жасау және шақыру беру

admin.permission.users.read
View accounts
Przeglądanie kont
Просмотр учётных записей
Тіркелгілерді қарау

admin.permission.users.activate
Activate accounts
Aktywowanie kont
Активация учётных записей
Тіркелгілерді белсендіру

admin.permission.users.disable
Disable accounts
Wyłączanie kont
Отключение учётных записей
Тіркелгілерді өшіру

admin.permission.roles.create
Create and copy roles
Tworzenie i kopiowanie ról
Создание и копирование ролей
Рөлдерді жасау және көшіру

admin.permission.roles.read
View roles
Przeglądanie ról
Просмотр ролей
Рөлдерді қарау

admin.permission.roles.edit
Edit roles
Edycja ról
Редактирование ролей
Рөлдерді өңдеу

admin.permission.roles.assign
Assign roles to accounts
Przypisywanie ról do kont
Назначение ролей учётным записям
Тіркелгілерге рөл тағайындау

admin.permission.roles.disable
Disable roles
Wyłączanie ról
Отключение ролей
Рөлдерді өшіру

admin.permission.roles.enable
Enable roles
Włączanie ról
Включение ролей
Рөлдерді қосу

admin.permission.roles.archive
Archive roles
Archiwizowanie ról
Архивация ролей
Рөлдерді мұрағаттау

admin.permission.roles.restore
Restore archived roles
Przywracanie zarchiwizowanych ról
Восстановление архивных ролей
Мұрағатталған рөлдерді қалпына келтіру

admin.permission.roles.delete
Delete archived roles
Usuwanie zarchiwizowanych ról
Удаление архивных ролей
Мұрағатталған рөлдерді жою

admin.permission.sessions.revoke_any
Revoke sessions for other users
Unieważnianie sesji innych użytkowników
Завершение сеансов других пользователей
Басқа пайдаланушылардың сеанстарын аяқтау

admin.permission.audit.read
Read the audit log
Odczyt dziennika audytu
Просмотр журнала аудита
Аудит журналын қарау

admin.permission.settings.manage
Manage portal settings
Zarządzanie ustawieniami portalu
Управление настройками портала
Портал баптауларын басқару

admin.permission.security_policy.manage
Manage session limits
Zarządzanie limitami sesji
Управление сроками сеансов
Сеанс шектеулерін басқару

admin.roles_create
Create role
Utwórz rolę
Создать роль
Рөл жасау

admin.roles_edit
Edit role policy
Edytuj politykę roli
Изменить политику роли
Рөл саясатын өңдеу

admin.roles_copy
Copy role
Skopiuj rolę
Скопировать роль
Рөлді көшіру

admin.roles_name
Role name
Nazwa roli
Название роли
Рөл атауы

admin.roles_grants
Role grants
Uprawnienia roli
Разрешения роли
Рөл рұқсаттары

admin.roles_remove_grant
Remove grant
Usuń uprawnienie
Удалить разрешение
Рұқсатты алып тастау

admin.roles_operation
Operation and scope
Operacja i zakres
Операция и область
Операция және ауқым

admin.roles_choose_operation
Choose a grant
Wybierz uprawnienie
Выберите разрешение
Рұқсатты таңдаңыз

admin.roles_fields
Fields
Pola
Поля
Өрістер

admin.roles_delegated_operations
Delegated operations
Operacje do delegowania
Делегируемые операции
Тапсырылатын операциялар

admin.roles_allow_delegation
Allow this grant to be delegated
Zezwól na przekazywanie tego uprawnienia
Разрешить делегирование этого разрешения
Осы рұқсатты тапсыруға рұқсат беру

admin.roles_add_grant
Add grant
Dodaj uprawnienie
Добавить разрешение
Рұқсат қосу

admin.roles_save
Save role
Zapisz rolę
Сохранить роль
Рөлді сақтау

admin.roles_duplicate
A grant for this operation and scope already exists.
Istnieje już uprawnienie dla tej operacji i zakresu.
Разрешение для этой операции и области уже существует.
Осы операция мен ауқымға арналған рұқсат бар.

admin.roles_confirm_enable
Enable this role’s grants for its members?
Włączyć uprawnienia tej roli dla jej członków?
Включить разрешения этой роли для её участников?
Рөл мүшелері үшін рұқсаттарды қосу керек пе?

admin.roles_confirm_disable
Disable this role’s grants for its members?
Wyłączyć uprawnienia tej roli dla jej członków?
Отключить разрешения этой роли для её участников?
Рөл мүшелері үшін рұқсаттарды өшіру керек пе?

admin.roles_confirm_archive
Archive this disabled role?
Przenieść tę wyłączoną rolę do archiwum?
Архивировать отключённую роль?
Өшірілген рөлді мұрағаттау керек пе?

admin.roles_confirm_restore
Restore this role as disabled?
Przywrócić rolę jako wyłączoną?
Восстановить роль в отключённом состоянии?
Рөлді өшірілген күйде қалпына келтіру керек пе?

admin.roles_confirm_delete
Permanently delete this role?
Trwale usunąć tę rolę?
Удалить эту роль безвозвратно?
Бұл рөлді біржола жою керек пе?

admin.assignees
Assignees
Przypisane osoby
Назначенные пользователи
Тағайындалған пайдаланушылар

admin.assignees_choose
Accounts assigned to this item
Konta przypisane do tego materiału
Учётные записи, назначенные на этот материал
Осы материалға тағайындалған тіркелгілер

admin.assignees_confirm
Save the complete assignment set?
Zapisać cały zestaw przypisań?
Сохранить полный список назначений?
Тағайындаулардың толық тізімін сақтау керек пе?

admin.assignees_save
Review assignments
Sprawdź przypisania
Проверить назначения
Тағайындауларды тексеру

admin.account_no_sessions
No active sessions.
Brak aktywnych sesji.
Активных сессий нет.
Белсенді сеанстар жоқ.

admin.roles_allow_delegable_content
Allow issuing delegable content grants
Zezwól na nadawanie uprawnień do treści z prawem dalszego delegowania
Разрешить выдачу делегируемых разрешений на контент
Тапсыруға болатын контент рұқсаттарын беруге рұқсат беру

admin.roles_delegation_scope
Delegation scope
Zakres delegowania
Область делегирования
Тапсыру ауқымы

admin.roles_scope_assigned
Assigned items only
Tylko przypisane materiały
Только назначенные материалы
Тек тағайындалған материалдар

admin.roles_scope_any
Any item
Wszystkie materiały
Любые материалы
Кез келген материал

admin.draft_untitled
Untitled
Bez tytułu
Без названия
Атаусыз

admin.draft_status_published
Published:
Opublikowano:
Опубликовано:
Жарияланған:

admin.draft_status_unpublished
Unpublished
Brak publikacji
Не опубликовано
Жарияланбаған

admin.content_field_title
Title
Tytuł
Заголовок
Тақырып

admin.content_field_slug
URL slug
Adres URL
Адрес страницы
Бет мекенжайы

admin.content_field_body
Body
Treść
Текст
Мәтін

admin.content_field_slug_help
The page address is generated automatically from the title.
Adres strony powstaje automatycznie z tytułu.
Адрес страницы создаётся автоматически из заголовка.
Бет мекенжайы тақырыптан автоматты түрде жасалады.

admin.leave_unsaved
Leave this section without saving your changes?
Opuścić sekcję bez zapisania zmian?
Покинуть раздел без сохранения изменений?
Өзгерістерді сақтамай бөлімнен шығу керек пе?

admin.stay_editing
Keep editing
Wróć do edycji
Продолжить редактирование
Өңдеуді жалғастыру

admin.discard_changes
Discard changes
Odrzuć zmiany
Отменить изменения
Өзгерістерден бас тарту

admin.settings_identity
Portal details
Informacje o portalu
О портале
Портал туралы

admin.settings_appearance
Appearance
Wygląd
Оформление
Безендіру

admin.settings_languages
Languages
Języki
Языки
Тілдер

admin.settings_search_messages
Search texts
Szukaj tekstów
Поиск текстов
Мәтіндерді іздеу

admin.settings_message_group
Section
Sekcja
Раздел
Бөлім

admin.settings_select_message
Text to edit
Tekst do edycji
Текст для редактирования
Өңделетін мәтін

admin.settings_message_value
Translation
Tłumaczenie
Перевод
Аударма

admin.settings_no_messages
No matching texts.
Nie znaleziono pasujących tekstów.
Подходящих текстов нет.
Сәйкес мәтіндер табылмады.

admin.slug_rules_title
URL replacements
Zamiany znaków w adresie
Замены для адресов страниц
Бет мекенжайларындағы алмастырулар

admin.slug_rules_language
Rule language
Język reguł
Язык правил
Ережелер тілі

admin.slug_rules_search
Search rules
Szukaj reguł
Поиск правил
Ережелерді іздеу

admin.slug_rules_character
Character
Znak
Символ
Таңба

admin.slug_rules_replacement
Replacement
Zamiana
Замена
Алмастыру

admin.slug_rules_add
Add rule
Dodaj regułę
Добавить правило
Ереже қосу

admin.slug_rules_remove
Remove rule
Usuń regułę
Удалить правило
Ережені жою

admin.slug_rules_help
Map one non-ASCII character to lowercase Latin letters, digits or hyphens. An empty replacement removes the character. Changes apply to new addresses after publication.
Zastąp pojedynczy znak spoza ASCII małymi literami łacińskimi, cyframi lub łącznikami. Pusta zamiana pomija znak. Zmiany obowiązują dla nowych adresów po publikacji.
Укажите замену для одного символа вне ASCII. В замене допустимы строчные латинские буквы, цифры и дефисы. Пустая замена пропускает символ. Правила применяются к новым адресам после публикации.
ASCII-ден тыс бір таңбаны кіші латын әріптерімен, цифрлармен не дефистермен алмастырыңыз. Бос алмастыру таңбаны өткізіп жібереді. Өзгерістер жарияланғаннан кейін жаңа мекенжайларға қолданылады.

admin.slug_rules_invalid
Check the character and replacement. Characters must be unique within each language.
Sprawdź znak i zamianę. W każdym języku znaki muszą być unikatowe.
Проверьте символ и замену. В каждом языке символ должен встречаться только один раз.
Таңба мен алмастыруды тексеріңіз. Әр тілде таңба бір рет қана кездесуі керек.

admin.settings_all_texts
All texts
Wszystkie teksty
Все тексты
Барлық мәтіндер

admin.settings_admin_texts
Sign-in and common controls
Logowanie i wspólne elementy
Вход и общие элементы
Кіру және ортақ элементтер

admin.settings_content_texts
Content editing
Edycja materiałów
Редактирование материалов
Материалдарды өңдеу

admin.settings_structure_texts
Collection structure
Struktura kolekcji
Структура коллекций
Жинақ құрылымы

admin.settings_access_texts
Users and access
Użytkownicy i dostęp
Пользователи и доступ
Пайдаланушылар және қолжетімділік

admin.settings_settings_texts
Portal configuration
Konfiguracja portalu
Конфигурация портала
Портал конфигурациясы

admin.security
Security
Bezpieczeństwo
Безопасность
Қауіпсіздік

admin.session_policy_heading
Session duration
Czas trwania sesji
Длительность сеансов
Сеанс ұзақтығы

admin.session_policy_idle_minutes
Inactivity limit (minutes)
Limit bezczynności (minuty)
Время бездействия (минуты)
Әрекетсіздік шегі (минут)

admin.session_policy_absolute_hours
Maximum session length (hours)
Maksymalny czas sesji (godziny)
Максимальная длительность сеанса (часы)
Сеанстың ең ұзақ мерзімі (сағат)

admin.session_policy_help
The inactivity limit signs users out after a period without activity. The maximum length applies even while they are active. Changes apply at the next sign-in.
Limit bezczynności kończy sesję po okresie bez aktywności. Maksymalny czas sesji obowiązuje także podczas pracy. Zmiany zaczną obowiązywać po następnym zalogowaniu.
Время бездействия отсчитывается от последнего действия. Максимальная длительность учитывается и при активной работе. Изменения вступают в силу при следующем входе.
Әрекетсіздік уақыты соңғы әрекеттен бастап есептеледі. Ең ұзақ мерзім белсенді жұмыс кезінде де қолданылады. Өзгерістер келесі кірген кезде күшіне енеді.

admin.session_policy_save
Save session policy
Zapisz zasady sesji
Сохранить политику сеансов
Сеанс саясатын сақтау

admin.session_policy_saved
Session policy saved.
Zasady sesji zapisane.
Политика сеансов сохранена.
Сеанс саясаты сақталды.

admin.settings_public_texts
Public portal
Portal publiczny
Публичный портал
Ашық портал

admin.settings_missing_only
Only untranslated texts
Tylko brakujące tłumaczenia
Только строки без перевода
Тек аударылмаған мәтіндер

admin.settings_confirm_remove_catalog
Remove this language and its translations from the draft?
Usunąć ten język i jego tłumaczenia ze szkicu?
Удалить этот язык и его переводы из черновика?
Бұл тілді және оның аудармаларын нобайдан алып тастау керек пе?

admin.settings_complete_catalog
Fill in the missing texts or remove this language.
Przed publikacją uzupełnij brakujące tłumaczenia lub usuń ten język.
Заполните пропуски или удалите этот язык перед публикацией.
Бос аудармаларды толтырыңыз немесе тілді алып тастаңыз.

admin.workspace_structure
Structure
Struktura
Структура
Құрылым

admin.settings_translation_language
Language to edit
Język do edycji
Язык перевода
Аударма тілі

admin.settings_reference_text
Reference text
Tekst odniesienia
Исходный текст
Бастапқы мәтін

admin.settings_back_to_texts
Back to texts
Wróć do tekstów
К списку текстов
Мәтіндер тізіміне оралу

admin.settings_choose_accent
Choose accent color
Wybierz kolor akcentu
Выбрать цвет акцента
Акцент түсін таңдау

collection
Collection
Kolekcja
Коллекция
Жинақ

admin.registry_template
Collection template
Szablon kolekcji
Шаблон коллекции
Жинақ үлгісі

admin.registry_blank_template
General collection
Kolekcja ogólna
Универсальная коллекция
Жалпы жинақ

admin.registry_collection_name
Collection name
Nazwa kolekcji
Название коллекции
Жинақ атауы

admin.registry_disabled_collections
Disabled collections
Wyłączone kolekcje
Отключённые коллекции
Өшірілген жинақтар

admin.registry_disable
Disable collection
Wyłącz kolekcję
Отключить коллекцию
Жинақты өшіру

admin.registry_enable
Enable collection
Włącz kolekcję
Включить коллекцию
Жинақты қосу

admin.registry_disabled_notice
Collection disabled.
Kolekcja została wyłączona.
Коллекция отключена.
Жинақ өшірілді.

admin.registry_enabled_notice
Collection enabled.
Kolekcja została włączona.
Коллекция включена.
Жинақ қосылды.

admin.registry_disable_effect
Editing will be paused and the collection will leave the menus. Published pages and drafts will be kept.
Edycja zostanie wstrzymana, a kolekcja zniknie z menu. Opublikowane strony i szkice zostaną zachowane.
Редактирование приостановится, коллекция исчезнет из меню. Опубликованные страницы и черновики сохранятся.
Өңдеу тоқтатылады және жинақ мәзірден алынады. Жарияланған беттер мен нобайлар сақталады.

admin.registry_enable_effect
The collection will return to the menus for people who have access.
Kolekcja wróci do menu osób, które mają do niej dostęp.
Коллекция вернётся в меню пользователей с правом доступа.
Жинақ қол жеткізу құқығы бар пайдаланушылардың мәзіріне қайтарылады.

admin.sign_in_retry
Enter your password again to continue signing in.
Wpisz ponownie hasło, aby kontynuować logowanie.
Введите пароль ещё раз, чтобы продолжить вход.
Кіруді жалғастыру үшін құпиясөзді қайта енгізіңіз.

admin.language_flags
Language flags
Flagi języków
Флаги языков
Тіл жалаулары

admin.language_flag_saved
Saved flag
Zapisana flaga
Сохранённый флаг
Сақталған жалау

admin.language_flag_svg
Flag SVG
Kod SVG flagi
SVG флага
Жалаудың SVG коды

admin.language_flag_help
Paste an SVG with simple shapes, up to 4 KB. Leave empty to use the globe.
Wklej SVG z prostymi kształtami, do 4 KB. Puste pole wyświetla globus.
Вставьте SVG с простыми фигурами до 4 КБ. Пустое поле отображает глобус.
Қарапайым пішіндері бар 4 КБ дейінгі SVG енгізіңіз. Бос өріс глобусты көрсетеді.

admin.language_flag_reset
Use globe
Użyj globusa
Использовать глобус
Глобусты пайдалану

admin.language_flag_invalid
This flag could not be saved. Check the SVG and try again.
Nie można zapisać flagi. Sprawdź SVG i spróbuj ponownie.
Не удалось сохранить флаг. Проверьте SVG и повторите попытку.
Жалау сақталмады. SVG кодын тексеріп, қайта көріңіз.

admin.navigation_labels
Section names
Nazwy sekcji
Названия разделов
Бөлім атаулары

admin.registry_icon
Collection icon (SVG)
Ikona kolekcji (SVG)
Значок коллекции (SVG)
Жинақ белгішесі (SVG)

admin.registry_icon_help
Paste an SVG with a viewBox and simple shapes. Leave the field empty to use the default icon.
Wklej SVG z atrybutem viewBox i prostymi kształtami. Puste pole przywraca ikonę domyślną.
Вставьте SVG с атрибутом viewBox и простыми фигурами. Пустое поле возвращает стандартный значок.
viewBox атрибуты және қарапайым пішіндері бар SVG енгізіңіз. Бос өріс әдепкі белгішені қайтарады.

admin.registry_icon_preview
Menu preview
Podgląd w menu
Предпросмотр в меню
Мәзірде алдын ала қарау

admin.registry_icon_pending
Preview updates after saving.
Podgląd zmieni się po zapisaniu.
Предпросмотр обновится после сохранения.
Алдын ала көрініс сақтағаннан кейін жаңарады.

admin.registry_icon_default
Use default icon
Użyj ikony domyślnej
Использовать стандартный значок
Әдепкі белгішені қолдану

admin.registry_icon_invalid
Use a valid SVG with a viewBox, simple shapes, and up to 4 KB of code.
Użyj poprawnego SVG z atrybutem viewBox, prostymi kształtami i kodem do 4 KB.
Используйте корректный SVG с viewBox, простыми фигурами и объёмом кода до 4 КБ.
viewBox атрибуты, қарапайым пішіндері және 4 КБ-қа дейінгі коды бар дұрыс SVG пайдаланыңыз.
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
