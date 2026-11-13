#!/usr/bin/env python3
"""Verify schema and tools on an owned, disposable PostgreSQL installation."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess

from db_tools import run_tool
from site_config import ROOT, compose_command, load_config, site_directory


LABEL = "io.stepolonia.stage12-test"


def command(arguments, log, *, env=None, capture=False, cwd=ROOT):
    result = subprocess.run(arguments, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE if capture else log,
                            stderr=log, check=True)
    return result.stdout.strip() if capture else ""


def verify_owned(project, token, log):
    # Never remove resources based on a guessed container or volume name.
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
            labels = item.get("Config", {}).get("Labels", {}) if resource == "container" else item.get("Labels", {})
            if labels.get(LABEL) != token:
                raise RuntimeError(f"Очистка запрещена: другой маркер владельца ресурса {resource}")


def main():
    if shutil.which("go") is None:
        raise SystemExit("Для интеграционных проверок БД нужен Go 1.27")
    token = secrets.token_hex(6)
    site = f"stage12-{token}.test"
    project = f"sp-stage12-{token}"
    database = f"stage12_{token}"
    directory = site_directory(site)
    directory.mkdir(mode=0o700, exist_ok=False)
    reports = ROOT / "reports"
    reports.mkdir(exist_ok=True)
    override = reports / f"publishing-{token}.compose.json"
    log_path = reports / f"publishing-{token}.log"
    labels = {LABEL: token}
    values = {
        "SITE_ID": site, "COMPOSE_PROJECT_NAME": project,
        "POSTGRES_DB": database, "POSTGRES_USER": "stage12_owner", "DB_USER": "stage12_app",
        "DB_VOLUME_NAME": f"{project}-data", "DB_VOLUME_EXTERNAL": "false",
        "NUXT_PUBLIC_SITE_NAME": "Schema integration", "NUXT_PUBLIC_SITE_LOCALE": "ru",
    }
    env_file = directory / ".env"
    descriptor = os.open(env_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as output:
        output.write("".join(f"{key}={json.dumps(value)}\n" for key, value in values.items()))
    services = {
        "postgres-init": {"image": f"{project}-init:check", "labels": labels},
        "postgres": {"image": f"{project}-postgres:check", "labels": labels,
                     "ports": [{"target": 5432, "published": "0", "host_ip": "127.0.0.1"}]},
        "db-tools": {"image": f"{project}-tools:check", "labels": labels},
    }
    override.write_text(json.dumps({"services": services,
                                   "networks": {"data": {"internal": False, "labels": labels}},
                                   "volumes": {"postgres_data": {"name": values["DB_VOLUME_NAME"],
                                                                  "external": False, "labels": labels}}}))
    compose = [*compose_command(site), "-f", str(override), "--project-name", project]
    success = False
    try:
        subprocess.run(["python3", "scripts/setup-local.py", "--site", site], cwd=ROOT, check=True)
        config = load_config(site)
        if config["name"] != project or config["volumes"]["postgres_data"]["name"] != values["DB_VOLUME_NAME"]:
            raise RuntimeError("Не совпадает идентичность временной установки")
        print(f"Проверка временной установки {site}; лог: {log_path}", flush=True)
        with log_path.open("w") as log:
            command([*compose, "build", "postgres-init", "postgres", "db-tools"], log)
            command([*compose, "up", "-d", "--wait", "--wait-timeout", "120", "postgres"], log)
            container = command([*compose, "ps", "-q", "postgres"], log, capture=True).split()
            inspected = json.loads(command(["docker", "inspect", *container], log, capture=True))
            if len(inspected) != 1 or inspected[0]["Config"]["Labels"].get(LABEL) != token:
                raise RuntimeError("Не совпадает идентичность контейнера PostgreSQL")
            bindings = inspected[0]["NetworkSettings"]["Ports"].get("5432/tcp") or []
            log.write("Порты тестовой PostgreSQL: " + json.dumps(bindings) + "\n")
            if not bindings or any(b["HostIp"] != "127.0.0.1" or not b["HostPort"].isdecimal() for b in bindings):
                raise RuntimeError("Тестовый порт PostgreSQL должен быть доступен только на loopback")
            port = bindings[0]["HostPort"]
            # Exercise the shipped executable before testing SQL through Go.
            run_tool(site, ["prepare-roles"], override=override, log=log)
            run_tool(site, ["migrate", "up"], override=override, log=log)
            run_tool(site, ["seed"], override=override, log=log)
            csv = directory / "languages.csv"
            csv.write_text("code,name,native_name,direction\nfr,French,Français,ltr\n", encoding="utf-8")
            run_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)
            run_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)
            environment = dict(os.environ)
            environment["STAGE12_TEST_DATABASE_URL"] = (
                f"postgres://stage12_owner@127.0.0.1:{port}/{database}?sslmode=disable")
            environment["STAGE12_TEST_PASSWORD_FILE"] = str(directory / ".secrets/db_owner_password")
            command(["go", "test", "-count=1", "-race", "-v", "./..."], log, env=environment, cwd=ROOT / "apps/api")
            success = True
    finally:
        with log_path.open("a") as log:
            verify_owned(project, token, log)
            command([*compose, "down", "--volumes", "--remove-orphans"], log)
        shutil.rmtree(directory)
        override.unlink()
    if success:
        print(f"PASS: схема PostgreSQL, CLI и интеграционные проверки; временные ресурсы удалены. Лог: {log_path}")


if __name__ == "__main__":
    main()
