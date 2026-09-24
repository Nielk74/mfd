import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from deploy import Deployer, atomic_json, exclusive_lock, successful_ci
from install_deploy import cron_text


class DeploymentTests(unittest.TestCase):
    def test_ci_must_match_exact_main_push_and_latest_run(self):
        good = dict(id=1, head_sha="a" * 40, head_branch="main", event="push",
                    status="completed", conclusion="success")
        self.assertEqual(successful_ci([good], "a" * 40, "main"), good)
        for change in [dict(head_sha="b" * 40), dict(head_branch="feature"),
                       dict(event="pull_request"), dict(status="in_progress"),
                       dict(conclusion="failure")]:
            self.assertIsNone(successful_ci([good | change], "a" * 40, "main"))
        self.assertIsNone(successful_ci([good, good | dict(id=2, conclusion="failure")], "a" * 40, "main"))

    def test_cron_install_preserves_other_jobs_and_is_idempotent(self):
        existing = "TZ=Europe/Paris\n* * * * * /unrelated/job\n"
        installed = cron_text(existing, "/python /deploy.py")
        self.assertIn(existing, installed)
        self.assertEqual(cron_text(installed, "/python /deploy.py"), installed)
        changed = cron_text(installed, "/python /new.py")
        self.assertNotIn("/deploy.py", changed)
        self.assertIn("/unrelated/job", changed)
        self.assertEqual(changed.count("@reboot"), 1)

    def test_lock_releases_and_json_write_is_complete(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            with exclusive_lock(root / "lock") as first:
                self.assertTrue(first)
                with exclusive_lock(root / "lock") as second:
                    self.assertFalse(second)
            with exclusive_lock(root / "lock") as third:
                self.assertTrue(third)
            atomic_json(root / "status.json", {"current_sha": "a" * 40})
            self.assertEqual(json.loads((root / "status.json").read_text())["current_sha"], "a" * 40)
            self.assertFalse((root / "status.tmp").exists())

    def test_failed_health_rolls_back_without_advancing_current(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "config.json").write_text(json.dumps({"repository": "Nielk74/mfd", "branch": "main",
                                                        "project": "mfd", "docker_context": "colima"}))
            (root / "settings.env").write_text("MFD_PORT=8088\n")
            atomic_json(root / "status.json", {"current_sha": "a" * 40})
            deployer = Deployer(root)
            with patch.object(deployer, "release"), patch.object(deployer, "backup"), \
                 patch.object(deployer, "compose") as compose, \
                 patch.object(deployer, "wait_healthy", side_effect=[RuntimeError("bad candidate"), None]):
                with self.assertRaises(RuntimeError):
                    deployer.deploy("b" * 40, {"html_url": "https://example.test/ci"})
                self.assertEqual(deployer.state["current_sha"], "a" * 40)
                self.assertEqual(deployer.state["failed_sha"], "b" * 40)
                self.assertEqual(compose.call_args_list[-1].args[0], "a" * 40)

    def test_changed_private_settings_recreate_current_release(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "config.json").write_text(json.dumps({"repository": "Nielk74/mfd", "branch": "main",
                                                        "project": "mfd", "docker_context": "colima"}))
            (root / "settings.env").write_text("ETORO_DEMO_USER_KEY=new-private-key\n")
            atomic_json(root / "status.json", {"current_sha": "a" * 40, "settings_sha256": "old"})
            deployer = Deployer(root)
            with patch.object(deployer, "runtime"), patch.object(deployer, "verify"), \
                 patch.object(deployer, "compose") as compose, \
                 patch.object(deployer, "wait_healthy"), \
                 patch.object(deployer, "fetch", return_value="a" * 40):
                deployer.tick()
                self.assertEqual(compose.call_count, 1)
                self.assertEqual(deployer.state["settings_sha256"], deployer.settings_sha256)


if __name__ == "__main__":
    unittest.main()
