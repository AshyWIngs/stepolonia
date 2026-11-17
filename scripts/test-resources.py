#!/usr/bin/env python3
"""Check one disposable installation on a shared CPU without container swap."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import redirect_stdout
import hashlib
from http.client import HTTPConnection
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import secrets
import shutil
import time
from urllib.parse import urlsplit

from site_config import ROOT, compose_command, load_config, postgres_sql_args


SERVICES = ("caddy", "api", "web", "postgres")
MAX_RUNTIME_BYTES = 1216 * 1024 * 1024
TARGETS = (
    ("/fr/articles", "Bonjour de Stepolonia"),
    ("/fr/articles/stage15-fr-current", "Bonjour de Stepolonia"),
    ("/api/v1/site?lang=fr", '"settings"'),
    ("/api/v1/content/article?language=fr", "Bonjour de Stepolonia"),
)


def load_publishing():
    """Reuse the existing ownership guards and Go publication fixtures."""
    spec = importlib.util.spec_from_file_location("resource_publishing", Path(__file__).with_name("test-publishing.py"))
    if spec is None or spec.loader is None:
        raise ImportError("Cannot load the publishing test harness")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def inspect_runtime(harness, compose, token, log, *, allow_starting=False):
    identifiers = harness.command([*compose, "ps", "-q", *SERVICES], log, capture=True).split()
    items = json.loads(harness.command(["docker", "inspect", *identifiers], log, capture=True))
    found = {}
    for item in items:
        harness.verify_resource_owner(item, "container", "resource-test", token)
        service = item["Config"]["Labels"]["com.docker.compose.service"]
        found[service] = item
    log.write(json.dumps({"runtime_state": {
        name: {"id": item["Id"], "limits": {key: item["HostConfig"][key]
               for key in ("CpusetCpus", "Memory", "MemorySwap")},
               "running": item["State"]["Running"], "oom": item["State"].get("OOMKilled"),
               "health": item["State"].get("Health", {}).get("Status"), "restarts": item["RestartCount"]}
        for name, item in found.items()}}) + "\n")
    validate_runtime(found, allow_starting=allow_starting)
    return found


def validate_runtime(items, *, allow_starting=False):
    """Fail if an unconstrained or restarted process could invalidate the result."""
    if set(items) != set(SERVICES):
        raise RuntimeError("Expected exactly four runtime services")
    total = 0
    for item in items.values():
        limits, state = item["HostConfig"], item["State"]
        memory = limits["Memory"]
        if limits["CpusetCpus"] != "0" or memory <= 0 or limits["MemorySwap"] != memory:
            raise RuntimeError("All services must share CPU 0 and have RAM limits without swap")
        allowed_health = ("healthy", "starting") if allow_starting else ("healthy",)
        if (not state["Running"] or state.get("OOMKilled") or item["RestartCount"] != 0
                or state.get("Health", {}).get("Status") not in allowed_health):
            raise RuntimeError("Runtime service is unhealthy, OOM-killed or restarted")
        total += memory
    if total > MAX_RUNTIME_BYTES:
        raise RuntimeError("Runtime RAM limits exceed the 1216 MiB budget")


def wait_healthy(harness, compose, token, log):
    """Observe recovery without starting or replacing a failed container."""
    deadline = time.monotonic() + 60
    while True:
        items = inspect_runtime(harness, compose, token, log, allow_starting=True)
        if all(item["State"]["Health"]["Status"] == "healthy" for item in items.values()):
            return items
        if time.monotonic() >= deadline:
            raise RuntimeError("Docker health did not recover within 60 seconds")
        time.sleep(1)


def memory_bytes(text):
    match = re.fullmatch(r"([0-9.]+)(B|kB|KB|MB|GB|KiB|MiB|GiB)", text.strip())
    if match is None:
        raise ValueError(f"Unsupported Docker memory value: {text}")
    units = {"B": 1, "kB": 1000, "KB": 1000, "MB": 1000**2, "GB": 1000**3,
             "KiB": 1024, "MiB": 1024**2, "GiB": 1024**3}
    return round(float(match[1]) * units[match[2]])


def sample(harness, items, log):
    ids = [item["Id"] for item in items.values()]
    output = harness.command(["docker", "stats", "--no-stream", "--format", "{{json .}}", *ids], log, capture=True)
    readings = [json.loads(line) for line in output.splitlines()]
    if len(readings) != len(SERVICES):
        raise RuntimeError("Missing container resource readings")
    return {"elapsed_clock": time.monotonic(), "containers": readings,
            "total_memory_bytes": sum(memory_bytes(item["MemUsage"].split("/")[0]) for item in readings),
            "total_cpu_percent": sum(float(item["CPUPerc"].rstrip("%")) for item in readings)}


def load_worker(base, duration, offset):
    connection = HTTPConnection("127.0.0.1", urlsplit(base).port, timeout=5)
    try:
        return request_loop(connection, duration, offset)
    finally:
        connection.close()


def request_loop(connection, duration, offset):
    """Reuse HTTP connections like a browser; count transport failures without retries."""
    deadline = time.monotonic() + duration
    observations = []
    index = offset
    while time.monotonic() < deadline:
        path, expected = TARGETS[index % len(TARGETS)]
        started = time.monotonic()
        try:
            connection.request("GET", path)
            response = connection.getresponse()
            status, body = response.status, response.read().decode()
            valid = status == 200 and expected in body
            error = None if valid else f"Unexpected response: HTTP {status}"
        except OSError as failure:
            error = type(failure).__name__
            connection.close()
        observations.append({"path": path, "seconds": time.monotonic() - started, "error": error})
        index += 1
    return observations


def summarize(observations):
    """Use nearest-rank p95; report latency separately for each request class."""
    result = {}
    for path, _ in TARGETS:
        rows = [item for item in observations if item["path"] == path]
        timings = sorted(item["seconds"] for item in rows)
        result[path] = {"requests": len(rows), "errors": sum(item["error"] is not None for item in rows),
                        "p95_ms": round(timings[math.ceil(len(timings) * 0.95) - 1] * 1000, 2) if timings else None}
    return result


def run_load(harness, base, items, duration, concurrency, log):
    samples = []
    started = time.monotonic()
    with ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = [executor.submit(load_worker, base, duration, index) for index in range(concurrency)]
        while not all(future.done() for future in futures):
            samples.append(sample(harness, items, log))
        observations = [row for future in futures for row in future.result()]
    elapsed = time.monotonic() - started
    result = {"duration_seconds": elapsed, "concurrency": concurrency,
              "requests": len(observations), "requests_per_second": len(observations) / elapsed,
              "paths": summarize(observations), "samples": samples, "errors": [row for row in observations if row["error"]]}
    log.write(json.dumps(result) + "\n")
    return result


def configure(harness, site, project, token):
    directory, override, log_path, volume = harness.prepare_installation(
        site, project, f"stage12_{token}", token, runtime=True)
    configuration = json.loads(override.read_text())
    for service in configuration["services"].values():
        service["cpuset"] = "0"
    override.write_text(json.dumps(configuration))
    compose = [*compose_command(site), "-f", str(override), "--project-name", project]
    return directory, override, log_path, volume, compose


def prepare_database(harness, site, directory, override, compose, token, config, log):
    harness.command([*compose, "up", "-d", "--wait", "--wait-timeout", "180", "postgres"], log)
    database = config["services"]["postgres"]["environment"]["POSTGRES_DB"]
    query = f'COMMENT ON DATABASE "{database}" IS \'stepolonia:disposable:{token}\''
    harness.command([*compose, *postgres_sql_args(config, query)], log)
    for arguments in (["prepare-roles"], ["migrate", "up"], ["seed"]):
        harness.run_tool(site, arguments, override=override, log=log)
    csv = directory / "languages.csv"
    csv.write_text("code,name,native_name,direction\nfr,French,Français,ltr\n", encoding="utf-8")
    harness.run_tool(site, ["import-languages", "/input/languages.csv"], input_file=csv, override=override, log=log)
    port = harness.published_port(compose, "postgres", 5432, token, log)
    return {**os.environ, "DB_DISPOSABLE_TOKEN": token,
            "STAGE12_TEST_DATABASE_URL": f"postgres://stage12_owner@127.0.0.1:{port}/{database}?sslmode=disable",
            "STAGE12_TEST_PASSWORD_FILE": str(directory / ".secrets/db_owner_password"),
            "STAGE12_TEST_RUNTIME_PASSWORD_FILE": str(directory / ".secrets/db_app_password")}


def exercise(harness, compose, environment, project, token, options, log):
    harness.command([*compose, "up", "-d", "--wait", "--wait-timeout", "180", "caddy"], log)
    base = f"http://127.0.0.1:{harness.published_port(compose, 'caddy', 8080, token, log)}"
    harness.command(["go", "test", "-count=1", "-run", "^TestContentRuntimeFixture$", "./internal/content"],
                    log, env={**environment, "STAGE32_CONTENT_FIXTURE": "primary"}, cwd=ROOT / "apps/api")
    harness.publish_site_fixture(environment, "primary", log)
    harness.smoke.COMPOSE = compose
    with redirect_stdout(log):
        harness.smoke.check_publishing(base)
    items = inspect_runtime(harness, compose, token, log)
    result = run_load(harness, base, items, options.seconds, options.concurrency, log)
    inspect_runtime(harness, compose, token, log)
    if result["errors"] or any(row["requests"] == 0 for row in result["paths"].values()):
        raise RuntimeError("Resource workload returned errors or missed a request class")
    before_status, _, before = harness.smoke.request(base + "/api/v1/site")
    harness.publish_site_fixture(environment, "primary", log)
    after_status, _, after = harness.smoke.request(base + "/api/v1/site")
    if before_status != 200 or after_status != 200 or json.loads(before)["version"] == json.loads(after)["version"]:
        raise RuntimeError("Republishing did not produce a new public site snapshot")
    with redirect_stdout(log):
        harness.verify_owned(project, token, log)
        harness.smoke.check_publishing_recovery(base)
    # HTTP readiness can recover before Docker's next healthcheck interval.
    final = wait_healthy(harness, compose, token, log)
    if any(final[name]["Id"] != item["Id"] for name, item in items.items()):
        raise RuntimeError("Recovery replaced a runtime container")
    result["images"] = {name: item["Image"] for name, item in final.items()}
    return result


def main(options):
    harness = load_publishing()
    token = secrets.token_hex(6)
    project, site = f"sp-resource-{token}", f"resource-{token}.test"
    directory, override, log_path, volume, compose = configure(harness, site, project, token)
    result_path = log_path.with_suffix(".json")
    allocated = False
    result = {"status": "FAIL", "whole_linux_host": "NOT RUN", "auth": "NOT RUN"}
    result["harness_sha256"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    print(f"Resource test: {site}; log: {log_path}", flush=True)
    try:
        with log_path.open("w") as log:
            result["base_sha"] = harness.command(["git", "rev-parse", "HEAD"], log, capture=True)
            result["compose_sha256"] = hashlib.sha256((ROOT / "infrastructure/compose.yaml").read_bytes()).hexdigest()
            harness.command(["python3", "scripts/setup-local.py", "--site", site], log)
            config = load_config(site, override=override)
            harness.verify_owned(project, token, log, volume=volume)
            allocated = True
            harness.command([*compose, "build", "postgres-init", "postgres", "db-tools", *SERVICES[:3]], log)
            environment = prepare_database(harness, site, directory, override, compose, token, config, log)
            result.update(exercise(harness, compose, environment, project, token, options, log))
            result["status"] = "PASS"
    finally:
        with log_path.open("a") as log:
            if allocated:
                try:
                    harness.command([*compose, "logs", "--no-color"], log)
                finally:
                    harness.verify_owned(project, token, log, volume=volume)
                    harness.command([*compose, "down", "--volumes", "--remove-orphans"], log)
        shutil.rmtree(directory)
        override.unlink()
        result_path.write_text(json.dumps(result, indent=2) + "\n")
    print(f"PASS: constrained containers; whole Linux host/auth NOT RUN. Results: {result_path}", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seconds", type=int, default=60)
    parser.add_argument("--concurrency", type=int, default=4)
    args = parser.parse_args()
    if not 10 <= args.seconds <= 3600 or not 1 <= args.concurrency <= 32:
        parser.error("Require 10–3600 seconds and 1–32 concurrent clients")
    main(args)
