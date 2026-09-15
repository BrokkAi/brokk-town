import gzip
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import package_release as release
import publish_release
import release_checks as checks


class PublishRecovery(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.native = self.directory / "native"
        self.native.mkdir()
        self.tag, self.sha = "v0.1.0", "a" * 40
        self.assets = {}
        for target in release.TARGETS:
            name = release.archive_name(self.tag, target)
            release.archive(self.native / name, {
                "bt": b"expected binary", **release.licenses.legal_files(), "README.md": b"readme",
                "BUILD.json": json.dumps({"tag": self.tag, "commit": self.sha, "target": target}).encode(),
            }, 0)
            self.assets[name] = (self.native / name).read_bytes()
        self.update_manifest(self.assets)
        for name, data in self.assets.items():
            (self.native / name).write_bytes(data)
        self.remote = dict(self.assets)
        self.uploaded = []
        self.corrupt_upload = None

    def update_manifest(self, assets):
        manifest = {"tag": self.tag, "commit": self.sha, "assets": [
            {"name": name, "size": len(assets[name]), "sha256": release.digest(assets[name])}
            for name in sorted(assets) if name.endswith(".tar.gz")
        ]}
        assets["release.json"] = json.dumps(manifest).encode()
        assets["checksums.txt"] = "".join(
            f"{asset['sha256']}  {asset['name']}\n" for asset in manifest["assets"]).encode()

    def fake_api(self, path, method="GET", body=None):
        if path == "releases/1/assets?per_page=100" and method == "GET":
            return [{"name": name} for name in self.remote]
        if path == "releases/1" and method == "PATCH" and body["draft"] is False:
            return {"draft": False}
        self.fail(f"unexpected GitHub operation: {method} {path}")

    def fake_gh(self, args, **kwargs):
        self.assertEqual(args[:2], ["gh", "release"])
        self.assertEqual(args[args.index("--repo") + 1], checks.GH_REPO)
        if args[2] == "upload":
            asset = Path(args[4])
            self.uploaded.append(asset.name)
            data = asset.read_bytes()
            if asset.name == self.corrupt_upload:
                data = gzip.compress(gzip.decompress(data), compresslevel=1, mtime=321)
            self.remote[asset.name] = data
        elif args[2] == "download":
            destination = Path(args[args.index("--dir") + 1])
            for name, data in self.remote.items():
                (destination / name).write_bytes(data)
        else:
            self.fail(f"unexpected gh operation: {args[2]}")

    def publish(self):
        with patch.dict(os.environ, {"GITHUB_REF": "refs/tags/" + self.tag}), \
                patch.object(checks, "tag_commit", return_value=self.sha), \
                patch.object(checks, "versions"), patch.object(checks, "authorization"), \
                patch.object(checks, "github_version", return_value={"id": 1, "draft": True}), \
                patch.object(checks, "api", side_effect=self.fake_api), \
                patch.object(checks, "published"), \
                patch.object(publish_release.subprocess, "run", side_effect=self.fake_gh), \
                patch.object(publish_release.package_registry, "run") as registry:
            try:
                publish_release.publish(self.directory, self.sha, self.tag)
            except ValueError:
                registry.assert_not_called()
                raise
            self.assertEqual([call.args[0] for call in registry.call_args_list], ["publish", "verify"])

    def test_complete_draft_reuses_equivalent_recompressed_assets(self):
        for name, data in list(self.remote.items()):
            if name.endswith(".tar.gz"):
                self.remote[name] = gzip.compress(gzip.decompress(data), compresslevel=1, mtime=123)
                self.assertNotEqual(self.remote[name], data)
        self.update_manifest(self.remote)
        self.publish()
        self.assertEqual(self.uploaded, [])

    def test_retained_payload_changes_fail_even_with_valid_manifest(self):
        name = release.archive_name(self.tag, release.TARGETS[0])
        files = {filename: data for filename, (_, data) in release.archive_payload(self.remote[name]).items()}
        files["bt"] = b"different binary"
        changed = self.directory / "changed.tar.gz"
        release.archive(changed, files, 0)
        self.remote[name] = changed.read_bytes()
        self.update_manifest(self.remote)
        with self.assertRaisesRegex(ValueError, "published payload differs"):
            self.publish()

    def test_new_upload_requires_exact_staged_bytes(self):
        name = release.archive_name(self.tag, release.TARGETS[0])
        del self.remote[name]
        self.corrupt_upload = name
        with self.assertRaisesRegex(ValueError, "upload integrity mismatch"):
            self.publish()
        self.assertEqual(self.uploaded, [name])

    def test_partial_draft_uploads_only_missing_asset(self):
        name = release.archive_name(self.tag, release.TARGETS[0])
        del self.remote[name]
        self.publish()
        self.assertEqual(self.uploaded, [name])


if __name__ == "__main__":
    unittest.main()
