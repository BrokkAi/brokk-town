#!/usr/bin/env python3
"""Tag-triggered release publisher. Runs in publish-packages.yml on a pushed v* tag.

Builds the four native archives and five npm packages from the tagged commit,
stages them in a draft GitHub release, publishes the npm packages with
provenance, then finalizes the release. Pushing the tag is the release request;
there is no dispatch flag or preflight tag.

Re-runs are safe: existing draft assets and published package versions are
reused, while anything conflicting fails closed for investigation. There is no
read-back verification: a re-run only fills in what is missing, and upload
exit codes gate each step.
"""

import argparse
from pathlib import Path
import json
import os
import subprocess
import sys

import package_installers
import package_registry
import package_release as release

REPO = "BrokkAi/brokk-town"
GH_REPO = "github.com/" + REPO


def log(message):
    print(message, flush=True)


def make_latest(tag):
    return "false"


def context(tag_arg=None, sha_arg=None):
    """Resolve and locally validate the release tag and commit. No network."""
    tag = tag_arg or os.environ.get("RELEASE_TAG") or os.environ.get("GITHUB_REF_NAME", "")
    sha = sha_arg or os.environ.get("RELEASE_COMMIT") or os.environ.get("GITHUB_SHA", "")
    if len(sha) != 40 or any(char not in "0123456789abcdef" for char in sha):
        raise ValueError("release requires the exact 40-character tagged commit SHA")
    if release.commit() != sha:
        raise ValueError("checkout is not at the tagged commit")
    expected_ref = "refs/tags/" + tag
    actual_ref = os.environ.get("GITHUB_REF", "")
    if actual_ref and actual_ref != expected_ref:
        raise ValueError(f"refusing to publish {tag} from {actual_ref}")
    if not tag.endswith("-repo-bot"):
        raise ValueError("release tag must end in -repo-bot")
    version = release.version_tag(tag)[1:]
    manifest_path = Path(__file__).resolve().parents[3] / "bundle.json"
    if manifest_path.exists():
        entries = json.loads(manifest_path.read_text())["bots"].values()
        entry = next(b for b in entries if b["project"] == "repo-bot")
        if version != entry["version"]:
            raise ValueError("bot tag does not match the bundle manifest version")
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


def find_release(tag):
    record = api("releases/tags/" + tag, missing=True)
    if record is not None:
        return record
    # GitHub's by-tag endpoint can omit drafts. Listing releases includes them;
    # otherwise a rerun creates a second draft and collides with the first assets.
    matches = []
    page = 1
    while True:
        records = api(f"releases?per_page=100&page={page}")
        matches.extend(record for record in records if record["tag_name"] == tag)
        if len(records) < 100:
            break
        page += 1
    if len(matches) > 1:
        raise ValueError("multiple releases use this tag; resolve duplicate drafts before retrying")
    return matches[0] if matches else None


def missing_assets(native, existing):
    expected = {path.name for path in native.iterdir()}
    if not set(existing) <= expected:
        raise ValueError("unexpected GitHub assets on the draft release")
    return sorted(expected - set(existing))


def publish(directory, tag, sha, check_only=False):
    if directory.exists():
        raise ValueError("build output must not exist; use a fresh directory")
    native = directory / "native"
    packages = directory / "packages"
    release.package(tag, native)
    package_installers.package(tag, native, packages, sha)
    if check_only:
        log("check-only: built everything; nothing uploaded")
        return
    record = find_release(tag)
    if record is not None and not record["draft"]:
        log(f"{tag} is already released; nothing to do")
        return
    if record is None:
        record = api("releases", "POST", {
            "tag_name": tag, "target_commitish": sha, "name": f"repo-bot {tag}",
            "body": f"Release {tag} from commit {sha}.",
            "draft": True, "prerelease": "-" in release.version_tag(tag),
        })
    if record["target_commitish"] != sha:
        raise ValueError("existing draft targets another commit; investigate before retrying")
    if not record["draft"]:
        raise ValueError("release changed concurrently; re-run to continue")
    existing = {asset["name"] for asset in api(f"releases/{record['id']}/assets?per_page=100")}
    for name in missing_assets(native, existing):
        subprocess.run(["gh", "release", "upload", tag, str(native / name), "--repo", GH_REPO], check=True)
    package_registry.run("publish", packages)
    api(f"releases/{record['id']}", "PATCH", {"draft": False, "make_latest": make_latest(tag)})
    log(f"Published {tag}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", default=None)
    parser.add_argument("--sha", default=None)
    parser.add_argument("--directory", type=Path, default=Path("dist/release"))
    parser.add_argument("--check-only", action="store_true",
                        help="build without uploading anything")
    args = parser.parse_args()
    tag, sha = context(args.tag, args.sha)
    publish(args.directory, tag, sha, check_only=args.check_only)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(f"Release failed: {error}")
