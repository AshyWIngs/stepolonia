#!/usr/bin/env python3
"""Stage 0 runtime checks. --lifecycle temporarily stops this local stack."""
import argparse
from datetime import datetime, timezone
from html import escape
from http.client import HTTPConnection
import json
import os
import subprocess
import time
from urllib.parse import urlsplit

from site_config import ROOT, compose_command, load_config, postgres_sql_args

COMPOSE = []
RESULTS = []
READY_PATH = "/health/ready"
LIVE_PATH = "/health/live"


def run(args, *, check=True):
    return subprocess.run(args, cwd=ROOT, check=check, capture_output=True, text=True)


def compose(*args, check=True):
    return run([*COMPOSE, *args], check=check)


def verify(name, condition):
    result = {"check": name, "status": "PASS" if condition else "FAIL"}
    RESULTS.append(result)
    print(f"{result['status']}: {name}", flush=True)
    if not condition:
        raise AssertionError(name)


def request(url, *, method="GET"):
    target = urlsplit(url)
    if (target.scheme != "http" or target.hostname != "127.0.0.1" or target.username is not None
            or target.port is None or not 1 <= target.port <= 65535):
        raise ValueError("Smoke разрешает только HTTP loopback с явным TCP-портом")
    # Прямое соединение исключает file/custom schemes, proxy и следование redirects.
    connection = HTTPConnection("127.0.0.1", target.port, timeout=5)
    try:
        path = target.path or "/"
        if target.query:
            path += "?" + target.query
        connection.request(method, path)
        response = connection.getresponse()
        return response.status, response.headers, response.read().decode()
    finally:
        connection.close()


def private_api(path):
    code = (f"const r = await fetch('http://api:8080{path}', "
            "{signal: AbortSignal.timeout(3000)}); "
            "console.log(JSON.stringify({status:r.status, body:await r.text()}))")
    output = compose("exec", "-T", "web", "/usr/bin/node", "--input-type=module", "-e", code)
    return json.loads(output.stdout)


def wait_ready():
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        try:
            if private_api(READY_PATH)["status"] == 200:
                return
        except (subprocess.CalledProcessError, json.JSONDecodeError):
            pass
        time.sleep(1)
    raise AssertionError("API did not recover within 60 seconds")


def publishing_sdk(base, *, unavailable=False):
    """Используем тот же SDK и статью, которую публикует Go runtime fixture."""
    environment = {**os.environ, "STAGE14_API_URL": base, "STAGE14_ARTICLE_LANG": "fr",
                   "STAGE14_ARTICLE_SLUG": "stage15-fr-current", "STAGE14_REDIRECT_SLUG": "stage15-fr-first"}
    # B603 требует аудита даже фиксированного argv. Программа и script постоянны,
    # режим имеет два литеральных значения, env принадлежит disposable harness.
    # Пересмотреть при изменении executable/script/mode или источника окружения.
    result = subprocess.run(  # nosec B603 - фиксированный SDK argv, доверенное test env
        ["node", "packages/sdk/tests/live.mjs", "--unavailable" if unavailable else "--published"],
        cwd=ROOT, check=True, capture_output=True, text=True, env=environment)
    print(result.stdout, end="", flush=True)


def check_publishing(base):
    """Проверяем Publishing через Caddy на подготовленной одноразовой установке."""
    publishing_sdk(base)
    status, _, body = request(base + "/api/v1/articles?lang=en")
    verify("Caddy: enabled empty language returns 200 []", status == 200 and json.loads(body) == [])
    for language in ("pl", "de"):
        status, _, _ = request(base + "/api/v1/articles?lang=" + language)
        verify(f"Caddy: unavailable language {language} has no fallback (HTTP {status})", status == 404)
    for path in ("/docs", "/openapi.json", "/schemas", "/schemas/Article.json"):
        status, _, _ = request(base + path)
        verify(f"Caddy: publishing contract route {path} is private (HTTP {status})", status == 404)
    verify("Caddy: public article write endpoint is absent",
           request(base + "/api/v1/articles", method="POST")[0] in (404, 405))


def check_publishing_recovery(base):
    """Остановка разрешена вызывающим harness только после проверки ownership-маркеров."""
    target = base + "/api/v1/articles/stage15-fr-current?lang=fr"
    before = request(target)
    verify("Caddy: imported fifth language is published", before[0] == 200)
    identifier = compose("ps", "-q", "api").stdout.strip()
    previous = json.loads(run(["docker", "inspect", identifier]).stdout)[0]
    try:
        compose("stop", "postgres")
        verify("Publishing outage: private liveness stays healthy", private_api(LIVE_PATH)["status"] == 200)
        verify("Publishing outage: private readiness reports 503", private_api(READY_PATH)["status"] == 503)
        publishing_sdk(base, unavailable=True)
    finally:
        compose("start", "postgres")
        wait_ready()
    publishing_sdk(base)
    verify("Publishing recovers with the same public snapshot", request(target)[2] == before[2])
    current = json.loads(run(["docker", "inspect", identifier]).stdout)[0]
    verify("API recovered without container or process restart",
           compose("ps", "-q", "api").stdout.strip() == identifier
           and current["RestartCount"] == previous["RestartCount"]
           and current["State"]["StartedAt"] == previous["State"]["StartedAt"])


def main(site, lifecycle):
    config = load_config(site)
    port = config["services"]["caddy"]["ports"][0]
    host = port.get("host_ip", "127.0.0.1")
    verify("local-only Caddy binding", host == "127.0.0.1")
    base = f"http://127.0.0.1:{port['published']}"
    containers = {}
    for service in ("caddy", "web", "api", "postgres"):
        container_id = compose("ps", "-q", service).stdout.strip()
        verify(f"{service}: container exists", bool(container_id))
        container = json.loads(run(["docker", "inspect", container_id]).stdout)[0]
        containers[service] = container
        verify(f"{service}: healthy", container["State"].get("Health", {}).get("Status") == "healthy")
        verify(f"{service}: non-root", container["Config"]["User"] not in ("", "0", "root", "0:0"))
        verify(f"{service}: read-only root filesystem", container["HostConfig"]["ReadonlyRootfs"])
        verify(f"{service}: all capabilities dropped", "ALL" in container["HostConfig"]["CapDrop"])
        if service != "caddy":
            verify(f"{service}: no published ports", not container["HostConfig"]["PortBindings"])
    network_name = next(iter(containers["postgres"]["NetworkSettings"]["Networks"]))
    network = json.loads(run(["docker", "network", "inspect", network_name]).stdout)[0]
    verify("data network is internal", network["Internal"])
    verify("PostgreSQL only joins data network", len(containers["postgres"]["NetworkSettings"]["Networks"]) == 1)
    verify("web does not join data network", network_name not in containers["web"]["NetworkSettings"]["Networks"])
    status, headers, body = request(base + "/")
    site_name = config["services"]["web"]["environment"]["NUXT_PUBLIC_SITE_NAME"]
    verify("Nuxt production SSR uses selected site branding", status == 200
           and f"<h1>{escape(site_name, quote=False)}</h1>" in body)
    verify("proxy security header", headers.get("X-Content-Type-Options") == "nosniff")
    status, headers, body = request(base + "/api/v1/unknown")
    verify("Caddy routes API requests to Go", status == 404 and json.loads(body)["status"] == 404
           and bool(headers.get("X-Request-ID")) and headers.get_content_type() == "application/problem+json")
    for path in (LIVE_PATH, READY_PATH, "/docs", "/openapi.json"):
        verify(f"private external route {path}", request(base + path)[0] == 404)
    verify("private API readiness checks PostgreSQL", private_api(READY_PATH)["status"] == 200)
    privilege = compose(*postgres_sql_args(config,
                        "SELECT NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole "
                        "AND NOT has_schema_privilege(current_user, 'public', 'CREATE') "
                        "FROM pg_roles WHERE rolname=current_user", runtime_role=True)).stdout.strip()
    verify("API database role has restricted privileges", privilege == "t")
    for service in ("caddy", "api", "web", "postgres"):
        verify(f"{service}: distroless has no shell", compose("exec", "-T", service, "/bin/sh", "-c", "exit 0", check=False).returncode != 0)
    init_id = compose("ps", "--all", "-q", "postgres-init").stdout.strip()
    initializer = json.loads(run(["docker", "inspect", init_id]).stdout)[0]
    verify("PostgreSQL initializer completed and is not running", initializer["State"]["Status"] == "exited"
           and initializer["State"]["ExitCode"] == 0)
    verify("PostgreSQL initializer has no network", initializer["HostConfig"]["NetworkMode"] == "none")
    verify("PostgreSQL runtime has no owner password secret", all(
        mount["Destination"] != "/run/secrets/db_owner_password"
        for mount in containers["postgres"]["Mounts"]))
    if not lifecycle:
        return

    check_lifecycle(config, base, containers)


def check_lifecycle(config, base, containers):
    """Проверяем восстановление и сохранность БД только по явному --lifecycle."""
    # No application data is modified by fault or restart checks.
    identifier_cmd = postgres_sql_args(config, "SELECT system_identifier FROM pg_control_system()")
    identifier = compose(*identifier_cmd).stdout.strip()
    try:
        compose("stop", "postgres")
        verify("API liveness survives database outage", private_api(LIVE_PATH)["status"] == 200)
        unavailable = private_api(READY_PATH)
        verify("API readiness reports database outage", unavailable["status"] == 503
               and config["services"]["api"]["environment"]["DB_USER"] not in unavailable["body"])
    finally:
        compose("start", "postgres")
        wait_ready()
    verify("API recovers without restart", private_api(READY_PATH)["status"] == 200)

    compose("stop")
    for service, container in containers.items():
        stopped = json.loads(run(["docker", "inspect", container["Id"]]).stdout)[0]
        verify(f"{service}: graceful exit without OOM", stopped["State"]["ExitCode"] == 0
               and not stopped["State"]["OOMKilled"])
    logs = compose("logs", "--no-color", "api").stdout
    verify("Go completed graceful shutdown", '"msg":"shutdown_complete"' in logs)
    compose("up", "-d", "--force-recreate", "--wait", "--wait-timeout", "120")
    verify("database survives container replacement", compose(*identifier_cmd).stdout.strip() == identifier)
    verify("stack serves SSR after replacement", request(base + "/")[0] == 200)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", required=True)
    parser.add_argument("--lifecycle", action="store_true")
    args = parser.parse_args()
    COMPOSE = compose_command(args.site)
    overall_status = "FAIL"
    try:
        main(args.site, args.lifecycle)
        overall_status = "PASS"
    finally:
        report = ROOT / "reports" / f"{args.site}-smoke.json"
        report.parent.mkdir(exist_ok=True)
        report.write_text(json.dumps({"recorded_at": datetime.now(timezone.utc).isoformat(),
                                      "status": overall_status,
                                      "site": args.site,
                                      "lifecycle": args.lifecycle, "checks": RESULTS}, indent=2) + "\n")
