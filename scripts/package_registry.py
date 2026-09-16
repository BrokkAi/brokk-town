#!/usr/bin/env python3
"""Publish the built npm packages; existing identical versions are skipped."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import urllib.error
import urllib.parse
import urllib.request

import package_installers
import package_release as release


def fetch_json(url):
    try:
        with urllib.request.urlopen(url, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        error.close()
        if error.code == 404:
            return None
        raise


def npm_exists(package):
    """Check the version record only. Differing bytes fail closed; visibility
    lag after an accepted upload surfaces as missing, and the re-run skips it
    once the record appears."""
    name = urllib.parse.quote(package["name"], safe="")
    record = fetch_json(f"https://registry.npmjs.org/{name}/{package['version']}")
    if record is None:
        return False
    if record.get("name") != package["name"] or record.get("version") != package["version"]:
        raise ValueError(f"published npm package differs from staged bytes: {package['name']}")
    if record.get("dist", {}).get("integrity") != package["integrity"]:
        raise ValueError(f"published npm package differs from staged bytes: {package['name']}")
    return True


def npm_tag(package):
    return "next" if "-" in package["version"] else "latest"


def validated_packages(directory):
    manifest = json.loads((directory / "npm/manifest.json").read_text())
    release.validate_tag(manifest["tag"])
    if manifest["commit"] != release.commit():
        raise ValueError("npm manifest must match the exact checkout commit")
    npm_version = manifest["tag"][1:]
    expected_names = {package_installers.NPM_ROOT} | {
        f"{package_installers.NPM_ROOT}-{system}-{arch}" for system in ("linux", "darwin") for arch in ("x64", "arm64")
    }
    packages = manifest["packages"]
    if len(packages) != 5 or {p["name"] for p in packages} != expected_names:
        raise ValueError("manifest must contain all five npm packages exactly once")
    packages.sort(key=lambda p: p["name"] == package_installers.NPM_ROOT)
    for package in packages:
        if package["version"] != npm_version or Path(package["filename"]).name != package["filename"]:
            raise ValueError("invalid npm package version or filename")
        data = (directory / "npm" / package["filename"]).read_bytes()
        integrity = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
        if release.digest(data) != package["sha256"] or integrity != package["integrity"]:
            raise ValueError(f"corrupt staged npm package: {package['filename']}")
    return packages


def run(command, directory):
    packages = validated_packages(directory)
    # Discover conflicts in every destination before making the first write.
    existing = {p["name"]: npm_exists(p) for p in packages}
    if command == "check":
        print("Package versions are available or identical. This checks availability, not publishing authorization.")
        return
    # Submit platform packages before the root launcher so a partial run
    # leaves the launcher pointing at resolvable dependencies.
    for package in packages:
        if not existing[package["name"]]:
            submit(package, directory)
    print("Submitted npm packages; registry visibility may lag behind accepted uploads")


def submit(package, directory):
    """Submit one package. An E409 for a version that is now visible means a
    previous attempt already staged it, so skip; otherwise re-raise and let a
    re-run finish the job."""
    tarball = directory / "npm" / package["filename"]
    command = ["npm", "publish", str(tarball.resolve()),
               "--access", "public", "--registry", "https://registry.npmjs.org",
               "--provenance", "--tag", npm_tag(package)]
    try:
        completed = subprocess.run(command, check=True, capture_output=True, text=True)
    except subprocess.CalledProcessError as error:
        output = (error.stdout or "") + (error.stderr or "")
        print(output)
        if "E409" not in output and "previously staged version" not in output:
            raise
        if npm_exists(package):
            print(f"{package['name']} is already published with identical bytes; skipping")
            return
        raise ValueError(f"{package['name']} was staged but is not yet visible; re-run to continue") from None
    print(completed.stdout)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("check", "publish"))
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    run(args.command, args.directory)


if __name__ == "__main__":
    main()
