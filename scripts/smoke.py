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


def pid_alive(pid):
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


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
        initial_capacity = snapshot()['capacity']
        assert initial_capacity['limit'] == 4 and initial_capacity['active'] >= 0
        capacity_cli = subprocess.check_output([binary, 'capacity', '--demo',
            '--state-dir', directory, '--max-workers', '2'], text=True)
        assert 'limit 2' in capacity_cli
        assert snapshot()['service_config']['max_workers'] == 2
        assert snapshot()['capacity']['limit'] == 2
        invalid_capacity = subprocess.run([binary, 'capacity', '--demo',
            '--state-dir', directory, '--max-workers', '65'],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert invalid_capacity.returncode != 0 and '--max-workers' in invalid_capacity.stderr
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
        # Each bot can select its own harness and model without changing its neighbors.
        for role, harness, model, effort in [
                ('review', 'claude', 'review-model', 'xhigh'),
                ('issue', 'codex', 'issue-model', 'xhigh'),
                ('release', 'opencode', 'release-model', '')]:
            subprocess.run([binary, 'settings', '--demo', '--state-dir', directory,
                            '--repo', 'BrokkAi/orchard', '--role', role,
                            '--harness', harness, '--model', model, '--effort', effort], check=True)
            with request('/api/choices', {'town': 'brokkai/orchard', 'role': role, 'agent': {}}) as response:
                assert json.load(response)['models'][0]['value'] == 'demo-model'
        profiles = snapshot()['towns']['brokkai/orchard']['config']['bot_agents']
        assert profiles['review']['harness'] == 'claude-acp' and profiles['review']['model'] == 'review-model'
        assert profiles['issue']['harness'] == 'codex-acp' and profiles['issue']['model'] == 'issue-model'
        assert profiles['release']['harness'] == 'opencode' and profiles['release']['effort'] == ''
        assert not any(profiles[role]['inherited'] for role in ['review', 'issue', 'release'])
        subprocess.run([binary, 'settings', '--demo', '--state-dir', directory,
                        '--repo', 'BrokkAi/orchard', '--model', 'new-default'], check=True)
        updated = snapshot()['towns']['brokkai/orchard']['config']['bot_agents']
        assert all(updated[role] == profiles[role] for role in ['review', 'issue', 'release'])
        assert updated['bug']['inherited'] and updated['bug']['model'] == 'new-default'
        assert updated['feature']['inherited'] and updated['feature']['model'] == 'new-default'
        subprocess.run([binary, 'settings', '--demo', '--state-dir', directory,
                        '--repo', 'BrokkAi/orchard', '--role', 'review', '--inherit'], check=True)
        reset = snapshot()['towns']['brokkai/orchard']['config']['bot_agents']
        assert reset['review']['inherited'] and reset['review']['model'] == 'new-default'
        assert reset['issue'] == profiles['issue'] and reset['release'] == profiles['release']
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
        assert state['service_config']['max_workers'] == 2
        assert state['capacity']['limit'] == 2
        assert state['towns']['brokkai/orchard']['config'] == managed['config']
        assert state['towns']['brokkai/orchard']['requests'][receipt['id']]['status'] == 'confirmed'
        saved = json.loads((root / 'demo' / 'state.json').read_text())
        assert saved['towns']['brokkai/paper-trail']['deleted']
        assert all(not w['enabled'] for w in saved['towns']['brokkai/paper-trail']['workers'].values())

        # A foreground service names the process that holds the state directory.
        clash = subprocess.run([binary, 'serve', '--demo', '--state-dir', directory,
                                '--listen', '127.0.0.1:0'], text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
        assert clash.returncode != 0 and f'pid {conn["pid"]}' in clash.stderr, clash.stderr
        # Closing the terminal or dropping an SSH session runs the same shutdown
        # sequence as Ctrl+C, rather than killing the service where it stands.
        service.send_signal(signal.SIGHUP)
        service.wait(timeout=10)
        assert service.returncode == 0 and not conn_path.exists()

        service = subprocess.Popen([binary, 'serve', '--demo', '--state-dir', directory,
                                    '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
        wait_for(conn_path.exists)
        service.send_signal(signal.SIGTERM)
        service.wait(timeout=10)
        assert service.returncode == 0 and not conn_path.exists()

        # Any client command starts the town on demand, detached from the
        # terminal, and the access key survives restarts.
        on_demand = subprocess.run([binary, 'status', '--demo', '--state-dir', directory,
                                    '--listen', '127.0.0.1:0'], text=True, check=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=40)
        assert 'starting the town service' in on_demand.stderr, on_demand.stderr
        assert json.loads(on_demand.stdout)['demo'] is True
        detached = json.loads(conn_path.read_text())
        assert detached['pid'] != conn['pid'] and detached['token'] == conn['token']
        assert detached['version'] and detached['executable'] and not detached.get('managed')
        assert (root / 'demo' / 'logs' / 'serve.err.log').stat().st_mode & 0o777 == 0o600
        status_out = subprocess.check_output([binary, 'service', 'status', '--demo',
            '--state-dir', directory], text=True, stderr=subprocess.DEVNULL, timeout=20)
        assert 'never (demo)' in status_out and 'started on demand' in status_out, status_out
        # A crash heals on the next command with the same browser address.
        os.kill(detached['pid'], signal.SIGKILL)
        wait_for(lambda: not pid_alive(detached['pid']))
        web = subprocess.run([binary, 'web', '--demo', '--state-dir', directory,
                              '--listen', '127.0.0.1:0'], text=True, check=True,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=40)
        healed = json.loads(conn_path.read_text())
        assert healed['pid'] != detached['pid'] and pid_alive(healed['pid'])
        assert web.stdout.strip() == healed['url'] + '/#token=' + conn['token'], web.stdout
        # An explicit restart re-executes in place, keeping the PID.
        restart = subprocess.check_output([binary, 'service', 'restart', '--demo',
            '--state-dir', directory], text=True, stderr=subprocess.DEVNULL, timeout=40)
        restarted = json.loads(conn_path.read_text())
        assert f'pid {healed["pid"]}' in restart and restarted['pid'] == healed['pid']
        assert restarted['started'] > healed['started'], (restarted, healed)
        stop = subprocess.check_output([binary, 'service', 'stop', '--demo',
            '--state-dir', directory], text=True, stderr=subprocess.DEVNULL, timeout=40)
        assert 'Stopped' in stop and not conn_path.exists()
        wait_for(lambda: not pid_alive(healed['pid']))
        print('Demo smoke passed: assets, auth, SSE, registry, supplemental harnesses, per-bot profiles/defaults/reset, pinned settings, issue submission/retry, PTY controls, delete/cancel, paste, resize, detach, hangup shutdown, restart recovery, on-demand start, crash recovery, stable access key, in-place restart.')
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
        if conn_path.exists():
            # A detached service started on demand outlives this script.
            try:
                os.kill(json.loads(conn_path.read_text())['pid'], signal.SIGTERM)
            except (OSError, ValueError, KeyError):
                pass
