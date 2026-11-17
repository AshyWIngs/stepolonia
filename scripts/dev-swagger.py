#!/usr/bin/env python3
"""Serve local Swagger on an owned disposable PostgreSQL until interrupted."""
import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
OWNERSHIP_LABEL = "io.stepolonia.dev-swagger"
POSTGRES_IMAGE = "postgres:18.7-alpine3.24"


def command(arguments, *, capture=False):
    """Run fixed arguments without a shell."""
    result = subprocess.run(arguments, cwd=ROOT, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None, timeout=300)
    return result.stdout.strip() if capture else ""


def inspect_owned_container(name, token):
    """Return the exact owned container, including one left partially allocated."""
    result = subprocess.run(["docker", "container", "inspect", name], cwd=ROOT,
                            check=False, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=300)
    missing_messages = (f"Error response from daemon: No such container: {name}",
                        f"Error: No such container: {name}")
    if result.returncode:
        if result.stdout.strip() == "[]" and result.stderr.strip() in missing_messages:
            return None
        raise RuntimeError(f"Cannot inspect disposable Swagger container: {result.stderr.strip()}")
    items = json.loads(result.stdout)
    if (len(items) != 1 or items[0].get("Name") != f"/{name}"
            or items[0]["Config"].get("Labels", {}).get(OWNERSHIP_LABEL) != token):
        raise RuntimeError("Disposable Swagger container ownership changed")
    return items[0]


def owned_container(name, token):
    """Verify ownership and loopback binding before connecting to the database."""
    item = inspect_owned_container(name, token)
    if item is None:
        raise RuntimeError("Disposable Swagger container disappeared before startup")
    bindings = item["NetworkSettings"]["Ports"].get("5432/tcp") or []
    if len(bindings) != 1 or bindings[0]["HostIp"] != "127.0.0.1":
        raise RuntimeError("Disposable Swagger PostgreSQL must be loopback-only")
    return int(bindings[0]["HostPort"])


def cleanup_container(name, token):
    """Remove only the immutable ID whose exact name and ownership were verified."""
    item = inspect_owned_container(name, token)
    if item is not None:
        command(["docker", "container", "rm", "--force", "--volumes", item["Id"]])


def wait_ready(name):
    """Wait up to sixty seconds for the owned PostgreSQL."""
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = subprocess.run(["docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"],
                                cwd=ROOT, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, check=False, timeout=5)
        if result.returncode == 0:
            return
        time.sleep(0.2)
    raise RuntimeError("Disposable Swagger PostgreSQL did not become ready")


def interrupt(_signal, _frame):
    """Route termination through the same owned-resource cleanup as Ctrl-C."""
    raise KeyboardInterrupt


def main():
    """Build before allocation, then own both process and database lifetime."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=8080)
    options = parser.parse_args()
    if not 1 <= options.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    signal.signal(signal.SIGTERM, interrupt)
    token = secrets.token_hex(8)
    name, database = f"sp-swagger-{token}", f"swagger_{token}"
    child = None
    allocation_attempted = False
    with tempfile.TemporaryDirectory(prefix="stepolonia-swagger-") as directory:
        binary = str(Path(directory) / "dev-swagger")
        command(["go", "-C", "apps/api", "build", "-o", binary, "./cmd/dev-swagger"])
        try:
            # Trust is confined to this disposable, loopback-only database.
            allocation_attempted = True
            command(["docker", "run", "--detach", "--name", name, "--label", f"{OWNERSHIP_LABEL}={token}",
                     "--publish", "127.0.0.1::5432", "--env", f"POSTGRES_DB={database}",
                     "--env", "POSTGRES_HOST_AUTH_METHOD=trust", "--tmpfs", "/var/lib/postgresql", POSTGRES_IMAGE])
            port = owned_container(name, token)
            wait_ready(name)
            environment = {**os.environ,
                           "DEV_SWAGGER_DATABASE_URL": f"postgres://postgres@127.0.0.1:{port}/{database}?sslmode=disable",
                           "DEV_SWAGGER_TOKEN": token,
                           "DEV_SWAGGER_PORT": str(options.port)}
            child = subprocess.Popen([binary], cwd=ROOT, env=environment, start_new_session=True)
            try:
                status = child.wait()
            except KeyboardInterrupt:
                child.send_signal(signal.SIGTERM)
                status = child.wait(timeout=15)
            if status:
                raise RuntimeError(f"Swagger API exited with status {status}")
        finally:
            if child is not None and child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=5)
            if allocation_attempted:
                cleanup_container(name, token)


if __name__ == "__main__":
    main()
