#!/usr/bin/env python3
"""Exercise the demo service, browser API and CLI, without GitHub or agents."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
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
    service = subprocess.Popen([binary, '--demo', '--state-dir', directory,
                                '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
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
        hook_marker = root / 'demo-hook-must-not-run'
        with request('/api/attention-hook', {'enabled': True, 'command':
                [sys.executable, '-c', 'from pathlib import Path; Path(__import__("sys").argv[1]).touch()', str(hook_marker)]}) as response:
            assert json.load(response) == {'enabled': True, 'configured': True}
        assert str(hook_marker) not in json.dumps(snapshot())
        hook_cli = subprocess.check_output([binary, 'attention-hook', '--demo',
            '--state-dir', directory, '--json'], text=True)
        assert json.loads(hook_cli) == {'enabled': True, 'configured': True}
        storage_cli = subprocess.check_output([binary, 'storage', '--demo',
            '--state-dir', directory, '--repo', 'BrokkAi/orchard', '--json'], text=True)
        storage = json.loads(storage_cli)
        assert storage['town'] == 'brokkai/orchard' and storage['minimum_age_hours'] == 168
        refused_cleanup = subprocess.run([binary, 'storage', '--demo', '--state-dir', directory,
            '--repo', 'BrokkAi/orchard', '--cleanup', 'fixture'], text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert refused_cleanup.returncode != 0 and 'demo storage cleanup is disabled' in refused_cleanup.stderr
        initial_capacity = snapshot()['capacity']
        assert initial_capacity['limit'] == 4 and initial_capacity['active'] >= 0
        capacity_cli = subprocess.check_output([binary, 'settings', '--demo',
            '--state-dir', directory, '--max-workers', '2'], text=True)
        assert 'limit 2' in capacity_cli
        assert snapshot()['service_config']['max_workers'] == 2
        assert snapshot()['capacity']['limit'] == 2
        invalid_capacity = subprocess.run([binary, 'settings', '--demo',
            '--state-dir', directory, '--max-workers', '65'],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert invalid_capacity.returncode != 0 and '--max-workers' in invalid_capacity.stderr
        for action in ['pause', 'start']:
            subprocess.run([binary, action, '--demo', '--state-dir', directory,
                            '--repo', 'BrokkAi/orchard', '--role', 'feature'],
                           check=True, stdout=subprocess.DEVNULL)
            assert snapshot()['towns']['brokkai/orchard']['workers']['feature']['enabled'] == (action == 'start')
        for path in ['/', '/app.js', '/town.js', '/tools.js', '/manage.js', '/attention.js', '/storage.js', '/scenery.js', '/style.css',
                     '/assets/buildings-atlas.png', '/assets/actors-atlas.png',
                     '/assets/feature-study.png', '/assets/feature-reader.png',
                     '/assets/simplifier-clarifier.png']:
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

        subprocess.run([binary, "delete", "--demo", "--state-dir", directory, "--repo", "BrokkAi/paper-trail"], check=True)
        service.send_signal(signal.SIGTERM)
        service.wait(timeout=6)
        assert service.returncode == 0 and not conn_path.exists()
        # The lock is released and the isolated inventory can be reopened.
        service = subprocess.Popen([binary, '--demo', '--state-dir', directory,
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
        clash = subprocess.run([binary, '--demo', '--state-dir', directory,
                                '--listen', '127.0.0.1:0'], text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
        assert clash.returncode != 0 and f'pid {conn["pid"]}' in clash.stderr, clash.stderr
        # Closing the terminal or dropping an SSH session runs the same shutdown
        # sequence as Ctrl+C, rather than killing the service where it stands.
        service.send_signal(signal.SIGHUP)
        service.wait(timeout=10)
        assert service.returncode == 0 and not conn_path.exists()

        service = subprocess.Popen([binary, '--demo', '--state-dir', directory,
                                    '--listen', '127.0.0.1:0'], stdout=subprocess.DEVNULL)
        wait_for(conn_path.exists)
        service.send_signal(signal.SIGTERM)
        service.wait(timeout=10)
        assert service.returncode == 0 and not conn_path.exists()

        # A background start that fails is reported at once, not after a timeout.
        started = time.monotonic()
        failed = subprocess.run([binary, '-d', '--demo', '--state-dir', directory,
                                 '--listen', 'example.com:80'], text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
        assert failed.returncode != 0 and 'exited during startup' in failed.stderr, failed.stderr
        assert time.monotonic() - started < 10
        # Background startup is explicit, and its token survives restart.
        subprocess.run([binary, '-d', '--demo', '--state-dir', directory,
                        '--listen', '127.0.0.1:0'], check=True, stdout=subprocess.DEVNULL, timeout=40)
        detached = json.loads(conn_path.read_text())
        assert detached['token'] == conn['token']
        status_out = subprocess.check_output([binary, 'status', '--demo', '--state-dir', directory], text=True)
        assert 'running' in status_out
        subprocess.run([binary, 'shutdown', '--demo', '--state-dir', directory], check=True, timeout=40)
        assert not conn_path.exists()
        assert not hook_marker.exists(), 'demo invoked the attention hook'
        print('Demo smoke passed: browser assets, auth, SSE, registry, profiles, requests, foreground shutdown, explicit daemon lifecycle.')
    finally:
        if service.poll() is None:
            service.send_signal(signal.SIGTERM)
            service.wait(timeout=10)
        if conn_path.exists():
            # A detached service started on demand outlives this script.
            try:
                os.kill(json.loads(conn_path.read_text())['pid'], signal.SIGTERM)
            except (OSError, ValueError, KeyError):
                pass
