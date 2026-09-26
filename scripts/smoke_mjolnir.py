#!/usr/bin/env python3
"""Exercise Town's passive catalog against a fake daemon, with zero real towns."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "bin/bt").resolve())
options = {
    "revision": 7,
    "profiles": [{"id": "fixture", "harness": "fake"}],
    "targets": [{"id": "implied-local", "kind": "local-bare", "availability": "ready",
                 "requires_project_directory": True}],
    "bundles": [],
    "default": {"target_id": "implied-local", "profile_id": "fixture"},
}


class Daemon(BaseHTTPRequestHandler):
    calls = 0
    unavailable = False

    def log_message(self, *args):
        pass

    def do_GET(self):
        assert self.path == "/api/v1/options"
        assert self.headers.get("Authorization") == "Bearer fixture-token"
        Daemon.calls += 1
        self.send_response(503 if Daemon.unavailable else 200)
        self.send_header("Mj-Api-Version", "1")
        self.end_headers()
        self.wfile.write(json.dumps({"error": "private daemon output"} if Daemon.unavailable else options).encode())


def wait_for(read, predicate):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        value = read()
        if predicate(value):
            return value
        time.sleep(.05)
    raise AssertionError("timed out waiting for fixture state")


with tempfile.TemporaryDirectory(prefix="town-mjolnir-") as directory:
    root = Path(directory)
    token = root / "mj-token"
    token.write_text("fixture-token")
    token.chmod(0o600)
    daemon = ThreadingHTTPServer(("127.0.0.1", 0), Daemon)
    thread = threading.Thread(target=daemon.serve_forever, daemon=True)
    thread.start()
    env = dict(os.environ, BT_MJOLNIR_API_URL=f"http://127.0.0.1:{daemon.server_port}/api/v1",
               BT_MJOLNIR_TOKEN_FILE=str(token))

    def run_town(demo=False, restart=False):
        args = [binary, "--state-dir", str(root / "town"), "--listen", "127.0.0.1:0"]
        if demo:
            args.append("--demo")
        state = root / "town" / ("demo" if demo else "")
        conn_path = state / "connection.json"
        conn_path.unlink(missing_ok=True)
        before = Daemon.calls
        process = subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL)
        try:
            wait_for(lambda: conn_path.exists(), bool)
            conn = json.loads(conn_path.read_text())

            def request(path, body=None):
                req = Request(conn["url"] + path,
                              data=None if body is None else json.dumps(body).encode(),
                              headers={"Authorization": "Bearer " + conn["token"], "Content-Type": "application/json"})
                with urlopen(req, timeout=2) as response:
                    return json.load(response)

            if demo:
                listing = request("/api/execution-options/refresh", {})
                assert listing["demo"] and not listing["configured"] and not listing["targets"]
                subprocess.run([binary, "execution", "--demo", "--json", "--state-dir", str(root / "town")],
                               check=True, env=env, stdout=subprocess.DEVNULL)
                assert Daemon.calls == before, "demo contacted Mjolnir"
                return
            assert request("/api/state")["towns"] == {}, "integration fixture must have no repository automation"
            listing = wait_for(lambda: request("/api/execution-options"), lambda v: bool(v["targets"]))
            assert listing["targets"] == options["targets"]
            if restart:
                request("/api/execution-options/refresh", {})
                listing = wait_for(lambda: request("/api/execution-options"), lambda v: bool(v.get("error")))
                assert listing["stale"] and listing["targets"] == options["targets"]
                assert "private daemon output" not in json.dumps(listing)
            else:
                cli = subprocess.check_output([binary, "execution", "--json", "--state-dir", str(state)], env=env, text=True)
                assert json.loads(cli)["targets"] == listing["targets"]
                assert "fixture-token" not in (state / "mjolnir-options.json").read_text()
        finally:
            process.terminate()
            process.wait(timeout=10)

    try:
        run_town()
        Daemon.unavailable = True
        run_town(restart=True)
        run_town(demo=True)
    finally:
        daemon.shutdown()
        daemon.server_close()
        thread.join(timeout=2)
print("Mjolnir catalog, offline restart, CLI and demo isolation smoke passed")
