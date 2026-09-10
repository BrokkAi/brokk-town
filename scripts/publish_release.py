#!/usr/bin/env python3
"""Publication entry point. Never invoke during preflight."""

import os
from pathlib import Path
import subprocess
import tempfile
import time

import package_registry
import release_checks as checks


def publish(directory, sha, tag):
    if os.environ.get("GITHUB_REF") != "refs/tags/" + tag or checks.tag_commit(tag) != sha:
        raise ValueError("publication requires the existing tag at the exact release commit")
    checks.versions(directory, sha, tag)
    record = checks.github_version(directory, sha, tag)
    if record is not None and not record["draft"]:
        checks.published(directory, sha, tag)
        return
    checks.authorization(sha)
    # Recreate staging independently of the disposable authorization probe.
    checks.versions(directory, sha, tag)
    record = checks.github_version(directory, sha, tag)
    if record is None:
        record = checks.api("releases", "POST", {
            "tag_name": tag, "target_commitish": sha, "name": f"Brokk Town {tag}",
            "body": f"Release from commit {sha}. See RELEASING.md for destinations and verification.",
            "draft": True, "prerelease": "-" in tag,
        })
    if not record["draft"]:
        raise ValueError("release changed concurrently; re-run read-only verification")
    existing = {a["name"] for a in checks.api(f"releases/{record['id']}/assets?per_page=100")}
    for asset in sorted((directory / "native").iterdir()):
        if asset.name not in existing:
            subprocess.run(["gh", "release", "upload", tag, str(asset), "--repo", checks.GH_REPO], check=True)
    # Upload integrity compares with exact staged bytes within this job.
    with tempfile.TemporaryDirectory() as temp:
        subprocess.run(["gh", "release", "download", tag, "--repo", checks.GH_REPO, "--dir", temp], check=True)
        actual = Path(temp)
        if {p.name for p in actual.iterdir()} != {p.name for p in (directory / "native").iterdir()}:
            raise ValueError("incomplete staged native upload")
        for asset in (directory / "native").iterdir():
            if (actual / asset.name).read_bytes() != asset.read_bytes():
                raise ValueError(f"upload integrity mismatch: {asset.name}")
    package_registry.run("publish", directory / "packages")
    for attempt in range(40):
        try:
            package_registry.run("verify", directory / "packages")
            break
        except ValueError as error:
            if "publication is incomplete" not in str(error) or attempt == 39:
                raise
            time.sleep(15)
    checks.github_version(directory, sha, tag)
    checks.api(f"releases/{record['id']}", "PATCH", {"draft": False, "make_latest": "false" if "-" in tag else "true"})
    checks.published(directory, sha, tag)


if __name__ == "__main__":
    commit, version = checks.context()
    publish(Path("dist/candidate"), commit, version)
