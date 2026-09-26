#!/usr/bin/env python3
"""Build Town alone; independently released bots run through npx."""
import argparse
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent.parent


def build(output, version="dev", target=None):
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, CGO_ENABLED="0", GOWORK="off", GOFLAGS="-mod=readonly")
    if target:
        env["GOOS"], env["GOARCH"] = target.split("-")
    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false",
                    f"-ldflags=-s -w -X main.version={version}",
                    "-o", str(output / "bt"), "./cmd/bt"], cwd=ROOT, env=env, check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="bin", type=Path)
    parser.add_argument("--version", default="dev")
    parser.add_argument("--target")
    args = parser.parse_args()
    build(args.output, args.version, args.target)
