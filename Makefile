-include .site.mk
SITE ?= example.test
COMPOSE = python3 scripts/site_config.py --site "$(SITE)"

.PHONY: setup up down config build docker-check test smoke lifecycle isolation e2e migrate seed import-languages db-test sdk

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

docker-check:
	mkdir -p reports
	docker build --check apps/api > reports/api-docker-check.log 2>&1 || { cat reports/api-docker-check.log; exit 1; }
	docker build --check apps/web > reports/web-docker-check.log 2>&1 || { cat reports/web-docker-check.log; exit 1; }
	docker build --check infrastructure/postgres > reports/postgres-docker-check.log 2>&1 || { cat reports/postgres-docker-check.log; exit 1; }

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
