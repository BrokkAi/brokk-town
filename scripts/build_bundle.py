#!/usr/bin/env python3
"""Build each independent module into one local Town installation."""
import argparse
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent

def bots():
    return json.loads((ROOT / "bundle.json").read_text())["bots"]

def executables():
    return {"bt", *(bot["command"] for bot in bots().values())}

def legal_files():
    files = {}
    for bot in bots().values():
        for name in ("LICENSE", "NOTICE", "licenses/THIRD_PARTY_NOTICES.txt"):
            files[f"bots/{bot['project']}/{name}"] = (ROOT / "bots" / bot["project"] / name).read_bytes()
    return files

def build(output, version="dev", target=None):
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, CGO_ENABLED="0", GOWORK="off", GOFLAGS="-mod=readonly")
    if target:
        env["GOOS"], env["GOARCH"] = target.split("-")
    projects = [(ROOT, "bt", version)] + [(ROOT / "bots" / b["project"], b["command"], "v" + b["version"]) for b in bots().values()]
    for directory, command, version in projects:
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", f"-ldflags=-s -w -X main.version={version}", "-o", str(output / command), f"./cmd/{command}"], cwd=directory, env=env, check=True)
    (output / "bundle.json").write_bytes((ROOT / "bundle.json").read_bytes())

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="bin", type=Path)
    parser.add_argument("--version", default="dev")
    parser.add_argument("--target")
    args = parser.parse_args()
    build(args.output, args.version, args.target)
