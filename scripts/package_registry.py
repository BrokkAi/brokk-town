#!/usr/bin/env python3
"""Publish the built npm packages. npm is the authority on what already exists."""

import argparse
import concurrent.futures
import json
from pathlib import Path
import subprocess

import package_installers

REGISTRY = "https://registry.npmjs.org"
# npm's wording for "this version is already there"; anything else is a real failure.
CONFLICT = ("E409", "EPUBLISHCONFLICT", "cannot publish over", "previously staged version")


def staged_packages(directory):
    manifest = json.loads((directory / "npm/manifest.json").read_text())
    packages = manifest["packages"]
    launcher = [p for p in packages if p["name"] == package_installers.NPM_ROOT]
    platforms = [p for p in packages if p["name"] != package_installers.NPM_ROOT]
    if len(launcher) != 1 or len(platforms) != 4:
        raise ValueError("manifest must contain the launcher and four platform packages")
    return platforms, launcher[0]


def npm_tag(package):
    return "next" if "-" in package["version"] else "latest"


def submit(package, directory):
    """Publish one package. A version that already exists is done, not an error."""
    tarball = (directory / "npm" / package["filename"]).resolve()
    command = ["npm", "publish", str(tarball), "--access", "public",
               "--registry", REGISTRY, "--provenance", "--tag", npm_tag(package)]
    result = subprocess.run(command, capture_output=True, text=True)
    output = (result.stdout or "") + (result.stderr or "")
    print(f"--- {package['name']}\n{output}", flush=True)
    if result.returncode == 0:
        return
    if any(marker.lower() in output.lower() for marker in CONFLICT):
        print(f"{package['name']} {package['version']} is already published; skipping", flush=True)
        return
    raise subprocess.CalledProcessError(result.returncode, command, output)


def publish_all(directory):
    platforms, launcher = staged_packages(directory)
    # The launcher pins these versions as optionalDependencies, and npm treats a
    # missing optional dependency as a successful install. Publishing it before
    # they exist hands out a bt with no binary, so it goes last.
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(platforms)) as pool:
        for future in [pool.submit(submit, package, directory) for package in platforms]:
            future.result()
    submit(launcher, directory)
    print("Published npm packages; registry visibility may lag behind accepted uploads")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    publish_all(args.directory)


if __name__ == "__main__":
    main()
