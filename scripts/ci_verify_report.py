#!/usr/bin/env python3
"""Summarize npm advisories and warnings in GitHub Actions."""

import argparse
import json
import os
from pathlib import Path
import sys

NPM_AUDIT_REPORT_PATH = Path("reports/npm-audit.json")
NPM_AUDIT_EXIT_PATH = Path("reports/npm-audit.exit")
NOT_RUN = "NOT RUN"

def group(message):
    if os.environ.get("GITHUB_ACTIONS") == "true":
        print(message, flush=True)


def audit_data():
    if not NPM_AUDIT_REPORT_PATH.is_file():
        return NOT_RUN, {}, [], "npm audit report is missing"
    try:
        payload = json.loads(NPM_AUDIT_REPORT_PATH.read_text(encoding="utf-8"))
    except (ValueError, OSError):
        return NOT_RUN, {}, [], "npm audit returned invalid JSON"
    if not isinstance(payload, dict) or payload.get("error"):
        code = (payload.get("error") or {}).get("code", "unknown") if isinstance(payload, dict) else "unknown"
        return NOT_RUN, {}, [], f"npm audit returned an error: {code}"
    vulnerabilities = payload.get("vulnerabilities")
    if not isinstance(vulnerabilities, dict) or not isinstance(payload.get("metadata", {}).get("vulnerabilities"), dict):
        return NOT_RUN, {}, [], "npm audit JSON is missing required fields"
    counts = payload["metadata"]["vulnerabilities"]
    items = []
    for pkg, detail in sorted(vulnerabilities.items()):
        if not isinstance(detail, dict):
            continue
        advisory_ids = sorted({str(via["source"]) for via in detail.get("via", [])
                               if isinstance(via, dict) and isinstance(via.get("source"), int)})
        items.append({"name": pkg, "severity": detail.get("severity", "unknown"),
                      "advisory_ids": advisory_ids})
    exit_status = NPM_AUDIT_EXIT_PATH.read_text(encoding="utf-8").strip() if NPM_AUDIT_EXIT_PATH.is_file() else "unknown"
    if exit_status not in ("0", "1"):
        return NOT_RUN, {}, [], f"npm audit returned an unexpected exit code: {exit_status}"
    return "REVIEW", counts, items, None


def append_audit_findings(lines, counts, findings):
    """Summarize npm findings for review."""
    levels = ("critical", "high", "moderate", "low", "info")
    lines.append("Packages with advisories by severity: " + ", ".join(
        f"{key}={counts.get(key, 0)}" for key in levels) + ".")
    if not findings:
        lines.append("No advisories found in the current lockfile.")
        return
    lines.extend(["", "| Package | Severity | Advisory ID | Status |", "|---|---|---|---|"])
    for item in findings[:30]:
        ids = ", ".join(item["advisory_ids"]) or "transitive dependency"
        lines.append(f"| {item['name']} | {item['severity']} | {ids} | OPEN: review required |")
    if len(findings) > 30:
        lines.append(f"\nSee the attached JSON for {len(findings) - 30} more packages.")
    group(f"::warning::npm audit: {len(findings)} packages with open advisories. See the job summary and npm-audit.json.")


def append_audit_summary(lines):
    """Report npm advisories alongside the image scan results."""
    _state, counts, findings, error = audit_data()
    lines.extend(["", "### npm audit: Nuxt dependencies (lockfile report)", ""])
    if error:
        lines.append(f"**{NOT_RUN}:** {error}. The advisory inventory could not be evaluated.")
        group(f"::warning::{error}")
    else:
        append_audit_findings(lines, counts, findings)
    lines.extend(["", "**Review:** The npm inventory does not change the Docker/Grype threshold. "
                  "LOW/MODERATE findings remain open for review. "
                  "Review HIGH/CRITICAL findings individually."])


def summarize():
    lines = ["## Project checks: npm advisories", ""]
    append_audit_summary(lines)
    summary = "\n".join(lines) + "\n"
    dest = os.getenv("GITHUB_STEP_SUMMARY")
    if dest:
        with open(dest, "a", encoding="utf-8") as writer:
            writer.write(summary)
    else:
        print(summary)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("summary", help="Write the GitHub Actions job summary")
    parser.parse_args()
    return summarize()


if __name__ == "__main__":
    sys.exit(main())
