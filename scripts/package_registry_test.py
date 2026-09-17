import json
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch

import package_installers
import package_registry


def completed(command, code=0, output=""):
    return subprocess.CompletedProcess(command, code, stdout=output, stderr="")


class PackageRegistry(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "npm").mkdir()
        self.packages = []
        names = [package_installers.NPM_ROOT] + [
            f"{package_installers.NPM_ROOT}-{system}-{arch}"
            for system in ("linux", "darwin") for arch in ("x64", "arm64")
        ]
        for index, name in enumerate(names):
            filename = f"package-{index}.tgz"
            (self.root / "npm" / filename).write_bytes(name.encode())
            self.packages.append({"name": name, "version": "0.1.0", "filename": filename})
        (self.root / "npm/manifest.json").write_text(
            json.dumps({"tag": "v0.1.0", "commit": "a" * 40, "packages": self.packages}))

    def published(self, calls):
        return [call[call.index("--tag") + 1] for call in calls]

    def test_launcher_publishes_after_every_platform_package(self):
        order = []
        lock = threading.Lock()

        def publish(command, **kwargs):
            with lock:
                order.append(Path(command[2]).name)
            return completed(command)

        with patch.object(package_registry.subprocess, "run", side_effect=publish):
            package_registry.publish_all(self.root)
        self.assertEqual(len(order), 5)
        self.assertEqual(order[4], "package-0.tgz")
        self.assertEqual(set(order[:4]), {f"package-{i}.tgz" for i in range(1, 5)})

    def test_platform_packages_publish_concurrently(self):
        barrier = threading.Barrier(4, timeout=5)

        def publish(command, **kwargs):
            if not Path(command[2]).name.endswith("package-0.tgz"):
                barrier.wait()
            return completed(command)

        with patch.object(package_registry.subprocess, "run", side_effect=publish):
            package_registry.publish_all(self.root)

    def test_every_publish_requests_provenance_and_public_access(self):
        with patch.object(package_registry.subprocess, "run",
                          side_effect=lambda command, **kwargs: completed(command)) as command:
            package_registry.publish_all(self.root)
            calls = [call.args[0] for call in command.call_args_list]
        self.assertEqual(len(calls), 5)
        for call in calls:
            self.assertEqual(call[:2], ["npm", "publish"])
            self.assertIn("--provenance", call)
            self.assertIn("--access", call)
        self.assertEqual(self.published(calls), ["latest"] * 5)

    def test_prerelease_versions_use_the_next_dist_tag(self):
        for package in self.packages:
            package["version"] = "0.1.0-rc.1"
        (self.root / "npm/manifest.json").write_text(
            json.dumps({"tag": "v0.1.0-rc.1", "commit": "a" * 40, "packages": self.packages}))
        with patch.object(package_registry.subprocess, "run",
                          side_effect=lambda command, **kwargs: completed(command)) as command:
            package_registry.publish_all(self.root)
            calls = [call.args[0] for call in command.call_args_list]
        self.assertEqual(self.published(calls), ["next"] * 5)

    def test_already_published_version_is_skipped(self):
        conflict = ("npm error code E409\n"
                    "npm error 409 Conflict - Cannot publish over previously staged version \"0.1.0\".\n")
        with patch.object(package_registry.subprocess, "run",
                          side_effect=lambda command, **kwargs: completed(command, 1, conflict)) as command:
            package_registry.publish_all(self.root)
            self.assertEqual(command.call_count, 5)

    def test_non_conflict_failure_raises(self):
        with patch.object(package_registry.subprocess, "run",
                          side_effect=lambda command, **kwargs: completed(command, 1, "npm error code E401\n")):
            with self.assertRaises(subprocess.CalledProcessError):
                package_registry.publish_all(self.root)

    def test_platform_failure_stops_before_the_launcher(self):
        def publish(command, **kwargs):
            if Path(command[2]).name == "package-0.tgz":
                raise AssertionError("launcher published after a platform package failed")
            return completed(command, 1, "npm error code E401\n")

        with patch.object(package_registry.subprocess, "run", side_effect=publish):
            with self.assertRaises(subprocess.CalledProcessError):
                package_registry.publish_all(self.root)

    def test_incomplete_manifest_publishes_nothing(self):
        (self.root / "npm/manifest.json").write_text(
            json.dumps({"tag": "v0.1.0", "commit": "a" * 40, "packages": self.packages[:3]}))
        with patch.object(package_registry.subprocess, "run") as command:
            with self.assertRaisesRegex(ValueError, "four platform packages"):
                package_registry.publish_all(self.root)
            command.assert_not_called()


if __name__ == "__main__":
    unittest.main()
