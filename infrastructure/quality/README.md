# Quality pipeline

`make lint` проверяет исходники без БД. `make duplicates` запускает только CPD.
`make SITE=example.test verify` выполняет `lint`, `config`, `test`, `build`,
`db-test`, `image-audit`; `duplicates` входит через `lint` один раз. PostgreSQL создаётся только
собственным disposable harness, постоянные volumes не очищаются.

Нужны Docker/BuildKit, Make и Python 3. Для `verify` также нужны Go 1.27.4+
той же minor-ветки и C compiler для race detector. CI берёт Go из `go.mod`.
Логи находятся в игнорируемом `reports/`. Передавайте `SITE` явно, если локально
выбран другой сайт.

| Инструмент | Версия и блокирующие правила |
|---|---|
| golangci-lint | 2.15.0, Go 1.27.4: staticcheck, errcheck, gosec, gocognit, gocyclo, unused, funlen, goconst |
| Ruff | 0.17.5: E4/E7/E9/F/B/C901/PLR0915, только scripts |
| Nuxt ESLint / ESLint | 1.17.0 / 10.14.0: штатный Vue/TypeScript flat config, warnings=0 |
| jscpd | 5.4.3, Node 26.13.1/npm 11.21.2, Go/Python/TypeScript/Vue |

Go cognitive/cyclomatic и ESLint cognitive/cyclomatic: максимум 15 для всех
функций, включая тесты. Исключений по сложности и повышенных baseline-бюджетов
нет. Go funlen и ESLint ограничивают функцию 80 строками и 50 statements;
комментарии не учитываются, ESLint также пропускает пустые строки.
Ruff C901 ограничивает cyclomatic complexity Python значением 15, PLR0915
ограничивает функцию 50 statements. Это не cognitive metric Python: её отдельно
проверяет SonarQube IDE/Cloud. Go goconst проверяет повтор литералов длиной
от трёх символов при трёх и более вхождениях, включая тесты. Штатное
исключение goconst для аргументов вызовов сохраняется: независимые SQL fixtures
и сообщения ошибок не объединяются механически. Это не полный аналог S1192.
Для TypeScript/Vue подключён только cognitive-complexity из
eslint-plugin-sonarjs 4.2.2; остальные его правила не дублируют Nuxt ESLint.
Лимиты не требуют объединять разные сценарии или создавать абстракции ради
чисел: превышение блокирует проверку и требует осмысленного изменения.

jscpd блокирует любое совпадение размером не менее 10 строк и 80 токенов:
порог дублирования 0%. Тесты включены. Исключены зависимости, Nuxt output,
генерируемые Go-файлы, lockfiles и отчёты. Результат требует смыслового разбора:
похожие fixtures или SQL не объединяются автоматически.

Точечные gosec-исключения относятся к operator-owned file/env, quoted SQL
identifier и фиксированному psql. Точные
path/rule/source-исключения и условия пересмотра находятся в `.golangci.yml`.
Cleanup исключения относятся только к проверенным defer после обработки основных
ошибок чтения, Commit и rows.Err; глобального Close/Rollback ignore нет.

`go vet` выполняется в `test`, не дублируется golangci-lint. Race-тесты включают
обычное исполнение: отдельный повтор `go test` удалён. После blocking `go vet`
тот же Docker RUN не повторяет его внутри `go test` (`-vet=off`). Unit/HTTP tests идут
в `test`; `db-test` запускает только PostgreSQL integration scenario.
Nuxt typecheck и production build сохранены. Собственный анализатор, второй
локальный CPD и обязательный allocation/coverage gate не вводятся.

SonarCloud остаётся независимым облачным Gate с прежними порогами. Локальный
PASS не означает cloud PASS. Benchmarks и сетевой npm audit остаются отдельными командами;
образные CVE проверяет обязательный image-audit. npm audit
показал существующие 7 low в цепочке deepmerge/Rollup/Nitro; обновление runtime
зависимостей в этот инфраструктурный PR не входит.

`make image-audit` входит в `verify` один раз после lint/build и проверяет
реальное содержимое одиннадцати образов через Grype 0.121.3. High/Critical
блокируют результат, в том числе без исправленной версии. Ошибка сети/сканера
тоже блокирует проверку. Проверка обновлений CVE DB обязательна при каждом сканировании; база старше
72 часов отклоняется: это отдельная сетевая
часть verify, её результат зависит от актуальной базы advisories.

Образы Go/Python/web/CPD и npm builder анализаторов, runtime API/web/PostgreSQL,
db-tools, initializer PostgreSQL и runtime Caddy передаются как Docker archives без Docker socket и секретов.
Сохраняются image ID, платформа, JSON findings и версия/возраст базы в
`reports/images/`; архив удаляется при выходе. Фиксированный volume
`sp-grype-cache` содержит только публичную базу сканера.
API, web, PostgreSQL, Caddy, db-tools и исполнение CPD используют non-root runtime
без shell. Web/CPD собираются и работают на одной ветке Alpine 3.24 с Node
26.13.1; DHI builder уже содержит npm 11.21.2, версия проверяется при сборке.
Caddy 2.11.9 берётся из исправленной DHI-сборки; отдельный Go probe проверяет
реальный HTTP listener без wget/shell. Исходники probe проходят штатный Go lint
и fmt/vet/race checks.
golangci-lint 2.15.0 пересобирается `go install` на Go 1.27.4 по точному upstream commit v2.15.0 и его go.mod/go.sum:
готовый upstream binary собран старым Go и содержит исправленные stdlib CVE.
Go compiler и npm install остаются в отдельных builder stages: они требуют
инструментов сборки и проверяются по содержимому, а не по названию дистрибутива.
Host Go/C compiler и другие промежуточные build stages не входят в эти
одиннадцать образов; команда не доказывает отсутствие уязвимостей во всей цепочке
поставки. Сканирование собирает результаты всех образов и возвращает ненулевой
код, если хотя бы один не прошёл. Findings всех severity остаются в JSON.

Блокирующий CVE gate не означает, что каждый пакетный alert доказан как
эксплуатируемая уязвимость приложения. Findings требуют проверки реального
компонента, backports и source-to-sink. Не добавляйте общие исключения для
неисправленных или унаследованных CVE ради зелёного результата.
