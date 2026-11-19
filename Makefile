-include .site.mk
SITE ?= example.test
COMPOSE = python3 scripts/site_config.py --site "$(SITE)"
GRYPE_IMAGE = anchore/grype:v0.121.3

.PHONY: swagger swagger-assets-check setup up down config build docker-check test smoke lifecycle isolation e2e migrate import-languages db-test publishing-test auth-test resource-test sdk sdk-check lint lint-go lint-python lint-web duplicates image-audit verify check-go check-python check-web check-db api-checks web-checks python-tests lint-sql ci-config ci-build ci-service-checks ci-db-test ci-publishing-test ci-image-audit python-version-check

swagger:
	python3 scripts/dev-swagger.py

setup:
	python3 scripts/setup-local.py --site "$(SITE)"

up: setup
	$(COMPOSE) up --build -d --wait --wait-timeout 120

down:
	$(COMPOSE) down

config: setup
	$(MAKE) --no-print-directory ci-config

ci-config:
	$(COMPOSE) config --quiet

build: setup
	$(MAKE) --no-print-directory ci-build

ci-build:
	$(COMPOSE) build

lint: lint-go lint-python lint-web lint-sql duplicates sdk-check

# Use targeted checks during development and run full Verify before merging.
check-go: lint-go api-checks

check-python: lint-python python-tests

check-web: lint-web web-checks

check-db: db-test

# Check application Goose migrations. Empty input and skipped files fail verification.
lint-sql:
	@test "$$(sqlfluff --version)" = "sqlfluff, version 4.5.0"
	@test -n "$(wildcard apps/api/internal/database/migrations/*.sql)"
	sqlfluff lint --config .sqlfluff --disregard-sqlfluffignores --disable-noqa $(wildcard apps/api/internal/database/migrations/*.sql)
	python3 scripts/migration_layout.py

lint-go:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target go-lint -t sp-quality-go:verify . > reports/go-lint.log 2>&1 || { cat reports/go-lint.log; exit 1; }

lint-python:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target python-lint -t sp-quality-python:verify . > reports/python-lint.log 2>&1 || { cat reports/python-lint.log; exit 1; }

lint-web:
	mkdir -p reports
	docker build --progress=plain -f apps/web/Dockerfile --target lint -t sp-quality-web:verify . > reports/web-lint.log 2>&1 || { cat reports/web-lint.log; exit 1; }

duplicates:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target duplicates -t sp-quality-duplicates:verify . > reports/duplicates.log 2>&1 || { cat reports/duplicates.log; exit 1; }

# Each prerequisite runs once, including duplicates through lint.
verify: lint config test build db-test publishing-test auth-test image-audit

auth-test:
	python3 scripts/test-auth.py

resource-test:
	python3 scripts/test-resources.py

# Scan image archives without exposing the Docker socket or secrets.
# An unavailable CVE database fails the check. High and Critical findings fail even without a fix.
image-audit: lint build
	$(MAKE) --no-print-directory ci-image-audit

# Reuse images built on the same runner.
ci-image-audit:
	mkdir -p reports/images
	docker image inspect $(GRYPE_IMAGE) >/dev/null 2>&1 || docker pull $(GRYPE_IMAGE)
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target duplicates-build -t sp-quality-npm:verify . > reports/images/npm-build.log 2>&1 || { cat reports/images/npm-build.log; exit 1; }
	docker build --progress=plain --target dbtools -t sp-db-tools:verify apps/api > reports/images/db-tools-build.log 2>&1 || { cat reports/images/db-tools-build.log; exit 1; }
	@set -eu; trap 'rm -f reports/images/image.tar' EXIT; status=0; \
	for image in sp-quality-go:verify sp-quality-python:verify sp-quality-web:verify sp-quality-duplicates:verify sp-quality-npm:verify sp-api:stage0 sp-web:stage0 sp-postgres:stage0 sp-db-tools:verify sp-postgres-init:stage0 sp-caddy:stage0; do \
	  name=$$(echo "$$image" | tr ':/' '__'); \
	  docker image inspect "$$image" --format '{{.Id}} {{.Os}}/{{.Architecture}}' > "reports/images/$$name.identity"; \
	  docker image save --output reports/images/image.tar "$$image"; \
	  chmod 0644 reports/images/image.tar; \
	  echo "CVE audit: $$image"; \
	  docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges --tmpfs /tmp \
	    --mount type=bind,src="$(CURDIR)/reports/images",dst=/scan,readonly \
	    --mount type=volume,src=sp-grype-cache,dst=/cache/grype \
	    --env GRYPE_DB_CACHE_DIR=/cache/grype \
	    --env GRYPE_DB_REQUIRE_UPDATE_CHECK=true --env GRYPE_DB_MAX_UPDATE_CHECK_FREQUENCY=0s \
	    --env GRYPE_DB_MAX_ALLOWED_BUILT_AGE=72h $(GRYPE_IMAGE) docker-archive:/scan/image.tar --fail-on high --output json \
	    > "reports/images/$$name.json" 2> "reports/images/$$name.log" \
	    || { scan_exit=$$?; cat "reports/images/$$name.log"; \
	        if [ "$$scan_exit" -eq 2 ] && [ "$${VERIFY_ACCEPT_RECORDED_FINDINGS:-}" = true ] && \
	            python3 scripts/check_recorded_cves.py "$$image" < "reports/images/$$name.json" 3< "reports/images/$$name.log"; then \
	          echo "::warning::Owner-accepted High findings in $$image for this development PR only"; \
	        else echo "FAIL: CVE audit $$image; findings: reports/images/$$name.json"; status=1; fi; }; \
	done; exit $$status

docker-check:
	mkdir -p reports
	docker build --check apps/api > reports/api-docker-check.log 2>&1 || { cat reports/api-docker-check.log; exit 1; }
	docker build --check -f apps/web/Dockerfile . > reports/web-docker-check.log 2>&1 || { cat reports/web-docker-check.log; exit 1; }
	docker build --check infrastructure/caddy > reports/caddy-docker-check.log 2>&1 || { cat reports/caddy-docker-check.log; exit 1; }
	docker build --check infrastructure/postgres > reports/postgres-docker-check.log 2>&1 || { cat reports/postgres-docker-check.log; exit 1; }
	docker build --check -f infrastructure/quality/Dockerfile . > reports/quality-docker-check.log 2>&1 || { cat reports/quality-docker-check.log; exit 1; }

test: setup docker-check python-tests api-checks web-checks
	$(MAKE) --no-print-directory ci-service-checks

# Run Caddy and PostgreSQL checks after the calling Make target's prerequisites.
ci-service-checks:
	mkdir -p reports
	docker build --progress=plain --target checks --output type=cacheonly infrastructure/caddy > reports/caddy-checks.log 2>&1 || { cat reports/caddy-checks.log; exit 1; }
	docker build --progress=plain --target checks --output type=cacheonly infrastructure/postgres > reports/postgres-checks.log 2>&1 || { cat reports/postgres-checks.log; exit 1; }

python-version-check:
	python3 -c 'import platform, sys; from pathlib import Path; expected = Path(".python-version").read_text().strip(); actual = platform.python_version(); print(sys.executable, actual); sys.exit(0 if platform.python_implementation() == "CPython" and actual == expected else "Required CPython version: " + expected)'

python-tests: python-version-check
	mkdir -p reports
	python3 -m unittest discover -s scripts/tests -v > reports/python-checks.log 2>&1 || { cat reports/python-checks.log; exit 1; }

api-checks:
	mkdir -p reports
	docker build --progress=plain --target checks --output type=cacheonly apps/api > reports/api-checks.log 2>&1 || { cat reports/api-checks.log; exit 1; }

web-checks:
	mkdir -p reports
	docker build --progress=plain -f apps/web/Dockerfile --target checks --output type=cacheonly . > reports/web-checks.log 2>&1 || { cat reports/web-checks.log; exit 1; }

smoke: setup
	python3 scripts/smoke.py --site "$(SITE)"

e2e: smoke

lifecycle: setup
	python3 scripts/smoke.py --site "$(SITE)" --lifecycle

isolation: setup
	python3 scripts/check-isolation.py --site "$(SITE)"

migrate import-languages:
	@test "$(origin SITE)" = "command line" || { echo 'Specify SITE on the command line for database maintenance'; exit 2; }
	python3 scripts/db_tools.py --site "$(SITE)" $@ $(if $(filter migrate,$@),$(or $(ACTION),up)) $(if $(filter import-languages,$@),"$(FILE)") $(if $(and $(filter migrate,$@),$(filter down,$(ACTION)),$(filter 1,$(DISPOSABLE))),--disposable)

db-test: sdk-check
	$(MAKE) --no-print-directory ci-db-test

ci-db-test:
	python3 scripts/test-publishing.py

publishing-test: sdk-check
	$(MAKE) --no-print-directory ci-publishing-test

ci-publishing-test:
	cd apps/web && npm ci --ignore-scripts && node node_modules/playwright/cli.js install --with-deps --only-shell chromium
	python3 scripts/test-publishing.py --runtime

sdk:
	cd packages/sdk && npm ci --ignore-scripts && STEPOLONIA_GO_EXECUTABLE="$$(command -v go)" npm run generate

swagger-assets-check:
	cd apps/api/internal/platform/docsassets && npm ci --ignore-scripts --no-audit && \
		cmp swagger-ui.css node_modules/swagger-ui-dist/swagger-ui.css && \
		cmp swagger-ui-bundle.js node_modules/swagger-ui-dist/swagger-ui-bundle.js && \
		cmp swagger-ui-standalone-preset.js node_modules/swagger-ui-dist/swagger-ui-standalone-preset.js && \
		npm audit --package-lock-only --omit=dev --audit-level=high

sdk-check: swagger-assets-check
	cd packages/sdk && npm ci --ignore-scripts && STEPOLONIA_GO_EXECUTABLE="$$(command -v go)" npm run check
