-include .site.mk
SITE ?= example.test
COMPOSE = python3 scripts/site_config.py --site "$(SITE)"

.PHONY: setup up down config build docker-check test smoke lifecycle isolation e2e migrate seed import-languages db-test sdk lint lint-go lint-python lint-web duplicates verify

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

lint: lint-go lint-python lint-web duplicates

lint-go:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target go-lint . > reports/go-lint.log 2>&1 || { cat reports/go-lint.log; exit 1; }

lint-python:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target python-lint . > reports/python-lint.log 2>&1 || { cat reports/python-lint.log; exit 1; }

lint-web:
	mkdir -p reports
	docker build --progress=plain --target lint apps/web > reports/web-lint.log 2>&1 || { cat reports/web-lint.log; exit 1; }

duplicates:
	mkdir -p reports
	docker build --progress=plain -f infrastructure/quality/Dockerfile --target duplicates . > reports/duplicates.log 2>&1 || { cat reports/duplicates.log; exit 1; }

# Каждый prerequisite исполняется один раз, включая lint -> duplicates.
verify: lint config test build db-test

docker-check:
	mkdir -p reports
	docker build --check apps/api > reports/api-docker-check.log 2>&1 || { cat reports/api-docker-check.log; exit 1; }
	docker build --check apps/web > reports/web-docker-check.log 2>&1 || { cat reports/web-docker-check.log; exit 1; }
	docker build --check infrastructure/postgres > reports/postgres-docker-check.log 2>&1 || { cat reports/postgres-docker-check.log; exit 1; }
	docker build --check -f infrastructure/quality/Dockerfile . > reports/quality-docker-check.log 2>&1 || { cat reports/quality-docker-check.log; exit 1; }

test: setup docker-check
	mkdir -p reports
	python3 -m unittest discover -s scripts/tests -v > reports/python-checks.log 2>&1 || { cat reports/python-checks.log; exit 1; }
	docker build --progress=plain --target checks -t sp-api-checks:stage0 apps/api > reports/api-checks.log 2>&1 || { cat reports/api-checks.log; exit 1; }
	docker build --progress=plain --target checks -t sp-web-checks:stage0 apps/web > reports/web-checks.log 2>&1 || { cat reports/web-checks.log; exit 1; }
	docker build --progress=plain --target checks -t sp-postgres-checks:stage0 infrastructure/postgres > reports/postgres-checks.log 2>&1 || { cat reports/postgres-checks.log; exit 1; }

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

db-test:
	python3 scripts/test-publishing.py

sdk:
	@echo "$@ is not implemented in Stage 0; Publishing, migrations and SDK belong to Stage 1."
	@exit 2
