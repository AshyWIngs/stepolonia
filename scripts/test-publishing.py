#!/usr/bin/env python3
"""Verify schema and tools on an owned, disposable PostgreSQL installation."""
import argparse
from contextlib import redirect_stdout
from functools import partial
from ipaddress import IPv4Address
import json
import os
import secrets
import shutil
import subprocess
from tempfile import NamedTemporaryFile
from urllib.parse import urlsplit

from db_tools import DISPOSABLE_MARKER, run_tool
import smoke
from site_config import ROOT, compose_command, load_config, postgres_sql_args, site_directory


run_prebuilt_tool = partial(run_tool, build_image=False)
OWNERSHIP_LABEL = "io.stepolonia.stage12-test"


def command(arguments, log, *, env=None, capture=False, cwd=ROOT):
    result = subprocess.run(arguments, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE if capture else log,
                            stderr=log, check=True)
    return result.stdout.strip() if capture else ""


def verify_named_volume(project, token, volume, log):
    """Проверяем точный том: чужой том может не попасть в фильтр Compose project."""
    existing = command(["docker", "volume", "ls", "--format", "{{.Name}}"], log, capture=True).splitlines()
    if volume not in existing:
        return
    inspected = json.loads(command(["docker", "volume", "inspect", volume], log, capture=True))
    if len(inspected) != 1 or (inspected[0].get("Labels") or {}).get(OWNERSHIP_LABEL) != token:
        raise RuntimeError(f"Очистка запрещена: том {volume} не принадлежит тесту {project}")


def verify_resource_owner(item, resource, project, token):
    """Отсутствующий или чужой маркер запрещает очистку найденного ресурса."""
    metadata = item.get("Config", {}) if resource == "container" else item
    labels = metadata.get("Labels") or {}
    if labels.get(OWNERSHIP_LABEL) != token:
        raise RuntimeError(f"Очистка запрещена: другой маркер владельца ресурса {resource}; "
                           f"проект {project}. Сохраните ресурсы и проверьте их владельца вручную")


def verify_owned(project, token, log, *, volume=None):
    # Имена ресурсов недостаточны: удалять можно только ресурсы с нашим маркером.
    if volume is not None:
        verify_named_volume(project, token, volume, log)
    for resource, listing, inspect in (
        ("container", ["docker", "ps", "-aq"], ["docker", "inspect"]),
        ("volume", ["docker", "volume", "ls", "-q"], ["docker", "volume", "inspect"]),
        ("network", ["docker", "network", "ls", "-q"], ["docker", "network", "inspect"]),
    ):
        ids = command([*listing, "--filter", f"label=com.docker.compose.project={project}"],
                      log, capture=True).split()
        if not ids:
            continue
        resources = json.loads(command([*inspect, *ids], log, capture=True))
        for item in resources:
            verify_resource_owner(item, resource, project, token)


def prepare_installation(site, project, database, token, *, runtime=False):
    """Создаём новую одноразовую конфигурацию с маркерами владельца ресурсов."""
    directory = site_directory(site)
    directory.mkdir(mode=0o700, exist_ok=False)
    reports = ROOT / "reports"
    reports.mkdir(exist_ok=True)
    override = reports / f"publishing-{token}.compose.json"
    log_path = reports / f"publishing-{token}.log"
    values = {
        "SITE_ID": site, "COMPOSE_PROJECT_NAME": project,
        "POSTGRES_DB": database, "POSTGRES_USER": "stage12_owner", "DB_USER": "stage12_app",
        "DB_VOLUME_NAME": f"{project}-data", "DB_VOLUME_EXTERNAL": "false",
        "HTTP_BIND": "127.0.0.1", "HTTP_PORT": "0",
    }
    env_file = directory / ".env"
    descriptor = os.open(env_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as output:
        output.write("".join(f"{key}={json.dumps(value)}\n" for key, value in values.items()))
    descriptor = os.open(directory / DISPOSABLE_MARKER, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as output:
        json.dump({"token": token, "database": database, "project": project,
                   "volume": values["DB_VOLUME_NAME"]}, output)
    override.write_text(json.dumps(installation_override(project, token, values["DB_VOLUME_NAME"], runtime=runtime)))
    return directory, override, log_path, values["DB_VOLUME_NAME"]


def installation_override(project, token, volume, *, runtime=False, images_project=None):
    """Один источник конфигурации ресурсов для основной и второй одноразовых установок."""
    images = images_project or project
    labels = {OWNERSHIP_LABEL: token}
    services = {
        "postgres-init": {"image": f"{images}-init:check", "labels": labels},
        "postgres": {"image": f"{images}-postgres:check", "labels": labels,
                     "ports": [{"target": 5432, "published": "0", "host_ip": "127.0.0.1"}]},
        "db-tools": {"image": f"{images}-tools:check", "labels": labels},
    }
    if runtime:
        for service in ("api", "web", "caddy"):
            services[service] = {"image": f"{images}-{service}:check", "labels": labels}
    return {"services": services,
            "networks": {"data": {"internal": False, "labels": labels}, "app": {"labels": labels}},
            "volumes": {"postgres_data": {"name": volume, "external": False, "labels": labels}}}


def check_cli(site, directory, override, compose, config, log):
    """Проверяем поставляемый CLI и защиту Down до SQL-сценариев Go."""
    # Проверяем поставляемый CLI, включая защищённый Down, до SQL-тестов Go.
    run_prebuilt_tool(site, ["prepare-roles"], override=override, log=log)
    run_prebuilt_tool(site, ["migrate", "up"], override=override, log=log)
    # Обходим Python guard только в этом отрицательном тесте настоящего Go CLI.
    # Сравниваем всю историю версий и publishing-каталог, чтобы заметить выполненный Down.
    snapshot_query = """SELECT json_build_object(
 'migration_history', (SELECT json_agg(row_to_json(v) ORDER BY id) FROM public.goose_db_version v),
 'relations', (SELECT json_agg(json_build_array(relname,relkind,oid,relowner,relacl,reloptions) ORDER BY relname)
       FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace),
 'views', (SELECT json_agg(json_build_array(viewname,definition) ORDER BY viewname)
   FROM pg_catalog.pg_views WHERE schemaname='public'))::text"""
    before_down = command([*compose, *postgres_sql_args(config, snapshot_query)], log, capture=True)
    rejected_down = subprocess.run(
        [*compose, "run", "--rm", "--no-deps", "-T", "--env", "DB_DISPOSABLE_TOKEN=",
         "db-tools", "migrate", "down"], cwd=ROOT, text=True,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    log.write(rejected_down.stdout)
    after_down = command([*compose, *postgres_sql_args(config, snapshot_query)], log, capture=True)
    if (rejected_down.returncode == 0 or "для down требуется DB_DISPOSABLE_TOKEN" not in rejected_down.stdout
            or before_down != after_down):
        raise RuntimeError("CLI Down с пустым токеном должен отказать без изменения каталога и версий")
    log.write("PASS: CLI Down с пустым токеном отклонён; catalog/version сохранены\n")
    run_prebuilt_tool(site, ["migrate", "down"], override=override, log=log, allow_disposable_down=True)
    run_prebuilt_tool(site, ["migrate", "up"], override=override, log=log)
    run_prebuilt_tool(site, ["seed"], override=override, log=log)
    csv = directory / "languages.csv"
    csv.write_text("code,name,native_name,direction\nfr,French,Français,ltr\n", encoding="utf-8")
    run_prebuilt_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)
    run_prebuilt_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)


def published_port(compose, service, target, token, log):
    containers = command([*compose, "ps", "-q", service], log, capture=True).split()
    inspected = json.loads(command(["docker", "inspect", *containers], log, capture=True))
    if len(inspected) != 1 or inspected[0]["Config"]["Labels"].get(OWNERSHIP_LABEL) != token:
        raise RuntimeError(f"Не совпадает идентичность контейнера {service}")
    bindings = inspected[0]["NetworkSettings"]["Ports"].get(f"{target}/tcp") or []
    if (len(bindings) != 1 or bindings[0]["HostIp"] != "127.0.0.1"
            or not bindings[0]["HostPort"].isdecimal()):
        raise RuntimeError("Тестовый порт должен быть доступен только на loopback")
    return int(bindings[0]["HostPort"])


def check_runtime(site, directory, override, compose, environment, project, token, log):
    """Принимаем контейнерный Publishing без повторения ранее принятых SQL-сценариев."""
    run_prebuilt_tool(site, ["prepare-roles"], override=override, log=log)
    run_prebuilt_tool(site, ["migrate", "up"], override=override, log=log)
    run_prebuilt_tool(site, ["seed"], override=override, log=log)
    csv = directory / "languages.csv"
    csv.write_text("code,name,native_name,direction\nfr,French,Français,ltr\n", encoding="utf-8")
    run_prebuilt_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)
    command([*compose, "build", "api", "web", "caddy"], log)
    command([*compose, "up", "-d", "--wait", "--wait-timeout", "120", "caddy"], log)
    port = published_port(compose, "caddy", 8080, token, log)
    base = f"http://127.0.0.1:{port}"
    command(["go", "test", "-count=1", "-race", "-v", "-run", "^TestContentRuntimeFixture$",
             "./internal/content"], log, env={**environment, "STAGE15_API_URL": base, "STAGE32_CONTENT_FIXTURE": "primary"}, cwd=ROOT / "apps/api")
    publish_site_fixture(environment, "primary", log)
    smoke.COMPOSE = compose
    with redirect_stdout(log):
        smoke.check_publishing(base)
        # Перед реальной остановкой БД повторно подтверждаем владельца всех ресурсов.
        verify_owned(project, token, log)
        smoke.check_publishing_recovery(base)
    check_localization(base, project, compose, log)


def localization_browser(base, mode, log, **environment):
    """Один browser harness проверяет реальные установки через Caddy."""
    command(["node", "apps/web/tests/live.mjs", mode], log,
            env={**os.environ, "STAGE14_API_URL": base, **environment})


def check_localization(base, images_project, primary_compose, log):
    """Вторая изолированная БД исключает смешивание материалов и предпочтений."""
    token = secrets.token_hex(6)
    site = f"stage22-{token}.test"
    project = f"sp-stage22-{token}"
    database = f"stage12_{token}"
    directory, override, unused_log, volume = prepare_installation(
        site, project, database, token, runtime=True)
    configuration = installation_override(project, token, volume, runtime=True, images_project=images_project)
    override.write_text(json.dumps(configuration))
    compose = [*compose_command(site), "-f", str(override), "--project-name", project]
    resources_owned = False
    try:
        command(["python3", "scripts/setup-local.py", "--site", site], log)
        config = load_config(site, override=override)
        if config["name"] != project or config["volumes"]["postgres_data"]["name"] != volume:
            raise RuntimeError("Не совпадает идентичность второй временной установки")
        verify_owned(project, token, log, volume=volume)
        resources_owned = True
        command([*compose, "up", "-d", "--wait", "--wait-timeout", "120", "postgres"], log)
        query = f'COMMENT ON DATABASE "{database}" IS \'stepolonia:disposable:{token}\''
        command([*compose, *postgres_sql_args(config, query)], log)
        for arguments in (["prepare-roles"], ["migrate", "up"], ["seed"]):
            run_prebuilt_tool(site, arguments, override=override, log=log)
        languages = directory / "languages.csv"
        languages.write_text("code,name,native_name,direction\nfr,French,Français,ltr\n", encoding="utf-8")
        run_prebuilt_tool(site, ["import-languages", "/input/languages.csv"], input_file=languages,
                 override=override, log=log)
        command([*compose, *postgres_sql_args(config,
                 "UPDATE public.languages SET is_enabled=(code IN ('pl','en','fr'))")], log)
        database_port = published_port(compose, "postgres", 5432, token, log)
        environment = {
            **os.environ,
            "DB_DISPOSABLE_TOKEN": token,
            "STAGE12_TEST_DATABASE_URL": (
                f"postgres://stage12_owner@127.0.0.1:{database_port}/{database}?sslmode=disable"),
            "STAGE12_TEST_PASSWORD_FILE": str(directory / ".secrets/db_owner_password"),
            "STAGE12_TEST_RUNTIME_PASSWORD_FILE": str(directory / ".secrets/db_app_password"),
        }
        publish_site_fixture(environment, "secondary", log)
        command([*compose, "up", "-d", "--wait", "--wait-timeout", "120", "caddy"], log)
        port = published_port(compose, "caddy", 8080, token, log)
        second_base = f"http://127.0.0.1:{port}"
        localization_browser(base, "--localization", log, STAGE22_SECOND_URL=second_base)
        check_seo(base, second_base, primary_compose, compose, config, environment, log)
        check_language_configuration(second_base, compose, config, environment, log)
    finally:
        if resources_owned:
            verify_owned(project, token, log, volume=volume)
            command([*compose, "down", "--volumes", "--remove-orphans"], log)
        shutil.rmtree(directory)
        override.unlink()
        unused_log.unlink(missing_ok=True)



def seo_origin(compose, origin, log):
    # Отдельный overlay меняет только origin, не переписывая конфигурацию владения ресурсами.
    configuration = {"services": {"web": {"environment": {"NUXT_PUBLIC_SITE_ORIGIN": origin}}}}
    with NamedTemporaryFile("w", prefix="seo-origin-", suffix=".compose.json", dir=ROOT / "reports") as overlay:
        json.dump(configuration, overlay)
        overlay.flush()
        command([*compose, "-f", overlay.name, "up", "-d", "--no-deps", "--wait", "--wait-timeout", "120", "web"], log)


def check_seo(base, second, primary_compose, compose, config, environment, log):
    """SEO проверяется на общих образах, разных БД и только owned disposable-данных."""
    primary_origin = "https://primary.stepolonia.kz"
    second_origin = "https://secondary.stepolonia.kz"
    seo_origin(primary_compose, primary_origin, log)
    seo_origin(compose, second_origin, log)
    localization_browser(base, "--seo", log, STAGE23_ORIGIN=primary_origin,
                         STAGE22_SECOND_URL=second, STAGE23_SECOND_ORIGIN=second_origin)
    # The same Engine service creates bounded fixtures through its test-only actor.
    command(["go", "test", "-count=1", "-run", "^TestContentRuntimeFixture$", "./internal/content"],
            log, env={**environment, "STAGE32_CONTENT_FIXTURE": "seo"}, cwd=ROOT / "apps/api")
    localization_browser(second, "--seo-unsupported", log, STAGE23_ORIGIN=second_origin)
    logs = command([*compose, "logs", "--no-color", "web"], log, capture=True)
    if "Набор hreflang пропущен: неподдерживаемые коды языков" not in logs or "qaa" not in logs:
        raise RuntimeError("Нет диагностики пропущенного hreflang")
    command([*compose, *postgres_sql_args(config,
             "UPDATE public.languages SET is_enabled=false WHERE code='qaa'")], log)
    localization_browser(second, "--seo-pagination", log, STAGE23_ORIGIN=second_origin)
    insecure_origin = urlsplit("https://public.stepolonia.kz")._replace(scheme="http").geturl()
    # TEST-NET-1 используется только как отклоняемое значение конфигурации, не адрес соединения.
    # Все HTTP-проверки harness соединяются с отдельным фиксированным loopback-портом.
    mapped_address = IPv4Address(bytes((192, 0, 2, 1)))
    for origin in ("", insecure_origin, "https://user:password@public.stepolonia.kz",
                   "https://public.stepolonia.kz/path", "https://public.stepolonia.kz?query=1",
                   "https://public.stepolonia.kz#fragment", "https://127.0.0.1", f"https://[::ffff:{mapped_address}]", "https://private.internal"):
        seo_origin(compose, origin, log)
        localization_browser(second, "--seo-rejected-origin", log)
    seo_origin(compose, second_origin, log)


def publish_site_fixture(environment, mode, log):
    """Go service публикует только в БД с проверенным disposable-маркером."""
    command(["go", "test", "-count=1", "-run", "^TestSiteRuntimeFixture$", "./internal/database"],
            log, env={**environment, "STAGE22_SITE_FIXTURE": mode}, cwd=ROOT / "apps/api")


def check_language_configuration(base, compose, config, environment, log):
    """DB-default и публикации меняются без пересоздания web; сломанные ссылки дают 503."""
    for query in ("UPDATE public.languages SET is_enabled=false WHERE code='pl'",
                  "UPDATE public.languages SET is_enabled=false"):
        command([*compose, *postgres_sql_args(config, query)], log)
        localization_browser(base, "--config-unavailable", log)
    command([*compose, *postgres_sql_args(config,
             "UPDATE public.languages SET is_enabled=true WHERE code IN ('pl','en','fr')")], log)
    publish_site_fixture(environment, "updated", log)
    localization_browser(base, "--site-default", log, STAGE22_DEFAULT="en")
    localization_browser(base, "--site-republished", log)
    publish_site_fixture(environment, "draft", log)
    localization_browser(base, "--site-draft", log)
    publish_site_fixture(environment, "secondary", log)
    # Default pl остаётся enabled: отдельно ломаем именно опубликованный fallback en.
    command([*compose, *postgres_sql_args(config,
             "UPDATE public.languages SET is_enabled=false WHERE code='en'")], log)
    localization_browser(base, "--config-unavailable", log)


def main(runtime=False):
    if shutil.which("go") is None:
        raise SystemExit("Для интеграционных проверок БД нужен Go 1.27")
    node = shutil.which("node")
    if node is None:
        raise SystemExit("Для интеграционной проверки SDK нужен Node.js 26")
    token = secrets.token_hex(6)
    site = f"stage12-{token}.test"
    project = f"sp-stage12-{token}"
    database = f"stage12_{token}"
    directory, override, log_path, volume = prepare_installation(site, project, database, token, runtime=runtime)
    compose = [*compose_command(site), "-f", str(override), "--project-name", project]
    success = False
    resources_owned = False
    try:
        subprocess.run(["python3", "scripts/setup-local.py", "--site", site], cwd=ROOT, check=True)
        config = load_config(site, override=override)
        if config["name"] != project or config["volumes"]["postgres_data"]["name"] != volume:
            raise RuntimeError("Не совпадает идентичность временной установки")
        print(f"Проверка временной установки {site}; лог: {log_path}", flush=True)
        with log_path.open("w") as log:
            # Compose может принять существующий именованный том без наших labels.
            # Проверяем точное имя до первого запуска, а затем и перед удалением.
            verify_owned(project, token, log, volume=volume)
            resources_owned = True
            command([*compose, "build", "postgres-init", "postgres", "db-tools"], log)
            command([*compose, "up", "-d", "--wait", "--wait-timeout", "120", "postgres"], log)
            port = published_port(compose, "postgres", 5432, token, log)
            # Метку БД создаёт только владелец новой тестовой установки после проверки ресурсов.
            verify_owned(project, token, log, volume=volume)
            query = f'COMMENT ON DATABASE "{database}" IS \'stepolonia:disposable:{token}\''
            command([*compose, *postgres_sql_args(config, query)], log)
            environment = {
                **os.environ,
                "DB_DISPOSABLE_TOKEN": token,
                "STAGE12_TEST_DATABASE_URL": (
                    f"postgres://stage12_owner@127.0.0.1:{port}/{database}?sslmode=disable"),
                "STAGE12_TEST_PASSWORD_FILE": str(directory / ".secrets/db_owner_password"),
                "STAGE12_TEST_RUNTIME_PASSWORD_FILE": str(directory / ".secrets/db_app_password"),
            }
            if runtime:
                check_runtime(site, directory, override, compose, environment, project, token, log)
            else:
                check_cli(site, directory, override, compose, config, log)
                # Unit/HTTP tests уже выполняет make test; здесь нужен реальный PostgreSQL.
                command(["go", "test", "-count=1", "-race", "-v", "-run", "^TestContentSchemaIntegration$",
                         "./internal/database"], log, env=environment, cwd=ROOT / "apps/api")
                command(["go", "test", "-count=1", "-race", "-v", "-run", "^TestContentIntegration$",
                         "./internal/content"], log, env=environment, cwd=ROOT / "apps/api")
            success = True
    finally:
        if resources_owned:
            with log_path.open("a") as log:
                verify_owned(project, token, log, volume=volume)
                command([*compose, "down", "--volumes", "--remove-orphans"], log)
        shutil.rmtree(directory)
        override.unlink()
    if success:
        print(f"PASS: схема PostgreSQL, CLI и интеграционные проверки; временные ресурсы удалены. Лог: {log_path}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", action="store_true", help="Приёмка Publishing через Compose/Caddy")
    main(parser.parse_args().runtime)
