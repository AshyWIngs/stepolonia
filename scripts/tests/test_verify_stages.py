"""Проверки эквивалентности поэтапного GitHub Verify локальному Make-графу."""

from pathlib import Path
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


if __name__ == "__main__":
    unittest.main()
