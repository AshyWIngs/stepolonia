# Stepolonia

Stepolonia is a Go and Nuxt portal platform. Installations share code and have separate PostgreSQL databases, settings, and content.

The project is in pre-release development. Public content and site settings are available through a read-only API and Nuxt SSR. Private administration provides sign-in and access-controlled editing over HTTPS with explicit configuration. The portal title and interface messages come from the site publication.

## Repository layout

| Path | Contents |
|---|---|
| `apps/api/` | Go API and database tools |
| `apps/web/` | Nuxt frontend |
| `packages/sdk/` | OpenAPI, generated TypeScript types, and Fetch client |
| `infrastructure/` | Docker Compose, Caddy, and PostgreSQL |
| `scripts/` | Installation and verification tools |
| `sites/example.test/` | Example installation configuration |

## Local development

Run `make swagger` from the repository root to serve the public API and Swagger UI at [http://127.0.0.1:8080/docs](http://127.0.0.1:8080/docs). The OpenAPI document is at `/openapi.json`. The launcher requires Docker, Go from `apps/api/go.mod`, and Python from `.python-version`. It creates a loopback-only disposable database, loads synthetic data, and removes the database on Ctrl-C. To select a different port, run `python3 scripts/dev-swagger.py --port 8081`.

The Swagger demo includes published Article and Event items in Polish (`pl`). Use `welcome-to-stepolonia` and `community-meetup` for detail reads, `limit=1` for pagination, and `welcome-old` for an HTTP 301 redirect. `/api/v1/site?lang=kk` selects the published Kazakh UI catalog. The site catalog uses `lang`. Content endpoints use `language`. The API connects with a restricted runtime login, and the fixture writer uses an owner connection before the API starts. Private administration routes are disabled in this demo.

### Full installation

Install Docker with Compose and BuildKit, Make, and the CPython version in `.python-version`. The initial build needs network access. Go and Node.js are installed in the builder containers. If you use pyenv, run `pyenv install -s "$(cat .python-version)"` to install the pinned Python version.

```sh
make python-version-check
make up
```

The default installation is `example.test`, served on [http://127.0.0.1:8080](http://127.0.0.1:8080). A new installation returns 503 until the operator applies the schema and publishes the first site settings revision. Initialize the selected installation with its own title and initial language.

```sh
make SITE=example.test migrate
python3 scripts/db_tools.py --site example.test initialize-site "Polacy w Kazachstanie" pl
```

The command publishes the initial PostgreSQL interface catalogs for `en`, `kk`, `pl` and `ru` once. Later edits are available in Administration at `/admin` for an account with `settings.manage`. Saving a draft preserves the public revision until publication. Run `make SITE=example.test smoke` after the initial publication. The example installation serves local HTTP.

Administration requires the configured HTTPS origin. For a different installation:

```sh
python3 scripts/setup-local.py --site another.test --port 8081
make SITE=another.test up
```

Local configuration is stored in `sites/<SITE>/.env`, and passwords in `sites/<SITE>/.secrets/`. Git ignores these files. Setup preserves existing values and verifies ownership, file type, and the absence of links before changing secret permissions through open file descriptors. An existing database's credentials and volume require their own change procedure.

Administration requires a canonical HTTPS origin. Set `ADMIN_AUTH_ENABLED=true`, `ADMIN_AUTH_ORIGIN`, `ADMIN_DB_USER`, `ADMIN_DB_PASSWORD_FILE`, `ADMIN_TOTP_KEY_FILE`, and `ADMIN_TOTP_KEY_VERSION` in the API process. Configure `ADMIN_AUTH_PROXY_CIDRS` only for trusted proxies. Portal settings and content edits use a distinct verified login through `CONTENT_EDITOR_DB_USER` and `CONTENT_EDITOR_DB_PASSWORD_FILE`.

To set a local default installation, use the ignored `.site.mk`:

```make
SITE ?= another.test
```

### Public content

`/{lang}/articles` lists published articles, and `/{lang}/articles/{slug}` opens a published article. The homepage chooses the language from the visitor's saved choice, `Accept-Language`, or the published content default, in that order. A localized URL selects content directly. Article language switches link to published translations.

`GET /api/v1/site` returns settings and UI text from the same published PostgreSQL revision. If the requested catalog is missing, the API uses the installation's published fallback. Missing publication or invalid language configuration returns 503. Compose sets `NUXT_API_ORIGIN` for SSR. Set it explicitly when starting Nuxt separately. Browser requests use the current site's origin. Set `NUXT_PUBLIC_SITE_ORIGIN` to a trusted public HTTPS origin to enable canonical links. Responses carry `noindex, nofollow`. The published revision controls settings and UI text without a frontend rebuild.

Markdown renders through the allowed Vue nodes. Images currently render as alternative text.

The PostgreSQL, API, Nuxt, and Caddy runtime images use non-root users and omit a shell. Caddy publishes the only host port in the standard Compose installation.

## Database and languages

Run migrations separately from the API and pass the target `SITE` explicitly. Use a new installation or one approved for database maintenance.

```sh
python3 scripts/setup-local.py --site sandbox.test
python3 scripts/site_config.py --site sandbox.test up -d --wait postgres
make SITE=sandbox.test migrate
make SITE=sandbox.test migrate ACTION=status
```

The one-shot `db-tools` container uses the target database's owner secret. The runtime login can read approved public views. It has no DDL access. The domain Goose migrations define the PostgreSQL 18 schema. Before schema creation and on repeated `up`, the tooling rejects inconsistent runtime privileges and schema drift, including public-view filters, constraints, and guard function bodies. Inspect `ACTION=status`, fix the cause, and retry `migrate up` after a failure. Before MVP, edit the domain migration that owns the definition.

`ACTION=down` removes the application schema and its data. It requires `DISPOSABLE=1` and the test harness's ownership marker. The project, database, volume, `stage12_` database prefix, and database metadata token must agree. Recover persistent data from a separately managed backup.

Migrations install `en`, `ru`, `pl` and `kk` as disabled language entries. Import preserves the enabled state of existing languages. Initialization enables the starter catalogs together with the first publication.

The importer accepts UTF-8 CSV up to 8 MiB with this exact header:

```csv
code,name,native_name,direction
pl,Polish,Polski,ltr
```

```sh
make SITE=sandbox.test import-languages FILE=/absolute/path/languages.csv
```

Normalized BCP 47 codes are stored in lowercase and have a length of 2–63 bytes. Invalid files are rejected atomically. Repeated import updates metadata while preserving availability and translations.

The bootstrap container initializes the Alpine/musl PostgreSQL 18.7 cluster. The runtime uses UID/GID 999 and `/var/lib/postgresql/18/docker`. Validate locale and collation and use dump/restore before moving a cluster from Debian/glibc. The image includes PL/pgSQL, ICU, XML, LDAP, LLVM JIT and contrib, and uses SCRAM-SHA-256 for authentication. Kerberos/GSS, PL/Perl, PL/Python and PL/Tcl are excluded.

## Checks

| Command | Checks |
|---|---|
| `make setup` / `make config` | Local configuration / Compose validation |
| `make up` / `make down` | Start / stop the installation and preserve its volume |
| `make build` | Build images |
| `make lint` / `make duplicates` | Static checks / duplication scan |
| `make sdk` / `make sdk-check` | Regenerate / verify OpenAPI and TypeScript |
| `make test` | Source, Dockerfile, and unit checks |
| `make db-test` | Disposable PostgreSQL schema, roles, and CLI |
| `make publishing-test` | Live publishing, API, SSR, and Chromium |
| `make auth-test` | PostgreSQL identity, HTTPS, and browser security |
| `make smoke` / `make lifecycle` | Live installation / controlled stop and recovery |
| `make isolation` | Separate installation and PostgreSQL dump/restore |
| `make resource-test` | Single-installation resource measurements |
| `make image-audit` | Scan built images with Grype |
| `make verify` | Full local verification |

Pass `SITE=...` to select an installation. `make lifecycle` temporarily stops the selected stack. `make isolation` cleans up the resources it creates. Docker Compose `down --volumes` deletes managed volumes and their data.

The database tests require host Go and a C compiler for race detection. They create their own installation, expose PostgreSQL on a random loopback port, and clean up their resources afterward. Their passwords are read from files. SDK generation and checks require Go from `apps/api/go.mod`, Node.js 26.13.1, and npm 11.21.2 on the host.

`make resource-test` requires Docker with CPU 0, Go, Node.js and Chromium. Run `npm ci --ignore-scripts` and `node node_modules/playwright/cli.js install chromium` in `apps/web` first. The harness exercises a synthetic installation with keep-alive connections, a shared CPU, and a combined 1216 MiB container memory limit without swap. To set the duration and concurrency, run `python3 scripts/test-resources.py --seconds 60 --concurrency 4`. Build and load generation run outside the measured containers. The `docker stats` samples describe container usage at sampling times.

Install SQLFluff 4.5.0 into a separate environment before running `make lint-sql` or the local lint and Verify targets.

```sh
make python-version-check
python3 -m venv /tmp/stepolonia-sqlfluff
. /tmp/stepolonia-sqlfluff/bin/activate
python3 -m pip install -r infrastructure/quality/sql-requirements.txt
make lint-sql
```

The [PR CI Watch workflow](.github/workflows/ci-watch.yml) watches the configured checks on its GitHub schedule and reports failures and long waits. [PR Merge Notice](.github/workflows/ci-merge-notice.yml) posts the result of a squash merge. If GitHub denies comment access, the workflow writes the result to its job summary. Both workflows run on GitHub Actions without external model credentials.

Logs go to the ignored `reports/` directory. See the [quality pipeline](infrastructure/quality/README.md) for tool versions, scan scope, and CI behavior. See the [SDK guide](packages/sdk/README.md) for code generation and client behavior.
