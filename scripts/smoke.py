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
        for action in ['pause', 'start']:
            subprocess.run([binary, action, '--demo', '--state-dir', directory,
                            '--repo', 'BrokkAi/orchard', '--role', 'feature'],
                           check=True, stdout=subprocess.DEVNULL)
            assert snapshot()['towns']['brokkai/orchard']['workers']['feature']['enabled'] == (action == 'start')
        for path in ['/', '/app.js', '/town.js', '/tools.js', '/manage.js', '/scenery.js', '/style.css',
                     '/assets/buildings-atlas.png', '/assets/actors-atlas.png',
                     '/assets/feature-study.png', '/assets/feature-reader.png']:
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

        catalog_cli = subprocess.check_output([binary, 'harnesses', '--demo',
            '--state-dir', directory, '--refresh'], text=True)
        with request('/api/harnesses') as response:
            catalog = json.load(response)
        agents = {a['id']: a for a in catalog['agents']}
        assert len(agents) >= 33 and catalog['demo']
        for harness in ['brokkai/anvil', 'brokkai/muse-acp', 'foundev/draupnir']:
            assert harness in catalog_cli and agents[harness]['version'] == 'installed'
            subprocess.run([binary, 'settings', '--demo', '--state-dir', directory,
                '--repo', 'BrokkAi/orchard', '--harness', harness], check=True,
                stdout=subprocess.DEVNULL)
            with request('/api/choices', {'town': 'brokkai/orchard', 'agent': {}}) as response:
                assert json.load(response)['models'][0]['value'] == 'demo-model'
            assert snapshot()['towns']['brokkai/orchard']['config']['harness'] == harness

        subprocess.run([binary, 'settings', '--demo', '--state-dir', directory,
                        '--repo', 'BrokkAi/orchard', '--harness', 'claude',
                        '--model', 'demo-model', '--effort', 'high'], check=True)
        with request('/api/choices', {'town': 'brokkai/orchard', 'agent': {}}) as response:
            assert json.load(response)['models'][0]['value'] == 'demo-model'
        issue_body = root / 'request.md'
        issue_body.write_text('Steps:\n1. Keep literal `code` and $(text).\n2. Preserve the selection.\n')
        submit = [binary, 'request', '--demo', '--state-dir', directory,
                  '--repo', 'BrokkAi/orchard', '--kind', 'bug', '--title', 'Demo operator bug',
                  '--body-file', str(issue_body), '--request-id', '00112233445566778899aabbccddeeff']
        for _ in range(2):
            subprocess.run(submit, check=True, stdout=subprocess.DEVNULL)
        managed = snapshot()['towns']['brokkai/orchard']
        assert managed['config']['harness'] == 'claude-acp' and managed['config']['effort'] == 'high'
        assert managed['config']['harness_version'] == agents['claude-acp']['version']
        assert len(managed['requests']) == 1
        receipt = managed['requests']['00112233445566778899aabbccddeeff']
        assert receipt['status'] == 'confirmed' and not receipt.get('url')
        assert receipt['body'] == issue_body.read_text().strip()
        assert managed['tasks'][f"issue:{receipt['number']}"]['title'] == 'Demo operator bug'

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
                    chunk = os.read(master, 65536)
                    if not chunk:
                        return
                    output.extend(chunk)
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
        os.write(master, b'd')
        wait_for(lambda: (drain(), b'Delete brokkai/orchard?' in output)[1])
        os.write(master, b'n')
        wait_for(lambda: (drain(), b'Deletion canceled' in output)[1])
        assert len(snapshot()['towns']) == 2
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 12, 30, 0, 0))
        os.write(master, b's')
        wait_for(lambda: (drain(), snapshot()['towns']['brokkai/orchard']['workers']['bug']['enabled'])[1])
        os.write(master, b'\td')
        wait_for(lambda: (drain(), b'Delete brokkai/paper-trail?' in output)[1])
        os.write(master, b'y')
        wait_for(lambda: (drain(), 'brokkai/paper-trail' not in snapshot()['towns'])[1])
        os.write(master, b'q')
        # Keep consuming output while the TUI restores the screen. macOS PTYs
        # can fill their output buffer before process exit if the test stops
        # reading here; a real terminal continues draining it.
        wait_for(lambda: (drain(), terminal.poll() is not None)[1], timeout=3)
        drain()
        assert terminal.returncode == 0
        assert b'\x1b[?1049l' in output and b'\x1b[?25h' in output
        restored = termios.tcgetattr(slave)
        expected_settings, actual_settings = list(original), list(restored)
        if sys.platform == 'darwin':
            # XNU sets PENDIN when ICANON is restored, to reprocess typeahead.
            # It is transient kernel state, not a changed terminal setting:
            # https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/tty.c
            expected_settings[3] &= ~termios.PENDIN
            actual_settings[3] &= ~termios.PENDIN
        assert actual_settings == expected_settings, f'Terminal settings were not restored: before={original!r}, after={restored!r}'
        assert service.poll() is None, 'Detaching stopped the service'
        service.send_signal(signal.SIGTERM)
        service.wait(timeout=6)
        assert service.returncode == 0 and not conn_path.exists()
        # The lock is released and the isolated inventory can be reopened.
        service = subprocess.Popen([binary, 'serve', '--demo', '--state-dir', directory,
                                    '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
        wait_for(conn_path.exists)
        conn = json.loads(conn_path.read_text())
        state = snapshot()
        assert len(state['towns']) == 1
        assert state['towns']['brokkai/orchard']['config'] == managed['config']
        assert state['towns']['brokkai/orchard']['requests'][receipt['id']]['status'] == 'confirmed'
        saved = json.loads((root / 'demo' / 'state.json').read_text())
        assert saved['towns']['brokkai/paper-trail']['deleted']
        assert all(not w['enabled'] for w in saved['towns']['brokkai/paper-trail']['workers'].values())
        print('Demo smoke passed: assets, auth, SSE, registry, supplemental harnesses, pinned settings, issue submission/retry, PTY controls, delete/cancel, paste, resize, detach, restart recovery.')
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
