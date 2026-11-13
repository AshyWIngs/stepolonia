"""Проверки границ файловой системы и запрета разрушительных операций до Docker."""
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
import db_tools
import site_config


def load_script(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), SCRIPTS / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


setup_local = load_script("setup-local")
publishing = load_script("test-publishing")


class SecretSafetyTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.foreign = self.root / "foreign"
        self.foreign.write_bytes(b"owned-test-fixture")
        self.foreign.chmod(0o640)
        self.site = self.root / "site"
        self.site.mkdir()

    def assert_foreign_unchanged(self):
        self.assertEqual(self.foreign.read_bytes(), b"owned-test-fixture")
        self.assertEqual(stat.S_IMODE(self.foreign.stat().st_mode), 0o640)

    def test_existing_credentials_survive_repeated_setup(self):
        setup_local.setup_secrets(self.site)
        directory = self.site / ".secrets"
        credentials = {}
        for name in ("db_owner_password", "db_app_password"):
            path = directory / name
            path.chmod(0o600)
            path.write_bytes(f"fixture-{name}".encode())
            credentials[name] = path.read_bytes()
        setup_local.setup_secrets(self.site)
        self.assertEqual(stat.S_IMODE(directory.stat().st_mode), 0o700)
        for name, expected in credentials.items():
            self.assertEqual((directory / name).read_bytes(), expected)
            self.assertEqual(stat.S_IMODE((directory / name).stat().st_mode), 0o444)

    def test_secret_directory_symlink_cannot_chmod_foreign_directory(self):
        foreign_directory = self.root / "foreign-directory"
        foreign_directory.mkdir(mode=0o750)
        (self.site / ".secrets").symlink_to(foreign_directory, target_is_directory=True)
        with self.assertRaises(OSError):
            setup_local.setup_secrets(self.site)
        self.assertEqual(stat.S_IMODE(foreign_directory.stat().st_mode), 0o750)
        self.assertEqual(list(foreign_directory.iterdir()), [])

    def test_secret_symlink_cannot_chmod_or_replace_foreign_file(self):
        directory = self.site / ".secrets"
        directory.mkdir()
        (directory / "db_owner_password").symlink_to(self.foreign)
        with self.assertRaises(OSError):
            setup_local.setup_secrets(self.site)
        self.assert_foreign_unchanged()

    def test_fifo_is_rejected_without_becoming_a_secret(self):
        directory = self.site / ".secrets"
        directory.mkdir()
        fifo = directory / "db_owner_password"
        os.mkfifo(fifo, 0o600)
        with self.assertRaises(ValueError):
            setup_local.setup_secrets(self.site)
        self.assertTrue(stat.S_ISFIFO(fifo.stat().st_mode))
        self.assertEqual(stat.S_IMODE(fifo.stat().st_mode), 0o600)

    def test_hardlink_cannot_chmod_foreign_file(self):
        directory = self.site / ".secrets"
        directory.mkdir()
        os.link(self.foreign, directory / "db_owner_password")
        with self.assertRaises(ValueError):
            setup_local.setup_secrets(self.site)
        self.assert_foreign_unchanged()

    def test_replacement_after_open_does_not_redirect_chmod(self):
        directory = self.site / ".secrets"
        directory.mkdir()
        path = directory / "db_owner_password"
        path.write_bytes(b"existing-credential")
        original_open = os.open

        def replace_after_open(name, flags, *args, **kwargs):
            fd = original_open(name, flags, *args, **kwargs)
            if name == "db_owner_password" and not flags & os.O_CREAT:
                path.rename(directory / "detached-credential")
                path.symlink_to(self.foreign)
            return fd

        with patch.object(setup_local.os, "open", side_effect=replace_after_open):
            setup_local.setup_secrets(self.site)
        self.assert_foreign_unchanged()
        self.assertEqual((directory / "detached-credential").read_bytes(), b"existing-credential")


class DisposableDownTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        self.site = "stage12-0123456789ab.test"
        self.directory = self.root / "sites" / self.site
        self.directory.mkdir(parents=True)
        self.marker = {"token": "0123456789ab", "database": "stage12_0123456789ab",
                       "project": "sp-stage12-0123456789ab", "volume": "sp-stage12-0123456789ab-data"}
        self.config = {"name": self.marker["project"],
                       "services": {"postgres": {"environment": {"POSTGRES_DB": self.marker["database"],
                                                                "POSTGRES_USER": "fixture_owner", "DB_USER": "fixture_app"}}},
                       "secrets": {"db_owner_password": {"file": str(self.directory / ".secrets/db_owner_password")}},
                       "volumes": {"postgres_data": {"name": self.marker["volume"], "external": False}}}
        root_patch = patch.object(site_config, "ROOT", self.root)
        root_patch.start()
        self.addCleanup(root_patch.stop)

    def write_marker(self):
        path = self.directory / db_tools.DISPOSABLE_MARKER
        with path.open("x") as output:
            json.dump(self.marker, output)
        path.chmod(0o600)
        return path

    def config_for_profile(self, site, *, override=None, profile=None):
        config = json.loads(json.dumps(self.config))
        if profile == "tools":
            config["services"]["db-tools"] = {
                "profiles": ["tools"], "environment": {"DB_NAME": self.marker["database"]}}
        return config

    def test_intent_and_marker_are_required_before_any_docker_call(self):
        with patch.object(db_tools.subprocess, "run") as run:
            with self.assertRaises(ValueError):
                db_tools.run_tool(self.site, ["migrate", "down"])
            with self.assertRaises(FileNotFoundError):
                db_tools.run_tool(self.site, ["migrate", "down"], allow_disposable_down=True)
            run.assert_not_called()

    def test_symlink_marker_is_rejected_before_any_docker_call(self):
        foreign = self.root / "foreign-marker"
        foreign.write_text(json.dumps(self.marker))
        foreign.chmod(0o600)
        (self.directory / db_tools.DISPOSABLE_MARKER).symlink_to(foreign)
        with patch.object(db_tools.subprocess, "run") as run:
            with self.assertRaises(OSError):
                db_tools.run_tool(self.site, ["migrate", "down"], allow_disposable_down=True)
            run.assert_not_called()

    def test_marker_mismatch_cannot_start_down(self):
        self.write_marker()
        self.config["volumes"]["postgres_data"]["name"] = "foreign-persistent-volume"
        with patch.object(db_tools, "load_config", side_effect=self.config_for_profile), \
                patch.object(db_tools.subprocess, "run") as run:
            with self.assertRaises(ValueError):
                db_tools.run_tool(self.site, ["migrate", "down"], allow_disposable_down=True)
            run.assert_not_called()

    def test_owned_down_passes_token_only_through_environment(self):
        self.write_marker()
        override = self.root / "own-test.compose.json"
        with patch.object(db_tools, "load_config", side_effect=self.config_for_profile) as load, \
                patch.object(db_tools.subprocess, "run") as run:
            db_tools.run_tool(self.site, ["migrate", "down"], override=override,
                              allow_disposable_down=True)
        load.assert_called_once_with(self.site, override=override, profile="tools")
        argv = run.call_args.args[0]
        self.assertEqual(argv[-3:], ["db-tools", "migrate", "down"])
        self.assertEqual(argv[argv.index("--env") + 1], "DB_DISPOSABLE_TOKEN")
        self.assertNotIn(self.marker["token"], argv)
        self.assertNotIn("DB_DISPOSABLE_TOKEN=" + self.marker["token"], argv)
        self.assertEqual(run.call_args.kwargs["env"]["DB_DISPOSABLE_TOKEN"], self.marker["token"])

    def test_config_profile_exposes_tools_without_starting_services(self):
        def compose_config(argv, **kwargs):
            self.assertEqual(argv[-3:], ["config", "--format", "json"])
            profile = argv[argv.index("--profile") + 1] if "--profile" in argv else None
            config = self.config_for_profile(self.site, profile=profile)
            return SimpleNamespace(returncode=0, stdout=json.dumps(config))

        with patch.object(site_config.subprocess, "run", side_effect=compose_config):
            normal = site_config.load_config(self.site)
            tools = site_config.load_config(self.site, profile="tools")
        self.assertNotIn("db-tools", normal["services"])
        self.assertIn("db-tools", tools["services"])

    def test_foreign_resource_label_stops_cleanup(self):
        inspected = json.dumps([{"Config": {"Labels": {publishing.LABEL: "foreign-owner"}}}])
        with patch.object(publishing, "command", side_effect=["own-name-collision", inspected]) as command:
            with self.assertRaisesRegex(RuntimeError, "Очистка запрещена"):
                publishing.verify_owned(self.marker["project"], self.marker["token"], io.StringIO())
        self.assertEqual(command.call_count, 2)

    def test_unlabelled_named_volume_stops_cleanup(self):
        volume = self.marker["volume"]
        inspected = json.dumps([{"Name": volume, "Labels": None}])
        with patch.object(publishing, "command", side_effect=[volume, inspected]) as command:
            with self.assertRaisesRegex(RuntimeError, "Очистка запрещена"):
                publishing.verify_owned(self.marker["project"], self.marker["token"],
                                        io.StringIO(), volume=volume)
        self.assertEqual(command.call_count, 2)


if __name__ == "__main__":
    unittest.main()
