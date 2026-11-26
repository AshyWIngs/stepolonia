#!/usr/bin/env python3
"""Report migration size and PostgreSQL statements from the SQLFluff parse tree."""

from collections import Counter
import json
from pathlib import Path
import sys


COUNTS = {
    "tables": "create_table_statement",
    "views": "create_view_statement",
    "functions": "create_function_statement",
    "triggers": "create_trigger",
    "do blocks": "do_statement",
}


def main():
    """Count top-level Up and Down statements without parsing literal text as SQL."""
    files = sorted(json.load(sys.stdin), key=lambda file: file["filepath"])
    if not files:
        raise RuntimeError("No SQL migration files were parsed")

    print(f"{'Migration':40} {'Lines':>6} {'Stmts':>6}", *(f"{name:>10}" for name in COUNTS))
    for file in files:
        path = Path(file["filepath"])
        statements = [node["statement"] for node in file["segments"]["file"] if "statement" in node]
        kinds = Counter(kind for statement in statements for kind in statement)
        lines = len(path.read_text(encoding="utf-8").splitlines())
        counts = (f"{kinds[kind]:10}" for kind in COUNTS.values())
        print(f"{path.name:40} {lines:6} {len(statements):6}", *counts)


if __name__ == "__main__":
    main()
