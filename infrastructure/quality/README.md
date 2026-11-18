# Quality pipeline

`make lint` checks the source without a database. `make duplicates` runs only
duplicate detection. `make SITE=example.test verify` runs lint, configuration
validation, tests, builds, the database, publishing and authentication suites,
and the image audit. Make runs duplicate detection once through lint. The
database harness creates its own disposable PostgreSQL resources.

| Change | Local check |
|---|---|
| Go | `make check-go` (lint, vet and race-enabled unit tests) |
| Python | `make check-python` (Ruff and safety tests) |
| Nuxt, Vue or TypeScript | `make check-web` (ESLint and type checking) |
| OpenAPI or SDK | `make sdk-check` (generated files, ESLint and type checking) |
| PostgreSQL or publishing SQL | `make check-db` (disposable PostgreSQL) |
| Publishing through Compose | `make publishing-test` (SDK, Caddy, API, fifth-language import, 503 and database recovery) |
| Markdown | Review links and the diff |

Run the checks for all affected areas. Rebuild and audit affected images when
changing images or dependencies. Run full Verify for executable changes at the
final revision.

The publishing suite sends HTTP fixtures through Caddy. The database suite
covers migrations, transactions and concurrency. Both use `sdk-check` and Make
runs it once. The disposable database publishes a random loopback port for
Go fixtures. The test data network permits that host connection.

GitHub Actions runs the `verify` job on ready-for-review PRs and their subsequent
revisions. Draft and Markdown-only PRs skip the full check. A successful
skipped job only indicates the skip. The complete workflow also runs through
`workflow_dispatch`. SonarCloud and Codacy report their own results. Verify is
advisory for merging under the current ruleset.

## Build caches

`build-dependencies` uses BuildKit and GitHub Actions to cache the prerequisite
targets in `dependencies.hcl` for Go tools and modules, npm dependencies and the
PostgreSQL distribution. Each target has a separate cache scope. `mode=max`
includes intermediate layers. Source is copied after the dependency stages,
so lint, test, and build run on the PR revision. The image audit refreshes its
vulnerability database separately.

`cache-ready` checks the dependency files and requires access to the snapshot
when the remote cache is available. The `no-cache-filter` applies only to this
readiness stage. Confirm reuse in the `CACHED` messages from subsequent builds.
A cache export warning and a failed build are reported separately.

Bake, Make and Compose share one builder and load the resulting images into the
daemon. Dependency preparation runs at most two jobs in parallel. After build
dependency changes reach `main`, a separate workflow warms the default-branch
cache. The npm, pip and Chromium caches use the corresponding lockfiles.
Playwright still checks the pinned browser and installs system dependencies.
Measure gains across subsequent runs.

## Toolchain and lint limits

The checks require Docker with BuildKit, Make and Python from
[`.python-version`](../../.python-version). Verify also requires Go 1.27.4 or a
later 1.27 patch and a C compiler for the race detector. SDK checks use Node.js
26.13.1 and npm 11.21.2. CI reads the Go version from
[`apps/api/go.mod`](../../apps/api/go.mod). Reports are written to the ignored
`reports/` directory. Pass `SITE` if your selected site differs from the example.

| Tool | Version and rules |
|---|---|
| golangci-lint | 2.15.0 with Go 1.27.4. Checks include staticcheck, errcheck, gosec, gocognit, gocyclo, unused, funlen, goconst and revive/argument-limit. |
| Ruff | 0.17.5. E4, E7, E9, F, B, C901 and PLR0915. |
| Nuxt ESLint / ESLint | 1.17.0 / 10.14.0. Vue and TypeScript flat config with zero warnings. |
| SDK | openapi-typescript 7.13.0, openapi-fetch 0.17.0 and TypeScript 5.9.3. ESLint uses the same complexity limits. |
| jscpd | 5.4.3 with Node.js 26.13.1/npm 11.21.2. Scans Go, Python, JavaScript, TypeScript and Vue. |

Go and ESLint limit cognitive and cyclomatic complexity to 15 per function,
including tests. Go funlen and ESLint limit functions to 80 lines and 50
statements. Comments are excluded from line limits and ESLint also skips blank
lines. Go revive/argument-limit allows seven parameters per function, including
tests (`run.tests: true`). Go lint covers the API and both health-check modules.

Ruff covers `scripts/`, including tests, and limits Python cyclomatic complexity
to 15 and statements per function to 50. Sonar checks Python cognitive
complexity independently. ESLint runs separately in the frontend and SDK, and
covers JavaScript fixtures and SDK type tests. TypeScript and Vue use the
cognitive complexity rule from eslint-plugin-sonarjs 4.2.2.

Go goconst checks strings of at least three characters that occur at least three
times within one package, including calls and tests. Struct tags are excluded.
Review flagged values for common meaning and ownership before introducing
constants. Keep test expectations independent from their production
implementation.

jscpd fails on duplicates of at least ten lines and 80 tokens. It includes
tests, JavaScript fixtures and handwritten SDK files. Dependencies, Nuxt build
output, generated Go and SDK types, lockfiles and reports are excluded.
Review fixtures and SQL for shared behavior before combining them.

[`.golangci.yml`](../../.golangci.yml) records the exact path, rule and source
for individual gosec exceptions. They cover operator-owned files and
environment values, quoted SQL identifiers and the fixed psql invocation.
Deferred cleanup exceptions apply where code already handles the relevant
read, commit or row errors.

`make test` runs `go vet` before race-enabled Go tests and uses `-vet=off` to
avoid repeating vet. Unit and HTTP tests run there. PostgreSQL integration
tests run through `db-test`. Nuxt type checking and the production build are
part of Verify. Benchmarks and networked npm audits run separately. Consult
the current reports for advisory counts and dependency chains.

## Image audit

`make image-audit` runs once in Verify after lint and build. Grype 0.121.3 scans
the actual eleven images. High and critical findings fail the audit, including
findings without a fix. Scanner or update errors also fail the audit. Every run
checks for CVE database updates and rejects a database older than 72 hours.

The scanner receives Docker archives for the Go, Python, web, CPD and npm build
images, the API, web, PostgreSQL, and Caddy runtime images, and the PostgreSQL
initializer and database tools. The scanner receives no Docker socket or
secrets. `reports/images/` contains image IDs, platforms, findings in JSON, and
the CVE database version and age. The archives are removed after scanning.
`sp-grype-cache` stores the public vulnerability database.

The API, web, PostgreSQL, Caddy and database tools images and the CPD runtime
use non-root users and omit a shell. The web and CPD build and runtime stages
use Alpine 3.24 with Node.js 26.13.1. The DHI builder includes npm 11.21.2 and
checks that version during the build. Caddy 2.11.9 uses the patched DHI build.
A Go probe checks its HTTP listener and runs through the regular Go checks.

The golangci-lint image recompiles the pinned v2.15.0 upstream commit with Go
1.27.4 to include standard library CVE fixes absent from the upstream binary.
Go and npm build tools live in separate stages. The audit covers the listed
images. Host compilers and other intermediate stages are outside that scope.
The scanner completes all images before reporting failure and retains findings
at all severity levels.

Review the affected components, upstream fixes and backports when triaging
findings. Verify how the affected code is used before assigning application
impact. Preserve the audit threshold while a finding is under review.
