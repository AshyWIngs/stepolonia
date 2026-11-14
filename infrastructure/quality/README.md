# Quality pipeline

`make lint` проверяет исходники без БД. `make duplicates` запускает только CPD.
`make SITE=example.test verify` выполняет `lint`, `config`, `test`, `build`,
`db-test`; `duplicates` входит через `lint` один раз. PostgreSQL создаётся только
собственным disposable harness, постоянные volumes не очищаются.

Нужны Docker/BuildKit, Make и Python 3. Для `verify` также нужны Go 1.27.3+
той же minor-ветки и C compiler для race detector. CI берёт Go из `go.mod`.
Логи находятся в игнорируемом `reports/`. Передавайте `SITE` явно, если локально
выбран другой сайт.

| Инструмент | Версия и блокирующие правила |
|---|---|
| golangci-lint | 2.15.0, Go 1.27.3: staticcheck, errcheck, gosec, gocognit, gocyclo, unused |
| Ruff | 0.17.5: E4/E7/E9/F/B, только scripts |
| Nuxt ESLint / ESLint | 1.17.0 / 10.14.0: штатный Vue/TypeScript flat config, warnings=0 |
| jscpd | 5.4.3, Node 26.13.0/npm 11.21.2, Go/Python/TypeScript/Vue |

Go cognitive/cyclomatic и ESLint cyclomatic: максимум 15 для нового кода.
Существующие три функции Go имеют ограниченные бюджеты:

| Функция | Cognitive / cyclomatic |
|---|---|
| cmd/db.run | 30 / 27 |
| ReadLanguages | 25 / 22 |
| checkDefinitions | 20 / 18 |

Рост выше этих значений блокируется. Пересматривать бюджет при следующей правке
функции; обслуживание schema_contract относится к Issue #2. Интеграционный
SQL-сценарий освобождён только от двух complexity-правил, остальные анализаторы
и CPD продолжают его проверять. Размер функции >80 строк и файла >500 строк
требует ревью, но не механического дробления. Cognitive complexity Python/Vue
и общий рейтинг качества также оценивает независимый SonarCloud.

jscpd блокирует любое совпадение размером не менее 10 строк и 80 токенов:
порог дублирования 0%. Тесты включены. Исключены зависимости, Nuxt output,
генерируемые Go-файлы, lockfiles и отчёты. Результат требует смыслового разбора:
похожие fixtures или SQL не объединяются автоматически.

Baseline на main `652bd2f`: Go выдаёт 25 сообщений без дедупликации по строке:
9 cleanup errcheck, 8 complexity (по два правила на четыре функции),
8 gosec на пяти sinks. Отдельный baseline unused дал 0 замечаний;
он проверяет неиспользуемые package declarations, дополняя compiler/staticcheck. Независимый source-to-sink разбор gosec не подтвердил
уязвимости: operator-owned file/env, quoted SQL identifier и фиксированный psql.
Точные path/rule/source-исключения и условия пересмотра находятся в `.golangci.yml`.
Cleanup исключения относятся только к проверенным defer после обработки основных
ошибок чтения, Commit и rows.Err; глобального Close/Rollback ignore нет.

Ruff: 2 E402 при явной настройке sys.path тестовым harness; исключение только
для этого правила и файла. Nuxt: 0 ошибок/предупреждений. CPD: 0 совпадений,
0%; контрольный запуск с меньшим размером 5 строк/50 токенов также дал 0.
Существующий долг не исправляется массовым рефакторингом в инфраструктурном PR.

`go vet` выполняется в `test`, не дублируется golangci-lint. Race-тесты включают
обычное исполнение: отдельный повтор `go test` удалён. После blocking `go vet`
тот же Docker RUN не повторяет его внутри `go test` (`-vet=off`). Unit/HTTP tests идут
в `test`; `db-test` запускает только PostgreSQL integration scenario.
Nuxt typecheck и production build сохранены. Собственный анализатор, второй
локальный CPD и обязательный allocation/coverage gate не вводятся.

SonarCloud остаётся независимым облачным Gate с прежними порогами. Локальный
PASS не означает cloud PASS. Сетевые dependency-аудиты и benchmarks рекомендуются
по отдельной задаче: они не смешиваются с детерминированным lint. npm audit
показал существующие 7 low в цепочке deepmerge/Rollup/Nitro; обновление runtime
зависимостей в этот инфраструктурный PR не входит.
