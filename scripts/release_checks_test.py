import base64
from datetime import datetime, timezone
import gzip
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import package_registry
import package_installers
import package_release as release
import publish_release
import release_checks as checks


class ReleaseChecks(unittest.TestCase):
    def test_npm_11_and_12_pack_metadata(self):
        info = {"name": "@brokkai/brokk-town", "version": "0.1.0",
                "filename": "package.tgz", "integrity": "sha512-example"}
        for output in ([info], {info["name"]: info}):
            self.assertEqual(package_installers.pack_record(json.dumps(output), info["name"], info["version"]), info)

    def test_ambiguous_or_mismatched_npm_pack_metadata_fails(self):
        info = {"name": "@brokkai/brokk-town", "version": "0.1.0",
                "filename": "package.tgz", "integrity": "sha512-example"}
        for output in ([], [info, info], {"other": info}, [dict(info, version="0.2.0")],
                       [dict(info, filename="../escape.tgz")]):
            with self.assertRaises(ValueError):
                package_installers.pack_record(json.dumps(output), info["name"], info["version"])

    def test_recompression_is_equal_but_payload_and_modes_are_not(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp) / "test.tar.gz"
            release.archive(archive, {"bt": b"binary", "BUILD.json": b"metadata"}, 0)
            original = archive.read_bytes()
            recompressed = gzip.compress(gzip.decompress(original), compresslevel=1, mtime=123)
            self.assertNotEqual(original, recompressed)
            self.assertEqual(release.archive_payload(original), release.archive_payload(recompressed))
            release.archive(archive, {"bt": b"changed binary", "BUILD.json": b"metadata"}, 0)
            self.assertNotEqual(release.archive_payload(original), release.archive_payload(archive.read_bytes()))
            raw = io.BytesIO()
            with tarfile.open(fileobj=raw, mode="w:gz") as out:
                for name, (mode, data) in release.archive_payload(original).items():
                    member = tarfile.TarInfo(name)
                    member.mode, member.size = mode & ~0o111, len(data)
                    out.addfile(member, io.BytesIO(data))
            self.assertNotEqual(release.archive_payload(original), release.archive_payload(raw.getvalue()))

    def test_duplicate_archive_entries_are_rejected(self):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode="w:gz") as out:
            for _ in range(2):
                member = tarfile.TarInfo("bt")
                member.size = 1
                out.addfile(member, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            release.archive_payload(data.getvalue())

    def test_remote_evidence_rejects_wrong_commit_event_and_incomplete_jobs(self):
        run = {"head_sha": "a" * 40, "event": "workflow_dispatch", "status": "completed", "conclusion": "success"}
        jobs = [{"status": "completed", "conclusion": "success"}]
        checks.successful_run(run, "a" * 40, jobs, "workflow_dispatch")
        for field, value in (("head_sha", "b" * 40), ("event", "push"),
                             ("status", "in_progress"), ("conclusion", "failure")):
            with self.subTest(field=field), self.assertRaises(ValueError):
                checks.successful_run(dict(run, **{field: value}), "a" * 40, jobs, "workflow_dispatch")
        for invalid in ([], [{"status": "completed", "conclusion": "skipped"}]):
            with self.assertRaises(ValueError):
                checks.successful_run(run, "a" * 40, invalid, "workflow_dispatch")

    def test_staging_only_or_wrong_workflow_trust_is_not_direct_publish_authorization(self):
        trust = {"type": "github", "claims": {"repository": checks.REPO,
                 "workflow_ref": {"file": checks.WORKFLOW}, "environment": checks.ENVIRONMENT},
                 "permissions": ["createPackage"]}
        checks.validate_trust([trust])
        for change in ({"permissions": ["createStagedPackage"]},
                       {"claims": dict(trust["claims"], environment="other")},
                       {"claims": dict(trust["claims"], workflow_ref={"file": "ci.yml"})}):
            with self.assertRaises(ValueError):
                checks.validate_trust([dict(trust, **change)])

    def test_oidc_must_bind_unexpired_identity_to_commit_and_environment(self):
        now = datetime.now(timezone.utc).timestamp()
        claims = {"repository": checks.REPO, "sha": "a" * 40, "environment": checks.ENVIRONMENT,
                  "aud": "npm:registry.npmjs.org", "iss": "https://token.actions.githubusercontent.com",
                  "workflow_ref": f"{checks.REPO}/.github/workflows/{checks.WORKFLOW}@refs/heads/master",
                  "nbf": now - 5, "exp": now + 300}
        checks.validate_claims(claims, "a" * 40, claims["aud"])
        for change in ({"exp": now - 1}, {"environment": "other"}, {"sha": "b" * 40}, {"aud": "sigstore"}):
            with self.assertRaises(ValueError):
                checks.validate_claims(dict(claims, **change), "a" * 40, claims["aud"])

    def test_local_github_login_cannot_validate_actions_publisher(self):
        with patch.dict(os.environ, {"GITHUB_ACTIONS": "false"}), patch.object(checks, "api") as api:
            with self.assertRaisesRegex(ValueError, "actual Actions"):
                checks.github_authorization("a" * 40)
            api.assert_not_called()

    def test_probe_deletion_is_not_required_staging_state(self):
        env = {"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": checks.REPO, "GITHUB_JOB": "packages",
               "GITHUB_SHA": "a" * 40, "GITHUB_RUN_ID": "123", "GITHUB_RUN_ATTEMPT": "1"}
        with patch.dict(os.environ, env), patch.object(checks, "tag_commit", return_value=None), \
                patch.object(checks, "api", side_effect=[None, {"id": 1, "draft": True, "tag_name": "preflight-123-1"}, None]) as api:
            checks.github_authorization("a" * 40)
            self.assertEqual(api.call_args_list[-1].args, ("releases/1", "DELETE"))
            self.assertTrue(api.call_args_list[1].args[2]["draft"])

    def test_wrong_tag_or_permission_failure_prevents_all_uploads(self):
        with patch.dict(os.environ, {"GITHUB_REF": "refs/tags/v0.1.0"}), \
                patch.object(checks, "tag_commit", return_value="a" * 40), \
                patch.object(checks, "versions"), patch.object(checks, "github_version", return_value=None), \
                patch.object(checks, "authorization", side_effect=ValueError("permission denied")), \
                patch.object(checks, "api") as api, patch.object(publish_release.subprocess, "run") as upload:
            with self.assertRaisesRegex(ValueError, "permission denied"):
                publish_release.publish(Path("unused"), "a" * 40, "v0.1.0")
            api.assert_not_called()
            upload.assert_not_called()

    def test_completed_release_uses_read_only_verification(self):
        with patch.dict(os.environ, {"GITHUB_REF": "refs/tags/v0.1.0"}), \
                patch.object(checks, "tag_commit", return_value="a" * 40), \
                patch.object(checks, "versions"), patch.object(checks, "github_version", return_value={"draft": False}), \
                patch.object(checks, "authorization") as auth, patch.object(checks, "published") as verify, \
                patch.object(checks, "api") as api:
            publish_release.publish(Path("unused"), "a" * 40, "v0.1.0")
            auth.assert_not_called()
            api.assert_not_called()
            verify.assert_called_once()

    def test_npm_download_checks_own_integrity_and_payload(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp) / "package.tgz"
            release.archive(archive, {"package/bin/bt": b"binary"}, 0)
            data = gzip.compress(gzip.decompress(archive.read_bytes()), compresslevel=1, mtime=456)
            integrity = "sha512-" + base64.b64encode(package_registry.hashlib.sha512(data).digest()).decode()
            package = {"name": "@brokkai/brokk-town", "version": "0.1.0", "integrity": "different compression"}
            record = dict(package, dist={"integrity": integrity, "tarball": "https://registry.npmjs.org/package.tgz"})
            with patch.object(package_registry, "fetch_json", return_value=record), \
                    patch.object(package_registry.urllib.request, "urlopen", return_value=io.BytesIO(data)):
                self.assertTrue(package_registry.npm_exists(package, archive))
            with patch.object(package_registry, "fetch_json", return_value=record), \
                    patch.object(package_registry.urllib.request, "urlopen", return_value=io.BytesIO(b"corrupt")):
                with self.assertRaisesRegex(ValueError, "integrity"):
                    package_registry.npm_exists(package, archive)


if __name__ == "__main__":
    unittest.main()
