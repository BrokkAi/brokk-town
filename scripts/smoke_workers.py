#!/usr/bin/env python3
"""Initialize bundled worker servers and exercise parent loss; dispatch no work."""
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent

class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('worker', timeout=2)
        self.path = path
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(2)
        self.sock.connect(self.path)

for bot in json.loads((ROOT / 'bundle.json').read_text())['bots'].values():
    with tempfile.TemporaryDirectory(prefix='bt-worker-smoke-', dir='/tmp') as directory:
        path = str(Path(directory) / 'w.sock')
        read, write = os.pipe()
        # fd 3 is the worker protocol's inherited parent-liveness descriptor.
        def setup():
            if read != 3:
                os.dup2(read, 3)
        process = subprocess.Popen([str(ROOT / 'bin' / bot['command']), 'worker', '--socket', path],
                                   env=dict(os.environ, BROKK_TOWN_PARENT_PIPE='1'),
                                   pass_fds=tuple({read, 3}), preexec_fn=setup,
                                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        os.close(read)
        try:
            deadline = time.monotonic() + 10
            while True:
                connection = UnixConnection(path)
                try:
                    connection.request('GET', '/v1/initialize')
                    response = connection.getresponse()
                    info = json.loads(response.read())
                    assert response.status == 200
                    break
                except (OSError, http.client.HTTPException):
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise AssertionError(f'{bot["project"]} failed to initialize')
                    time.sleep(.03)
                finally:
                    connection.close()
            assert info['bot'] == bot['project'], info
            assert info['version'].removeprefix('v') == bot['version'], info
            os.close(write)
            write = None
            assert process.wait(timeout=10) == 0, process.stderr.read().decode()
        finally:
            if write is not None:
                os.close(write)
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stderr.close()
print('All eight bundled workers initialized with their exact versions and stopped on parent loss; no jobs dispatched.')
