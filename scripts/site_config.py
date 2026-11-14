"""Common installation selection for local tooling; no secret values are read."""
from pathlib import Path
import argparse
import json
import os
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def site_directory(site):
    if len(site) > 253 or not re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*", site):
        raise ValueError("Site must contain lowercase letters, digits, dots or hyphens")
    path = ROOT / "sites" / site
    if path.resolve() != path:
        raise ValueError("Site configuration must not be a symlink")
    return path


def compose_command(site):
    path = site_directory(site)
    return ["docker", "compose", "--env-file", str(path / ".env"),
            "-f", str(ROOT / "infrastructure/compose.yaml")]


def load_config(site, *, override=None, profile=None):
    command = compose_command(site)
    if override is not None:
        command += ["-f", str(override)]
    if profile is not None:
        command += ["--profile", profile]
    command += ["config", "--format", "json"]
    result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True)
    if result.returncode:
        raise ValueError(f"Invalid Compose configuration for {site}: {result.stderr.strip()}")
    config = json.loads(result.stdout)
    expected = site_directory(site) / ".secrets"
    for secret in config["secrets"].values():
        if Path(secret["file"]).resolve().parent != expected:
            raise ValueError("SITE_ID must match the selected site; cross-site secrets are forbidden")
    if not config["name"].startswith("sp-"):
        raise ValueError("Installation COMPOSE_PROJECT_NAME must start with sp-")
    database = config["services"]["postgres"]["environment"]
    if database["DB_USER"] == database["POSTGRES_USER"]:
        raise ValueError("Runtime and owner database roles must differ")
    return config


def postgres_sql_args(config, query, *, runtime_role=False, database_name=None):
    """Local administration through psql directly; the runtime has no shell."""
    database = config["services"]["postgres"]["environment"]
    user = database["DB_USER" if runtime_role else "POSTGRES_USER"]
    return ["exec", "-T", "postgres", "/usr/libexec/postgresql18/psql",
            "-X", "--no-password", "--username=" + user,
            "--dbname=" + (database_name or database["POSTGRES_DB"]), "--set=ON_ERROR_STOP=1",
            "--tuples-only", "--no-align", "--command=" + query]


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run Compose for one validated installation")
    parser.add_argument("--site", required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.command:
        parser.error("a Compose command is required")
    load_config(args.site)
    os.execvp("docker", [*compose_command(args.site), *args.command])
