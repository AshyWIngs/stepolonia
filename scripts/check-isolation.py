#!/usr/bin/env python3
"""Verify two installations on identical images; remove only the generated test site."""
import argparse
from datetime import datetime, timezone
from html import escape
import hashlib
import json
import secrets
import shutil
import socket
import subprocess
from urllib.request import urlopen

from site_config import ROOT, compose_command, load_config, postgres_sql_args, site_directory

RESULTS = []


def verify(name, condition):
    RESULTS.append({"check": name, "status": "PASS" if condition else "FAIL"})
    print(f"{RESULTS[-1]['status']}: {name}", flush=True)
    if not condition:
        raise AssertionError(name)


def compose(site, *args):
    return subprocess.run([*compose_command(site), *args], cwd=ROOT,
                          capture_output=True, text=True, check=True).stdout.strip()


def container(site, service):
    identifier = compose(site, "ps", "-q", service)
    return json.loads(subprocess.check_output(["docker", "inspect", identifier], text=True))[0]


def database_identifier(site):
    return compose(site, *postgres_sql_args(load_config(site),
                   "SELECT system_identifier FROM pg_control_system()"))


def verify_postgres_runtime(site, config):
    """Exercise packaging failures only on the generated, disposable cluster."""
    def sql(query, **kwargs):
        return compose(site, *postgres_sql_args(config, query, **kwargs))

    sql("CREATE EXTENSION pgcrypto; "
        "CREATE TABLE public.sp_runtime_probe (id integer PRIMARY KEY, body text NOT NULL); "
        "INSERT INTO public.sp_runtime_probe VALUES (1, 'Zażółć · Қазақша')")
    digest = sql("SELECT encode(digest('Stepolonia', 'sha256'), 'hex')")
    verify("PostgreSQL loads bundled extension and crypto libraries",
           digest == hashlib.sha256(b"Stepolonia").hexdigest())
    plan = sql("SET jit=on; SET jit_above_cost=0; "
               "EXPLAIN (ANALYZE) SELECT sum(i) FROM generate_series(1,100) AS i")
    verify("PostgreSQL loads LLVM JIT libraries", "JIT:" in plan)

    database = config["services"]["postgres"]["environment"]
    role = '"' + database["DB_USER"].replace('"', '""') + '"'
    try:
        sql("ALTER ROLE " + role + " CREATEROLE")
        probe = subprocess.run([*compose_command(site), "exec", "-T", "postgres",
                                "/usr/local/bin/pg-healthcheck"], capture_output=True)
        verify("PostgreSQL healthcheck rejects elevated runtime role", probe.returncode != 0)
    finally:
        sql("ALTER ROLE " + role + " NOCREATEROLE")
    compose(site, "exec", "-T", "postgres", "/usr/local/bin/pg-healthcheck")

    archive = subprocess.check_output([
        *compose_command(site), "exec", "-T", "postgres", "/usr/lib/postgresql/18/bin/pg_dump",
        "--username=" + database["POSTGRES_USER"], "--dbname=" + database["POSTGRES_DB"],
        "--format=custom", "--no-owner", "--no-acl", "--table=public.sp_runtime_probe"])
    sql("CREATE DATABASE sp_restore_probe TEMPLATE template0")
    subprocess.run([*compose_command(site), "exec", "-T", "postgres",
                    "/usr/lib/postgresql/18/bin/pg_restore", "--username=" + database["POSTGRES_USER"],
                    "--dbname=sp_restore_probe", "--exit-on-error", "--no-owner", "--no-acl"],
                   input=archive, capture_output=True, check=True)
    verify("PostgreSQL custom dump restores data and UTF-8 in disposable database",
           sql("SELECT id,body FROM public.sp_runtime_probe", database_name="sp_restore_probe")
           == "1|Zażółć · Қазақша")


def main(primary):
    original = load_config(primary)
    original_id = database_identifier(primary)
    test_site = "isolation-" + secrets.token_hex(6)
    directory = site_directory(test_site)
    directory.mkdir() # Exclusive creation: cleanup may touch only this generated directory.
    created = False
    try:
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        subprocess.run(["python3", str(ROOT / "scripts/setup-local.py"), "--site", test_site,
                        "--port", str(port), "--name", "Stepolonia isolation check"], check=True)
        secondary = load_config(test_site)
        verify("distinct deployment names", original["name"] != secondary["name"])
        verify("distinct PostgreSQL volumes", original["volumes"]["postgres_data"]["name"]
               != secondary["volumes"]["postgres_data"]["name"])
        verify("test resources belong to generated installation", secondary["name"] == "sp-" + test_site
               and secondary["volumes"]["postgres_data"]["name"] == "sp-" + test_site + "_postgres_data"
               and not secondary["volumes"]["postgres_data"].get("external", False))
        verify("independent runtime credentials", (site_directory(primary) / ".secrets/db_app_password").read_bytes()
               != (directory / ".secrets/db_app_password").read_bytes())
        created = True
        compose(test_site, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120")
        for service in ("api", "web", "postgres"):
            first, second = container(primary, service), container(test_site, service)
            verify(f"{service}: identical application image", first["Image"] == second["Image"])
            first_networks = {v["NetworkID"] for v in first["NetworkSettings"]["Networks"].values()}
            second_networks = {v["NetworkID"] for v in second["NetworkSettings"]["Networks"].values()}
            verify(f"{service}: separate networks", first_networks.isdisjoint(second_networks))
        verify("distinct live PostgreSQL clusters", original_id != database_identifier(test_site))
        verify_postgres_runtime(test_site, secondary)
        for site, config in ((primary, original), (test_site, secondary)):
            published = config["services"]["caddy"]["ports"][0]["published"]
            with urlopen(f"http://127.0.0.1:{published}/", timeout=5) as response:
                body = response.read().decode()
                name = config["services"]["web"]["environment"]["NUXT_PUBLIC_SITE_NAME"]
                verify(f"{site}: own branding in SSR", response.status == 200
                       and f"<h1>{escape(name, quote=False)}</h1>" in body)
            active = compose(site, *postgres_sql_args(config,
                             "SELECT DISTINCT datname || ':' || usename FROM pg_stat_activity "
                             "WHERE application_name='sp-api'"))
            api = config["services"]["api"]["environment"]
            verify(f"{site}: API uses own database and runtime role", active == api["DB_NAME"] + ":" + api["DB_USER"])
    finally:
        # Primary data and external volumes are never passed to this cleanup.
        if created:
            compose(test_site, "down", "--volumes")
        shutil.rmtree(directory)
    verify("primary database preserved after secondary teardown", original_id == database_identifier(primary))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", required=True)
    args = parser.parse_args()
    status = "FAIL"
    try:
        main(args.site)
        status = "PASS"
    finally:
        report = ROOT / "reports" / f"{args.site}-isolation.json"
        report.parent.mkdir(exist_ok=True)
        report.write_text(json.dumps({"recorded_at": datetime.now(timezone.utc).isoformat(),
                                      "site": args.site, "status": status, "checks": RESULTS}, indent=2) + "\n")
