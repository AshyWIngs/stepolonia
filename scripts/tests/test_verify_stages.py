"""Check parity between GitHub Verify and local Make targets."""

from pathlib import Path
from fnmatch import fnmatch
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
STAGED_TARGETS = (
    "lint-go", "lint-python", "lint-web", "lint-sql", "duplicates", "sdk-check",
    "setup", "ci-config", "docker-check", "python-tests", "api-checks", "web-checks",
    "ci-service-checks", "ci-build", "ci-db-test", "ci-publishing-test", "auth-test", "ci-image-audit",
)
WRAPPERS = {
    "config": ("setup", "ci-config"),
    "build": ("setup", "ci-build"),
    "test": ("setup docker-check python-tests api-checks web-checks", "ci-service-checks"),
    "db-test": ("sdk-check", "ci-db-test"),
    "publishing-test": ("sdk-check", "ci-publishing-test"),
    "image-audit": ("lint build", "ci-image-audit"),
}


class VerifyStagesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        cls.workflow = (ROOT / ".github/workflows/verify.yml").read_text(encoding="utf-8")

    def test_stages_cover_exact_targets_in_order(self):
        targets = re.findall(r"^        run: make SITE=ci\.test ([\w-]+)$",
                             self.workflow, flags=re.MULTILINE)
        self.assertEqual(targets, list(STAGED_TARGETS))
        self.assertEqual(len(targets), len(set(targets)))

    def test_developer_verify_retains_its_prerequisites(self):
        self.assertRegex(self.makefile,
                         r"(?m)^verify: lint config test build db-test publishing-test auth-test image-audit$")

    def test_wrappers_preserve_dependencies_and_call_same_recipe(self):
        for target, (dependencies, runner) in WRAPPERS.items():
            with self.subTest(target=target):
                line = f"{target}: {dependencies}\n\t$(MAKE) --no-print-directory {runner}"
                self.assertIn(line, self.makefile)
                self.assertRegex(self.makefile, rf"(?m)^{re.escape(runner)}:$")

    def test_all_exec_targets_are_phony(self):
        phony = next(line for line in self.makefile.splitlines()
                     if line.startswith(".PHONY:"))
        for target in STAGED_TARGETS:
            with self.subTest(target=target):
                self.assertIn(target, phony.split())

    def test_cve_gate_and_public_status_are_not_weakened(self):
        self.assertIn("--fail-on high", self.makefile)
        self.assertIn("sp-caddy:stage0", self.makefile)
        self.assertRegex(self.workflow, r"(?m)^  verify:$")
        self.assertIn("permissions:\n  contents: read", self.workflow)
        self.assertNotIn("continue-on-error:", self.workflow)

    def test_failure_diagnostics_are_uploaded_even_after_a_failed_gate(self):
        upload = self.workflow.split('      - name: "Artifacts:', 1)[1]
        self.assertIn("if: ${{ always() && steps.scope.outputs.full == 'true' }}", upload)
        paths = re.findall(r"^            (reports/\S+)$", upload, flags=re.MULTILINE)
        expected = (
            "reports/go-lint.log", "reports/python-lint.log", "reports/web-lint.log",
            "reports/python-checks.log", "reports/api-checks.log", "reports/web-checks.log",
            "reports/caddy-checks.log", "reports/postgres-checks.log",
            "reports/api-docker-check.log", "reports/web-docker-check.log",
            "reports/caddy-docker-check.log", "reports/postgres-docker-check.log",
            "reports/quality-docker-check.log", "reports/duplicates.log",
            "reports/publishing-failed.log", "reports/images/failed.json",
            "reports/images/failed.identity", "reports/images/failed.log",
            "reports/npm-audit.json", "reports/npm-audit.exit", "reports/npm-audit.stderr",
        )
        for path in expected:
            with self.subTest(path=path):
                self.assertTrue(any(fnmatch(path, pattern) for pattern in paths), path)
        self.assertFalse(any(fnmatch("reports/images/image.tar", pattern) for pattern in paths))

    def test_external_cache_materializes_only_dependency_stages(self):
        bake = (ROOT / "infrastructure/quality/dependencies.hcl").read_text(encoding="utf-8")
        expected = {
            "go-modules": ("infrastructure/quality/Dockerfile", "go-cache-ready", "go-dependencies"),
            "web-dependencies": ("apps/web/Dockerfile", "cache-ready", "dependencies"),
            "npm-tools": ("infrastructure/quality/Dockerfile", "npm-cache-ready", "duplicates-build"),
            "api-modules": ("apps/api/Dockerfile", "cache-ready", "dependencies"),
            "postgres-distribution": ("infrastructure/postgres/Dockerfile", "cache-ready", "distribution"),
        }
        actual = set(re.findall(r'^target "([^_][^"]*)"', bake, flags=re.MULTILINE))
        self.assertEqual(actual, set(expected))
        for target, (dockerfile, stage, parent) in expected.items():
            with self.subTest(target=target):
                definition = re.search(rf'^target "{target}" {{(.*?)^}}', bake,
                                       flags=re.MULTILINE | re.DOTALL).group(1)
                self.assertIn(f'target = "{stage}"', definition)
                self.assertIn(f'no-cache-filter = ["{stage}"]', definition)
                source = (ROOT / dockerfile).read_text(encoding="utf-8")
                readiness = re.search(rf'^FROM {parent} AS {stage}\n(.*?)(?=^FROM |\Z)', source,
                                      flags=re.MULTILINE | re.DOTALL).group(1)
                self.assertRegex(readiness, r"(?m)^RUN test ")
                self.assertNotRegex(readiness, r"(?m)^(?:COPY|ADD) ")
                self.assertNotRegex(readiness, r"(?:go test|npm run|golangci-lint run|/ruff.*check)")


if __name__ == "__main__":
    unittest.main()
