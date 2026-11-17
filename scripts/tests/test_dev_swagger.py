"""Failure-injection checks for disposable Swagger allocation cleanup."""
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / "dev-swagger.py"
SPEC = importlib.util.spec_from_file_location("dev_swagger", SCRIPT)
swagger = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(swagger)

TOKEN = "0123456789abcdef"
NAME = f"sp-swagger-{TOKEN}"
CONTAINER_ID = "a" * 64


class AllocationFailureTests(unittest.TestCase):
    def run_failed_allocation(self, inspect_result):
        """Simulate Docker creating a container before the client times out."""
        calls = []

        def run(arguments, **_options):
            calls.append(arguments)
            if arguments[:2] == ["docker", "run"]:
                # Daemon state is represented by the following inspect response.
                raise subprocess.TimeoutExpired(arguments, 300)
            if arguments[:3] == ["docker", "container", "inspect"]:
                self.assertEqual(arguments[3], NAME)
                return inspect_result
            return subprocess.CompletedProcess(arguments, 0, stdout="", stderr="")

        with (patch.object(swagger.subprocess, "run", side_effect=run),
              patch.object(swagger.secrets, "token_hex", return_value=TOKEN),
              patch.object(swagger.signal, "signal"),
              patch("sys.argv", [str(SCRIPT)])):
            try:
                swagger.main()
            except (subprocess.TimeoutExpired, RuntimeError) as error:
                return calls, error
        self.fail("allocation failure was hidden")

    def test_create_then_timeout_removes_owned_unstarted_container(self):
        # No network bindings exist yet. Cleanup must still remove this resource.
        item = {"Id": CONTAINER_ID, "Name": f"/{NAME}",
                "Config": {"Labels": {swagger.LABEL: TOKEN}}, "NetworkSettings": {}}
        result = subprocess.CompletedProcess([], 0, stdout=json.dumps([item]), stderr="")
        calls, error = self.run_failed_allocation(result)
        self.assertIsInstance(error, subprocess.TimeoutExpired)
        self.assertIn(["docker", "container", "rm", "--force", "--volumes", CONTAINER_ID], calls)
        self.assertEqual(sum(call[:3] == ["docker", "container", "rm"] for call in calls), 1)

    def test_foreign_container_survives_failed_allocation(self):
        item = {"Id": CONTAINER_ID, "Name": f"/{NAME}",
                "Config": {"Labels": {swagger.LABEL: "foreign-owner"}}}
        result = subprocess.CompletedProcess([], 0, stdout=json.dumps([item]), stderr="")
        calls, error = self.run_failed_allocation(result)
        self.assertIsInstance(error, RuntimeError)
        self.assertIn("ownership changed", str(error))
        self.assertFalse(any(call[:3] == ["docker", "container", "rm"] for call in calls))

    def test_missing_container_preserves_original_timeout(self):
        result = subprocess.CompletedProcess([], 1, stdout="[]\n",
                                             stderr=f"Error response from daemon: No such container: {NAME}\n")
        calls, error = self.run_failed_allocation(result)
        self.assertIsInstance(error, subprocess.TimeoutExpired)
        self.assertFalse(any(call[:3] == ["docker", "container", "rm"] for call in calls))

    def test_daemon_inspection_failure_is_reported_without_deletion(self):
        result = subprocess.CompletedProcess([], 1, stdout="", stderr="permission denied")
        calls, error = self.run_failed_allocation(result)
        self.assertIsInstance(error, RuntimeError)
        self.assertIn("permission denied", str(error))
        self.assertFalse(any(call[:3] == ["docker", "container", "rm"] for call in calls))


if __name__ == "__main__":
    unittest.main()
