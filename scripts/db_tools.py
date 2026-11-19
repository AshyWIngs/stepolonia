#!/usr/bin/env python3
"""Run explicit database maintenance for one selected installation."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import subprocess

from site_config import ROOT, compose_command, load_config, site_directory


DISPOSABLE_MARKER = ".disposable.json"


def disposable_marker(site):
    """Require an owner-only marker before contacting Docker."""
    directory_fd = os.open(site_directory(site), os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fd = os.open(DISPOSABLE_MARKER, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                     dir_fd=directory_fd)
    finally:
        os.close(directory_fd)
    with os.fdopen(fd, "r") as source:
        metadata = os.fstat(source.fileno())
        if (not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.geteuid()
                or stat.S_IMODE(metadata.st_mode) != 0o600 or metadata.st_nlink != 1):
            raise ValueError("Down refused: marker must be an owner-only file with mode 0600")
        if metadata.st_size > 4096:
            raise ValueError("Down refused: disposable installation marker is too large")
        marker = json.loads(source.read(4097))
    if (not isinstance(marker, dict)
            or any(not isinstance(marker.get(key), str) for key in ("token", "database", "project", "volume"))
            or not re.fullmatch(r"[0-9a-f]{12,64}", marker["token"])
            or not marker["database"].startswith("stage12_")):
        raise ValueError("Down refused: invalid disposable installation marker")
    return marker


def run_tool(site, arguments, *, input_file=None, override=None, log=None, allow_disposable_down=False,
             build_image=True):
    marker = None
    if list(arguments) == ["migrate", "down"]:
        if not allow_disposable_down:
            raise ValueError("Down requires an explicit --disposable option and a disposable database")
        marker = disposable_marker(site)
    # Include the tools profile when reading config. The run command selects the service.
    config = load_config(site, override=override, profile="tools")
    environment = None
    if marker is not None:
        database = config["services"]["postgres"]["environment"]["POSTGRES_DB"]
        volume = config["volumes"]["postgres_data"]
        if (marker["database"] != database
                or config["services"]["db-tools"]["environment"]["DB_NAME"] != database
                or marker["project"] != config["name"]
                or marker["volume"] != volume["name"] or volume.get("external", False)):
            raise ValueError("Down refused: marker does not match the database, project or volume")
        environment = dict(os.environ, DB_DISPOSABLE_TOKEN=marker["token"])
    command = compose_command(site)
    if override is not None:
        command += ["-f", str(override)]
    command.append("run")
    command.extend(["--build"] if build_image else ["--pull", "never"])
    command += ["--rm", "--no-deps", "-T"]
    if marker is not None:
        command += ["--env", "DB_DISPOSABLE_TOKEN"]
    if input_file is not None:
        path = Path(input_file).resolve(strict=True)
        if not path.is_file():
            raise ValueError("The import source must be a regular file")
        command += ["--volume", f"{path}:/input/languages.csv:ro"]
    command += ["db-tools", *arguments]
    subprocess.run(command, cwd=ROOT, check=True, stdout=log, stderr=log, env=environment)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", required=True)
    commands = parser.add_subparsers(dest="command", required=True)
    migrate = commands.add_parser("migrate")
    migrate.add_argument("action", choices=("up", "down", "status"))
    migrate.add_argument("--disposable", action="store_true",
                         help="allow Down for a marked disposable installation")
    commands.add_parser("prepare-roles")
    initializer = commands.add_parser("initialize-site")
    initializer.add_argument("title")
    initializer.add_argument("language")
    importer = commands.add_parser("import-languages")
    importer.add_argument("file")
    args = parser.parse_args()
    if args.command == "migrate":
        if args.action == "up":
            run_tool(args.site, ["prepare-roles"])
        run_tool(args.site, ["migrate", args.action], allow_disposable_down=args.disposable)
    elif args.command == "import-languages":
        run_tool(args.site, ["import-languages", "/input/languages.csv"], input_file=args.file)
    elif args.command == "initialize-site":
        run_tool(args.site, ["initialize-site", args.title, args.language])
    else:
        run_tool(args.site, [args.command])


if __name__ == "__main__":
    main()
