"""Match the owner's temporary PR 43/44 exception to the main audit from run 44256665066."""

import hashlib
import json
import os
import re
import sys
from datetime import UTC, datetime
from pathlib import Path


# All three Alpine images have identical blocking package versions and locations.
ALPINE_FINDINGS = "fca1acae56cd90d35ad3eb93a43e00642ff7ebb8bb1445fe5ed75c9334c4947e"
ACCEPTED_BY_IMAGE = {
    "sp-postgres:stage0": ALPINE_FINDINGS,
    "sp-postgres-init:stage0": ALPINE_FINDINGS,
    "sp-quality-go:verify": ALPINE_FINDINGS,
    "sp-quality-npm:verify": "e2010ff81130a1bf1e000c3946b9f23ff29087fcf3051beb4847da99688a0802",
    "sp-quality-web:verify": "868bfc4a05359d4e4612cb6230441d9843acef7c3be24466feaacf018ba9b195",
}
EXPIRES_AT = datetime(2026, 11, 25, tzinfo=UTC)


def finding_identity(match):
    vulnerability = match["vulnerability"]
    package = match["artifact"]
    paths = sorted((loc["path"], loc["accessPath"]) for loc in package["locations"])
    fields = [
        vulnerability["id"], vulnerability["namespace"], vulnerability["severity"], vulnerability["fix"],
        package["type"], package["name"], package["version"], package["purl"], paths,
    ]
    return json.dumps(fields, sort_keys=True, separators=(",", ":"))


def accepted_report(image, report_path, now):
    if now >= EXPIRES_AT or image not in ACCEPTED_BY_IMAGE:
        return False
    # Code 2 is checked by the caller. Reject mixed threshold/operational diagnostics.
    if not re.fullmatch(r"\[\d+\] ERROR discovered vulnerabilities at or above the severity threshold",
                        report_path.with_suffix(".log").read_text().strip()):
        return False
    report = json.loads(report_path.read_bytes())
    if report["descriptor"]["db"]["status"]["valid"] is not True or image not in report["source"]["target"]["tags"]:
        return False
    selected = sorted(finding_identity(m) for m in report["matches"]
                      if m["vulnerability"]["severity"] in {"High", "Critical"})
    digest = hashlib.sha256("\n".join(selected).encode()).hexdigest()
    return digest == ACCEPTED_BY_IMAGE[image]


if __name__ == "__main__":
    authorized_pr = (
        os.environ.get("GITHUB_EVENT_NAME") == "pull_request"
        and os.environ.get("GITHUB_REPOSITORY") == "AshyWIngs/stepolonia"
        and os.environ.get("GITHUB_BASE_REF") == "main"
        and os.environ.get("GITHUB_REF") in {"refs/pull/43/merge", "refs/pull/44/merge"}
    )
    sys.exit(0 if authorized_pr and accepted_report(sys.argv[1], Path(sys.argv[2]), datetime.now(UTC)) else 1)
