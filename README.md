# Stepolonia

Платформа порталов на Go и Nuxt. Общий код, отдельные установки сайтов со
своими PostgreSQL, настройками и данными.

Сейчас реализованы инфраструктура, healthchecks и стартовая SSR-страница.
Публикация материалов, авторизация и редактор пока не реализованы.

## Структура

```text
apps/api/                 Go API
apps/web/                 Nuxt frontend
infrastructure/           Docker Compose, Caddy, PostgreSQL
scripts/                  настройка и проверки установок
sites/example.test/       пример конфигурации сайта
```

## Локальный запуск

Нужны Docker с Compose/BuildKit, Python 3 и Make. Go и Node.js на хосте
не требуются. Первые сборки требуют доступа к интернету.

```sh
make up
```

Адрес: <http://127.0.0.1:8080>. По умолчанию используется `example.test`.
Это локальная HTTP-конфигурация, публичный deployment требует настройки HTTPS.

Другой сайт:

```sh
python3 scripts/setup-local.py --site another.test --port 8081 --name "Мой портал"
make SITE=another.test up
```

Настройки создаются в `sites/<SITE>/.env`, пароли в `sites/<SITE>/.secrets/`.
Повторная настройка сохраняет существующие значения. Эти файлы исключены из Git.
Не меняйте credentials и имя volume существующей БД без отдельной процедуры.

Для локального сайта по умолчанию можно создать игнорируемый файл `.site.mk`:

```make
SITE ?= another.test
```

## Команды

| Команда | Назначение |
|---|---|
| `make setup` | Создать локальные настройки |
| `make config` | Проверить конфигурацию Compose |
| `make test` | Проверить Dockerfile, Go и TypeScript |
| `make build` | Собрать образы |
| `make up` | Собрать и запустить установку |
| `make down` | Удалить контейнеры и сети, сохранить данные |
| `make smoke` | Проверить запущенную установку |
| `make lifecycle` | Проверить остановку и восстановление локального стека |
| `make isolation` | Проверить отдельную временную установку и PostgreSQL dump/restore |

Все команды принимают `SITE=...`. `lifecycle` временно останавливает выбранный
стек; `isolation` удаляет только созданные им тестовые ресурсы. Логи проверок
попадают в игнорируемый каталог `reports/`. `down --volumes` удаляет управляемые
volumes с данными.

PostgreSQL работает в distroless; подготовка кластера выполняется одноразовым
контейнером `postgres-init`. API, frontend и PostgreSQL не публикуют порты хоста;
HTTP обслуживает Caddy. Команды `migrate`, `seed` и `sdk` пока не реализованы.
