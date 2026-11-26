#!/usr/bin/env python3
"""Run authentication checks on an owned loopback-only disposable PostgreSQL."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import time


ROOT = Path(__file__).resolve().parents[1]
OWNERSHIP_LABEL = "io.stepolonia.auth-test"
POSTGRES_IMAGE = "postgres:18.7-alpine3.24"


def command(arguments, *, environment=None, capture=False, timeout=300):
    """Execute fixed tool arguments without a shell or credentials in argv."""
    result = subprocess.run(arguments, cwd=ROOT, env=environment, check=True,
                            text=True, stdout=subprocess.PIPE if capture else None,
                            timeout=timeout)
    return result.stdout.strip() if capture else ""


def owned_container(name, token):
    """Refuse cleanup or connection if resource identity or binding differs."""
    items = json.loads(command(["docker", "inspect", name], capture=True))
    if len(items) != 1 or items[0]["Config"].get("Labels", {}).get(OWNERSHIP_LABEL) != token:
        raise RuntimeError("Disposable authentication container ownership changed")
    bindings = items[0]["NetworkSettings"]["Ports"].get("5432/tcp") or []
    if len(bindings) != 1 or bindings[0]["HostIp"] != "127.0.0.1":
        raise RuntimeError("Authentication test PostgreSQL is not loopback-only")
    return int(bindings[0]["HostPort"])


def wait_ready(name):
    """Wait a bounded time for the newly created fixture to accept connections."""
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"],
                                cwd=ROOT, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, check=False, timeout=5)
        if result.returncode == 0:
            return
        time.sleep(0.2)
    raise RuntimeError("Disposable authentication PostgreSQL did not become ready")


def main():
    """Own the complete fixture lifetime; no shared installation is modified."""
    token = secrets.token_hex(8)
    name, database = f"sp-auth-test-{token}", f"stage31_{token}"
    # Trust is restricted to this ephemeral database's published loopback port.
    # Dedicated LOGIN tests still exercise actual PostgreSQL object privileges.
    command(["docker", "run", "--detach", "--name", name, "--label", f"{OWNERSHIP_LABEL}={token}",
             "--publish", "127.0.0.1::5432", "--env", f"POSTGRES_DB={database}",
             "--env", "POSTGRES_HOST_AUTH_METHOD=trust", "--tmpfs", "/var/lib/postgresql",
             POSTGRES_IMAGE])
    try:
        port = owned_container(name, token)
        wait_ready(name)
        command(["docker", "exec", name, "psql", "-U", "postgres", "-d", database,
                 "-v", "ON_ERROR_STOP=1", "-c",
                 f'REVOKE CREATE,TEMP ON DATABASE "{database}" FROM PUBLIC'])
        environment = {**os.environ,
                       "STAGE31_TEST_DATABASE_URL": f"postgres://postgres@127.0.0.1:{port}/{database}?sslmode=disable",
                       "ADMIN_HTTP_BROWSER_TEST": "1"}
        # Both packages share this PostgreSQL cluster and its installation roles.
        command(["go", "-C", "apps/api", "test", "-p=1", "-race", "-count=1", "-timeout=7m", "-run",
                 "^Test(Auth|Admin|Bootstrap|Domain|SessionPolicy)", "./internal/database", "./internal/adminintegration", "./cmd/db"],
                environment=environment, timeout=480)
        command(["go", "-C", "apps/api", "test", "-race", "-count=1", "./internal/adminhttp"],
                environment=environment)
    finally:
        owned_container(name, token)
        command(["docker", "container", "stop", name])
        command(["docker", "container", "rm", "--volumes", name])
    print("PASS: PostgreSQL auth/session/recovery/ACL and Chromium CSRF; disposable resources removed")


if __name__ == "__main__":
    main()
