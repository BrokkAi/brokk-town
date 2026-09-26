#!/usr/bin/env python3
"""Check v1 and parent-loss through real offline npx packages; dispatch no work."""
import http.client
import json
import os
from pathlib import Path
import platform
import shutil
import socket
import subprocess
import tempfile
import time

from package_installers import npm_build_environment

ROOT = Path(__file__).resolve().parent.parent
VERSION = "0.0.0"
# Keep the v1 client contract explicit: a new API must not replace these.
V1_CAPABILITIES = {
    "bug-bot": {"policy", "run", "progress", "bug-scan"},
    "feature-bot": {"policy", "run", "progress", "feature-research", "feature-research-controls"},
    "issue-bot": {"policy", "run", "progress", "issue-result", "exact-issue", "requeue", "jobs", "retry-issue"},
    "review-bot": {"policy", "run", "progress", "exact-revision-review", "finding-severity"},
    "release-bot": {"policy", "run", "progress", "release", "retry"},
    "simplifier-bot": {"policy", "run", "progress", "simplifier-review"},
    "repo-bot": {"policy", "run", "progress", "repo-inventory", "branch-health"},
    "mayor-bot": {"policy", "run", "progress", "mayor-judgment", "mayor-bulletin"},
}


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("worker", timeout=2)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(2)
        self.sock.connect(self.path)


def run():
    system = {"Linux": "linux", "Darwin": "darwin"}[platform.system()]
    arch = {"x86_64": "x64", "amd64": "x64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
    with tempfile.TemporaryDirectory(prefix="bt-npm-", dir="/tmp") as temporary:
        root = Path(temporary)
        env = npm_build_environment(root)
        env["npm_config_offline"] = "true"
        env["npm_config_update_notifier"] = "false"
        installed = root / "installed"
        tarballs = []
        bots = []
        for project in sorted((ROOT / "bots").iterdir()):
            commands = list((project / "cmd").glob("*/main.go"))
            if len(commands) != 1:
                raise ValueError(f"expected one CLI in {project}")
            command = commands[0].parent.name
            name = "@brokkai/" + project.name
            native_name = f"{name}-{system}-{arch}"
            native = root / (project.name + "-native")
            (native / "bin").mkdir(parents=True)
            subprocess.run(["go", "build", "-ldflags=-X main.version=" + VERSION,
                            "-o", str(native / "bin" / command), "./cmd/" + command],
                           cwd=project, env=dict(env, GOWORK="off", GOFLAGS="-mod=readonly"), check=True)
            (native / "package.json").write_text(json.dumps({"name": native_name, "version": VERSION,
                                                            "os": [system], "cpu": [arch]}))
            launcher = root / project.name
            (launcher / "bin").mkdir(parents=True)
            shutil.copyfile(project / "npm" / (command + ".cjs"), launcher / "bin" / (command + ".cjs"))
            (launcher / "package.json").write_text(json.dumps({
                "name": name, "version": VERSION, "bin": {command: "bin/" + command + ".cjs"},
                "optionalDependencies": {native_name: VERSION}}))
            for package in (native, launcher):
                output = subprocess.check_output(["npm", "pack", "--ignore-scripts", "--json",
                                                  "--pack-destination", str(root)], cwd=package, env=env)
                records = json.loads(output)
                record = next(iter(records.values())) if isinstance(records, dict) else records[0]
                tarballs.append(str(root / record["filename"]))
            bots.append((project.name, name))
        subprocess.run(["npm", "install", "--offline", "--ignore-scripts", "--no-audit", "--no-fund",
                        "--prefix", str(installed), *tarballs], env=env, check=True, stdout=subprocess.DEVNULL)
        for project, package in bots:
            check_worker(root, installed, env, project, package)
    print("All eight npm workers served API v1 and stopped on parent loss through offline npx; no jobs dispatched.")


def check_worker(root, installed, env, project, package):
    directory = root / project
    path = str(directory / "w.sock")
    parent_path = str(directory / "p.sock")
    with socket.socket(socket.AF_UNIX) as listener:
        listener.bind(parent_path)
        listener.listen(1)
        listener.settimeout(15)
        command = ["npx", "--prefix=" + str(installed), "--offline", "--yes", "--", package + "@" + VERSION]
        version = subprocess.check_output([*command, "version"], cwd=installed, env=env, text=True).strip()
        assert version == VERSION, (project, version)
        process = subprocess.Popen([*command, "worker", "--socket", path], cwd=installed,
                                   env=dict(env, BROKK_TOWN_PARENT_SOCKET=parent_path),
                                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, start_new_session=True)
        parent = None
        try:
            parent, _ = listener.accept()
            deadline = time.monotonic() + 10
            while True:
                connection = UnixConnection(path)
                try:
                    connection.request("GET", "/v1/initialize")
                    response = connection.getresponse()
                    info = json.loads(response.read())
                    assert response.status == 200
                    break
                except (OSError, http.client.HTTPException):
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise AssertionError(f"{project} failed to initialize through npx")
                    time.sleep(.03)
                finally:
                    connection.close()
            assert info["bot"] == project, info
            assert info["version"] == VERSION, info
            assert info["minimum_protocol"] <= 1 <= info["protocol"], info
            assert V1_CAPABILITIES[project] <= set(info["capabilities"]), info
            assert "parent-socket" in info["capabilities"], info
            parent.close()
            parent = None
            assert process.wait(timeout=12) == 0, process.stderr.read().decode()
            assert not Path(path).exists(), f"{project} left its worker socket behind"
        finally:
            if parent is not None:
                parent.close()
            if process.poll() is None:
                import signal
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            process.stderr.close()


if __name__ == "__main__":
    run()
