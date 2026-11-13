#!/usr/bin/env python3
"""Run explicit database maintenance for one selected installation."""
import argparse
from pathlib import Path
import subprocess

from site_config import ROOT, compose_command, load_config


def run_tool(site, arguments, *, input_file=None, override=None, log=None):
    load_config(site)
    command = compose_command(site)
    if override is not None:
        command += ["-f", str(override)]
    command += ["run", "--build", "--rm", "--no-deps", "-T"]
    if input_file is not None:
        path = Path(input_file).resolve(strict=True)
        if not path.is_file():
            raise ValueError("Файл импорта должен быть обычным файлом")
        command += ["--volume", f"{path}:/input/languages.csv:ro"]
    command += ["db-tools", *arguments]
    subprocess.run(command, cwd=ROOT, check=True, stdout=log, stderr=log)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site", required=True)
    commands = parser.add_subparsers(dest="command", required=True)
    migrate = commands.add_parser("migrate")
    migrate.add_argument("action", choices=("up", "down", "status"))
    commands.add_parser("seed")
    commands.add_parser("prepare-roles")
    importer = commands.add_parser("import-languages")
    importer.add_argument("file")
    args = parser.parse_args()
    if args.command == "migrate":
        if args.action == "up":
            run_tool(args.site, ["prepare-roles"])
        run_tool(args.site, ["migrate", args.action])
    elif args.command == "import-languages":
        run_tool(args.site, ["import-languages", "/input/languages.csv"], input_file=args.file)
    else:
        run_tool(args.site, [args.command])


if __name__ == "__main__":
    main()
