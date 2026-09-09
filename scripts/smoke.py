#!/usr/bin/env python3
"""Exercise the actual demo daemon and a PTY client, without GitHub or agents."""
import fcntl
import json
import os
from pathlib import Path
import pty
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time
import urllib.error
import urllib.request

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else 'bin/bt').resolve())


def wait_for(fn, timeout=6):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if fn():
            return
        time.sleep(.05)
    raise AssertionError('Timed out waiting for local demo state')


with tempfile.TemporaryDirectory(prefix='brokk-town-smoke-') as directory:
    root = Path(directory)
    conn_path = root / 'demo' / 'connection.json'
    service = subprocess.Popen([binary, 'serve', '--demo', '--state-dir', directory,
                                '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
    terminal = None
    master = slave = None
    try:
        wait_for(conn_path.exists)
        conn = json.loads(conn_path.read_text())

        def request(path, body=None, auth=True):
            req = urllib.request.Request(conn['url'] + path,
                data=json.dumps(body).encode() if body is not None else None,
                headers=({'Authorization': 'Bearer ' + conn['token']} if auth else {}) |
                        ({'Content-Type': 'application/json'} if body is not None else {}))
            return urllib.request.urlopen(req, timeout=3)

        def snapshot():
            with request('/api/state') as response:
                return json.load(response)

        wait_for(lambda: len(snapshot()['towns']) == 2)
        assert snapshot()['demo'] is True
        for path in ['/', '/app.js', '/town.js', '/tools.js', '/style.css',
                     '/assets/buildings-atlas.png', '/assets/actors-atlas.png']:
            with request(path, auth=False) as response:
                assert response.status == 200 and response.read()
        try:
            request('/api/state', auth=False)
            raise AssertionError('Unauthenticated snapshot succeeded')
        except urllib.error.HTTPError as error:
            assert error.code == 401
        with request('/api/events') as response:
            assert response.readline().startswith(b'id: ')
            assert response.readline().startswith(b'data: ')

        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 32, 100, 0, 0))
        original = termios.tcgetattr(slave)
        terminal = subprocess.Popen([binary, 'tui', '--state-dir', str(root / 'demo')],
                                    stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.set_blocking(master, False)
        output = bytearray()

        def drain():
            try:
                while True:
                    output.extend(os.read(master, 65536))
            except (BlockingIOError, OSError):
                pass

        wait_for(lambda: (drain(), b'ALL TOWNS' in output)[1])
        os.write(master, b'1p')
        wait_for(lambda: (drain(), not snapshot()['towns']['brokkai/orchard']['workers']['bug']['enabled'])[1])
        assert snapshot()['towns']['brokkai/paper-trail']['workers']['bug']['enabled']
        os.write(master, b'\x1b[200~asq\x1b[201~')
        time.sleep(.3)
        drain()
        assert terminal.poll() is None
        assert not snapshot()['towns']['brokkai/orchard']['workers']['bug']['enabled']
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 12, 30, 0, 0))
        os.write(master, b's')
        wait_for(lambda: (drain(), snapshot()['towns']['brokkai/orchard']['workers']['bug']['enabled'])[1])
        os.write(master, b'q')
        terminal.wait(timeout=3)
        drain()
        assert terminal.returncode == 0
        assert b'\x1b[?1049l' in output and b'\x1b[?25h' in output
        assert termios.tcgetattr(slave) == original, 'Terminal settings were not restored'
        assert service.poll() is None, 'Detaching stopped the service'
        service.send_signal(signal.SIGTERM)
        service.wait(timeout=6)
        assert service.returncode == 0 and not conn_path.exists()
        # The lock is released and the isolated inventory can be reopened.
        service = subprocess.Popen([binary, 'serve', '--demo', '--state-dir', directory,
                                    '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
        wait_for(conn_path.exists)
        conn = json.loads(conn_path.read_text())
        assert len(snapshot()['towns']) == 2
        print('Demo smoke passed: embedded assets, auth, SSE, two towns, PTY controls, paste, resize, detach, shutdown, restart.')
    finally:
        if terminal is not None and terminal.poll() is None:
            terminal.kill()
            terminal.wait()
        for fd in [master, slave]:
            if fd is not None:
                os.close(fd)
        if service.poll() is None:
            service.send_signal(signal.SIGTERM)
            service.wait(timeout=10)
