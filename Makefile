-include .site.mk
SITE ?= example.test
COMPOSE = python3 scripts/site_config.py --site "$(SITE)"
GRYPE_IMAGE = anchore/grype:v0.121.3

.PHONY: setup up down config build docker-check test smoke lifecycle isolation e2e migrate seed import-languages db-test publishing-test sdk sdk-check lint lint-go lint-python lint-web duplicates image-audit verify check-go check-python check-web check-db api-checks web-checks python-tests

setup:
	python3 scripts/setup-local.py --site "$(SITE)"

up: setup
	$(COMPOSE) up --build -d --wait --wait-timeout 120

down:
	$(COMPOSE) down

config: setup
	$(COMPOSE) config --quiet

build: setup
	$(COMPOSE) build

lint: lint-go lint-python lint-web duplicates sdk-check

# Во время разработки запускаем только нужную подсистему; перед merge полный verify.
check-go: lint-go api-checks

check-python: lint-python python-tests

check-web: lint-web web-checks

check-db: db-test

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

# Каждый prerequisite исполняется один раз, включая lint -> duplicates.
verify: lint config test build db-test publishing-test image-audit

# Сканируем содержимое собранных образов; daemon socket и секреты сканеру не передаются.
# Ошибка загрузки CVE DB также блокирует проверку. High/Critical без ignore-unfixed.
image-audit: lint build
	mkdir -p reports/images
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
	    || { cat "reports/images/$$name.log"; echo "FAIL: CVE audit $$image; findings: reports/images/$$name.json"; status=1; }; \
	done; exit $$status

docker-check:
	mkdir -p reports
	docker build --check apps/api > reports/api-docker-check.log 2>&1 || { cat reports/api-docker-check.log; exit 1; }
	docker build --check -f apps/web/Dockerfile . > reports/web-docker-check.log 2>&1 || { cat reports/web-docker-check.log; exit 1; }
	docker build --check infrastructure/caddy > reports/caddy-docker-check.log 2>&1 || { cat reports/caddy-docker-check.log; exit 1; }
	docker build --check infrastructure/postgres > reports/postgres-docker-check.log 2>&1 || { cat reports/postgres-docker-check.log; exit 1; }
	docker build --check -f infrastructure/quality/Dockerfile . > reports/quality-docker-check.log 2>&1 || { cat reports/quality-docker-check.log; exit 1; }

test: setup docker-check python-tests api-checks web-checks
	mkdir -p reports
	docker build --progress=plain --target checks -t sp-caddy-checks:stage0 infrastructure/caddy > reports/caddy-checks.log 2>&1 || { cat reports/caddy-checks.log; exit 1; }
	docker build --progress=plain --target checks -t sp-postgres-checks:stage0 infrastructure/postgres > reports/postgres-checks.log 2>&1 || { cat reports/postgres-checks.log; exit 1; }

python-tests:
	mkdir -p reports
	python3 -m unittest discover -s scripts/tests -v > reports/python-checks.log 2>&1 || { cat reports/python-checks.log; exit 1; }

api-checks:
	mkdir -p reports
	docker build --progress=plain --target checks -t sp-api-checks:stage0 apps/api > reports/api-checks.log 2>&1 || { cat reports/api-checks.log; exit 1; }

web-checks:
	mkdir -p reports
	docker build --progress=plain -f apps/web/Dockerfile --target checks -t sp-web-checks:stage0 . > reports/web-checks.log 2>&1 || { cat reports/web-checks.log; exit 1; }

smoke: setup
	python3 scripts/smoke.py --site "$(SITE)"

e2e: smoke

lifecycle: setup
	python3 scripts/smoke.py --site "$(SITE)" --lifecycle

isolation: setup
	python3 scripts/check-isolation.py --site "$(SITE)"

migrate seed import-languages:
	@test "$(origin SITE)" = "command line" || { echo 'Укажите SITE в командной строке для обслуживания БД'; exit 2; }
	python3 scripts/db_tools.py --site "$(SITE)" $@ $(if $(filter migrate,$@),$(or $(ACTION),up)) $(if $(filter import-languages,$@),"$(FILE)") $(if $(and $(filter migrate,$@),$(filter down,$(ACTION)),$(filter 1,$(DISPOSABLE))),--disposable)

db-test: sdk-check
	python3 scripts/test-publishing.py

publishing-test: sdk-check
	cd apps/web && npm ci --ignore-scripts && node node_modules/playwright/cli.js install --with-deps chromium
	python3 scripts/test-publishing.py --runtime

sdk:
	cd packages/sdk && npm ci --ignore-scripts && STEPOLONIA_GO_EXECUTABLE="$$(command -v go)" npm run generate

sdk-check:
	cd packages/sdk && npm ci --ignore-scripts && STEPOLONIA_GO_EXECUTABLE="$$(command -v go)" npm run check
