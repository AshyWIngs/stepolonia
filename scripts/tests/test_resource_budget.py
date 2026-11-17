"""Reject resource evidence collected with missing or ineffective limits."""
from copy import deepcopy
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


def load_harness():
    scripts = Path(__file__).resolve().parents[1]
    sys.path.insert(0, str(scripts))
    spec = importlib.util.spec_from_file_location("test_resources", scripts / "test-resources.py")
    if spec is None or spec.loader is None:
        raise ImportError("Cannot load the resource harness")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


resources = load_harness()


class ResourceEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.containers = {
            name: {"HostConfig": {"CpusetCpus": "0", "Memory": mib * 1024**2,
                                  "MemorySwap": mib * 1024**2},
                   "State": {"Running": True, "OOMKilled": False, "Health": {"Status": "healthy"}},
                   "RestartCount": 0}
            for name, mib in (("api", 256), ("web", 384), ("postgres", 512), ("caddy", 64))
        }

    def test_accepts_exact_budget(self):
        resources.validate_runtime(self.containers)

    def test_rejects_larger_total_and_hidden_swap(self):
        for field, value in (("Memory", 513 * 1024**2), ("MemorySwap", -1),
                             ("CpusetCpus", "0-1")):
            with self.subTest(field=field):
                containers = deepcopy(self.containers)
                containers["postgres"]["HostConfig"][field] = value
                if field == "Memory":
                    containers["postgres"]["HostConfig"]["MemorySwap"] = value
                with self.assertRaises(RuntimeError):
                    resources.validate_runtime(containers)

    def test_rejects_missing_restarted_or_unhealthy_service(self):
        cases = [deepcopy(self.containers) for _ in range(4)]
        del cases[0]["web"]
        cases[1]["web"]["RestartCount"] = 1
        cases[2]["web"]["State"]["OOMKilled"] = True
        cases[3]["web"]["State"]["Health"]["Status"] = "unhealthy"
        for containers in cases:
            with self.subTest(containers=containers), self.assertRaises(RuntimeError):
                resources.validate_runtime(containers)

    def test_health_wait_only_tolerates_starting_running_services(self):
        self.containers["api"]["State"]["Health"]["Status"] = "starting"
        with self.assertRaises(RuntimeError):
            resources.validate_runtime(self.containers)
        resources.validate_runtime(self.containers, allow_starting=True)
        self.containers["api"]["State"]["Running"] = False
        with self.assertRaises(RuntimeError):
            resources.validate_runtime(self.containers, allow_starting=True)

    def test_docker_memory_units_and_nearest_rank_latency(self):
        self.assertEqual(resources.memory_bytes("1.5MiB"), 1572864)
        self.assertEqual(resources.memory_bytes("2MB"), 2000000)
        with self.assertRaises(ValueError):
            resources.memory_bytes("unavailable")
        rows = [{"path": "/fr/articles", "seconds": index / 1000, "error": None}
                for index in range(1, 21)]
        result = resources.summarize(rows)
        self.assertEqual(result["/fr/articles"]["p95_ms"], 19)
        self.assertEqual(result["/api/v1/site?lang=fr"]["requests"], 0)

    def test_configuration_uses_generated_services_and_preserves_ownership(self):
        harness = resources.load_publishing()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            (root / "sites").mkdir()
            with patch.object(harness, "ROOT", root), patch.object(harness, "site_directory", return_value=root / "sites/resource.test"):
                _, override, _, volume, _ = resources.configure(harness, "resource.test", "sp-resource-fixture", "a" * 12)
            configuration = json.loads(override.read_text())
        self.assertEqual(set(configuration["services"]), {"api", "web", "caddy", "postgres", "postgres-init", "db-tools"})
        for name, service in configuration["services"].items():
            with self.subTest(service=name):
                self.assertEqual(service["cpuset"], "0")
                self.assertEqual(service["labels"][harness.OWNERSHIP_LABEL], "a" * 12)
                self.assertTrue(service["image"].startswith("sp-resource-fixture-"))
        self.assertEqual(configuration["volumes"]["postgres_data"]["name"], volume)
        self.assertEqual(configuration["services"]["postgres"]["ports"][0]["host_ip"], "127.0.0.1")


if __name__ == "__main__":
    unittest.main()
