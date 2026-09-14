#!/usr/bin/env python3
"""Check, publish, and verify the built npm packages; retries require identical bytes."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
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


def fetch_bytes(url):
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "https" or parsed.hostname != "registry.npmjs.org":
        raise ValueError("unexpected npm attestation origin")
    with urllib.request.urlopen(url, timeout=60) as response:
        return response.read()


def npm_exists(package, tarball=None):
    name = urllib.parse.quote(package["name"], safe="")
    record = fetch_json(f"https://registry.npmjs.org/{name}/{package['version']}")
    if record is None:
        return False
    if record.get("name") != package["name"] or record.get("version") != package["version"]:
        raise ValueError(f"published npm package differs from staged bytes: {package['name']}")
    if tarball is None:
        if record.get("dist", {}).get("integrity") != package["integrity"]:
            raise ValueError(f"published npm package differs from staged bytes: {package['name']}")
        return True
    url = record.get("dist", {}).get("tarball", "")
    if urllib.parse.urlparse(url).scheme != "https" or urllib.parse.urlparse(url).hostname != "registry.npmjs.org":
        raise ValueError("unexpected npm tarball origin")
    with urllib.request.urlopen(url, timeout=60) as response:
        data = response.read()
    integrity = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
    if integrity != record["dist"].get("integrity"):
        raise ValueError("downloaded npm tarball fails registry integrity")
    if release.archive_payload(data) != release.archive_payload(tarball.read_bytes()):
        raise ValueError(f"published npm payload differs from expected build: {package['name']}")
    return True


def npm_tag(package):
    return "next" if "-" in package["version"] else "latest"


def npm_pointer(package):
    name = urllib.parse.quote(package["name"], safe="")
    record = fetch_json(f"https://registry.npmjs.org/{name}")
    if record is None:
        return None
    if record.get("name") != package["name"] or not isinstance(record.get("dist-tags", {}), dict):
        raise ValueError(f"invalid npm dist-tag metadata: {package['name']}")
    pointer = record.get("dist-tags", {}).get(npm_tag(package))
    if pointer is not None and (not isinstance(pointer, str) or not pointer):
        raise ValueError(f"invalid npm dist-tag metadata: {package['name']}")
    return pointer


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


def verify_provenance(directory):
    packages = validated_packages(directory)
    records = []
    for package in packages:
        encoded = urllib.parse.quote(package["name"], safe="")
        record = fetch_json(f"https://registry.npmjs.org/{encoded}/{package['version']}")
        metadata = record.get("dist", {}).get("attestations") if record else None
        provenance = metadata.get("provenance") if isinstance(metadata, dict) else None
        url = metadata.get("url") if isinstance(metadata, dict) else None
        predicate = provenance.get("predicateType") if isinstance(provenance, dict) else None
        if not url or predicate != "https://slsa.dev/provenance/v1":
            raise ValueError(f"publication is incomplete: npm provenance is missing: {package['name']}")
        payload = json.loads(fetch_bytes(url))
        entries = payload.get("attestations") if isinstance(payload, dict) else None
        if not isinstance(entries, list):
            raise ValueError(f"publication is incomplete: npm provenance is malformed: {package['name']}")
        algorithm, encoded_digest = package["integrity"].split("-", 1)
        if algorithm != "sha512":
            raise ValueError(f"unexpected npm integrity algorithm: {package['name']}")
        digest = base64.b64decode(encoded_digest, validate=True).hex()
        records.append({"name": package["name"], "version": package["version"], "sha512": digest,
                        "attestations": entries})
    manifest = json.loads((directory / "npm/manifest.json").read_text())
    request = {"commit": manifest["commit"], "ref": "refs/tags/" + manifest["tag"], "packages": records}
    with tempfile.TemporaryDirectory() as temporary:
        request_path = Path(temporary) / "provenance.json"
        request_path.write_text(json.dumps(request))
        subprocess.run(["node", str(Path(__file__).resolve().parent / "verify_sigstore_bundles.cjs"),
                        str(request_path)], check=True)
    print("All five npm packages have independently verified SLSA provenance")


def run(command, directory):
    packages = validated_packages(directory)
    # Discover conflicts in every destination before making the first write.
    existing = {p["name"]: npm_exists(p, directory / "npm" / p["filename"]) for p in packages}
    if command == "check":
        print("Package versions are available or identical. This checks availability, not publishing authorization.")
        return
    if command == "verify":
        if not all(existing.values()):
            raise ValueError("publication is incomplete: an npm package is missing")
    # Dist-tags are mutable installer inputs, checked separately from immutable
    # package version availability. A retry must never retarget an existing
    # version's pointer: it may belong to a later completed release, and npm
    # publish authorization does not establish dist-tag write authorization.
    pointers = {p["name"]: npm_pointer(p) for p in packages}
    for package in packages:
        pointer = pointers[package["name"]]
        if command == "verify" and pointer != package["version"]:
            raise ValueError(f"publication is incomplete: npm {package['name']} {npm_tag(package)} "
                             f"points to {pointer!r}, expected {package['version']}")
        if existing[package["name"]] and pointer != package["version"]:
            raise ValueError(f"conflicting npm dist-tag: {package['name']} {npm_tag(package)} "
                             f"points to {pointer!r}, expected {package['version']}; "
                             "an authorized npm maintainer must reconcile the pointer before retrying")
    if command == "verify":
        verify_provenance(directory)
        print("All five npm package payloads, provenance, and latest/next pointers match the release")
        return
    if command == "check-pointers":
        print("Existing npm versions have matching latest/next pointers")
        return
    # Submit platform packages before the root launcher. A successful upload
    # can take time to appear in public indexes; visibility is checked only by
    # the explicit verify command, not used as a release gate.
    for package in packages:
        if not existing[package["name"]]:
            subprocess.run(["npm", "publish", str((directory / "npm" / package["filename"]).resolve()),
                            "--access", "public", "--registry", "https://registry.npmjs.org",
                            "--provenance",
                            "--tag", npm_tag(package)], check=True)
    print("Submitted npm packages; registry visibility may lag behind accepted uploads")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("check", "check-pointers", "publish", "verify"))
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    run(args.command, args.directory)


if __name__ == "__main__":
    main()
