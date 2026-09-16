#!/usr/bin/env python3
"""Tag-triggered release publisher. Runs in publish-packages.yml on a pushed v* tag.

Builds the four native archives and five npm packages from the tagged commit,
stages them in a draft GitHub release, publishes the npm packages with
provenance, then finalizes the release. Pushing the tag is the release request;
there is no dispatch flag or preflight tag.

Re-runs are safe: identical existing assets and package versions are reused,
while conflicts fail closed for investigation.
"""

import argparse
from pathlib import Path
import json
import os
import subprocess
import sys
import tempfile
import time

import package_installers
import package_registry
import package_release as release
import smoke_installers

REPO = "BrokkAi/brokk-town"
GH_REPO = "github.com/" + REPO


def log(message):
    print(message, flush=True)


def make_latest(tag):
    return "false" if "-" in tag else "true"


def context(tag_arg=None, sha_arg=None):
    """Resolve and locally validate the release tag and commit. No network."""
    tag = tag_arg or os.environ.get("RELEASE_TAG") or os.environ.get("GITHUB_REF_NAME", "")
    sha = sha_arg or os.environ.get("RELEASE_COMMIT") or os.environ.get("GITHUB_SHA", "")
    release.validate_tag(tag)
    if len(sha) != 40 or any(char not in "0123456789abcdef" for char in sha):
        raise ValueError("release requires the exact 40-character tagged commit SHA")
    if release.commit() != sha:
        raise ValueError("checkout is not at the tagged commit")
    expected_ref = "refs/tags/" + tag
    actual_ref = os.environ.get("GITHUB_REF", "")
    if actual_ref and actual_ref != expected_ref:
        raise ValueError(f"refusing to publish {tag} from {actual_ref}")
    return tag, sha


def api(path, method="GET", body=None, missing=False):
    args = ["gh", "api", "--hostname", "github.com", f"repos/{REPO}/{path}", "--method", method]
    if body is not None:
        args += ["--input", "-"]
    result = subprocess.run(args, input=json.dumps(body) if body is not None else None,
                            text=True, capture_output=True, timeout=120)
    if result.returncode:
        if missing and "(HTTP 404)" in result.stderr:
            return None
        raise RuntimeError(f"GitHub {method} {path} failed: {result.stderr.strip()}")
    return json.loads(result.stdout) if result.stdout.strip() else None


def tag_commit(tag):
    import urllib.parse
    ref = api("git/ref/tags/" + urllib.parse.quote(tag, safe=""), missing=True)
    if ref is None:
        return None
    obj = ref["object"]
    for _ in range(5):
        if obj["type"] == "commit":
            return obj["sha"]
        if obj["type"] != "tag":
            break
        obj = api("git/tags/" + obj["sha"])["object"]
    raise ValueError("tag does not resolve to a commit")


def check_remote_tag(tag, sha):
    remote_sha = tag_commit(tag)
    if remote_sha not in (None, sha):
        raise ValueError("release tag missing or points to another commit; never move a pushed tag")


def missing_assets(native, existing):
    expected = {path.name for path in native.iterdir()}
    if not set(existing) <= expected:
        raise ValueError("unexpected GitHub assets on the draft release")
    return sorted(expected - set(existing))


def verify_staged(tag, sha, native, uploaded):
    with tempfile.TemporaryDirectory() as temp:
        actual = Path(temp)
        subprocess.run(["gh", "release", "download", tag, "--repo", GH_REPO, "--dir", temp], check=True)
        for name in uploaded:
            if (actual / name).read_bytes() != (native / name).read_bytes():
                raise ValueError(f"upload integrity mismatch: {name}")
        release.compare_assets(tag, actual, native, sha)


def publish_npm(packages):
    # Accepted uploads can lag behind registry visibility, including between a
    # new version record and its tarball. Submission and verification retry
    # together on incomplete publication; discovery never resubmits an
    # existing version, so waiting cannot duplicate an upload.
    for attempt in range(40):
        try:
            package_registry.run("publish", packages)
            package_registry.run("verify", packages)
            return
        except ValueError as error:
            if "publication is incomplete" not in str(error) or attempt == 39:
                raise
            time.sleep(15)


def certify(tag, sha, native, packages):
    record = api("releases/tags/" + tag, missing=True)
    if record is None or record["draft"] or not record.get("published_at"):
        raise ValueError("GitHub release is not finalized")
    assets = api(f"releases/{record['id']}/assets?per_page=100")
    if {asset["name"] for asset in assets} != {path.name for path in native.iterdir()}:
        raise ValueError("incomplete public GitHub release")
    with tempfile.TemporaryDirectory() as temp:
        actual = Path(temp)
        subprocess.run(["gh", "release", "download", tag, "--repo", GH_REPO, "--dir", temp], check=True)
        release.compare_assets(tag, actual, native, sha)
    package_registry.run("verify", packages)
    if "-" not in tag:
        latest = api("releases/latest")
        if latest.get("tag_name") != tag:
            raise ValueError("GitHub latest release does not point to this version")
    log(f"Certified publication of {tag}: GitHub release, native assets, "
        "npm packages and provenance all match")


def publish(directory, tag, sha, check_only=False):
    if directory.exists():
        raise ValueError("build output must not exist; use a fresh directory")
    native = directory / "native"
    packages = directory / "packages"
    release.package(tag, native)
    package_installers.package(tag, native, packages, sha)
    smoke_installers.smoke(packages)
    if check_only:
        log("check-only: built and locally verified everything; nothing uploaded")
        return
    check_remote_tag(tag, sha)
    record = api("releases/tags/" + tag, missing=True)
    if record is not None and not record["draft"]:
        certify(tag, sha, native, packages)
        return
    if record is None:
        record = api("releases", "POST", {
            "tag_name": tag, "target_commitish": sha, "name": f"Brokk Town {tag}",
            "body": f"Release {tag} from commit {sha}.",
            "draft": True, "prerelease": "-" in tag,
        })
    if record["target_commitish"] != sha:
        raise ValueError("existing draft targets another commit; investigate before retrying")
    if not record["draft"]:
        raise ValueError("release changed concurrently; re-run to verify")
    existing = {asset["name"] for asset in api(f"releases/{record['id']}/assets?per_page=100")}
    pending = missing_assets(native, existing)
    for name in pending:
        subprocess.run(["gh", "release", "upload", tag, str(native / name), "--repo", GH_REPO], check=True)
    verify_staged(tag, sha, native, pending)
    publish_npm(packages)
    api(f"releases/{record['id']}", "PATCH", {"draft": False, "make_latest": make_latest(tag)})
    certify(tag, sha, native, packages)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", default=None)
    parser.add_argument("--sha", default=None)
    parser.add_argument("--directory", type=Path, default=Path("dist/release"))
    parser.add_argument("--check-only", action="store_true",
                        help="build and locally verify without uploading anything")
    args = parser.parse_args()
    tag, sha = context(args.tag, args.sha)
    publish(args.directory, tag, sha, check_only=args.check_only)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(f"Release failed: {error}")
