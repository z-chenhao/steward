#!/usr/bin/env python3
"""CLI -> Code Mode -> Security -> real HTTP/SSH adapter, with offline peers.

The explicit Lume/Guest Fake does not establish macOS or Jianying compatibility.
Those opt-in L2 results are recorded separately in docs/reports/computer-l2-result.json.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import selectors
import shutil
from contextlib import closing
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.request

from file_digest import sha256_file
from verify_capabilities import CapabilityHarness
from verify_main import require


class ComputerHarness(CapabilityHarness):
    def setup(self, model='fixture-model', metadata=None):
        if metadata is None:
            metadata = dict(name='Computer workload', provider_kind='openai_compatible',
                            endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions',
                            model_id=model, context_window=65536, max_output_tokens=4096,
                            compatibility={'tool_result_format':'openai'},
                            capabilities={'text_input':True,'image_input':True,'tool_calls':True,'streaming':True})
        return super().setup(model, metadata)

    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'),
                '-test-bridge', 'computer', *args]

    def guest_command(self, live_root, command):
        with closing(sqlite3.connect(Path(self.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
            instance = json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
        from lume_live import Live
        return Live(live_root, 'product', instance['native_name']).guest(command)

    def cancel_at(self, boundary, plan):
        """Cancel through Main while a Worker holds a real lifecycle boundary."""
        self.send('delegate-' + boundary, 'Delegate capabilities: ' + plan)
        release = self.root / ('release-' + boundary)
        child = self.hold(boundary, extra_flags=('-test-main-release', str(release)), timeout=30)
        held = self.command('computer', 'status')
        state = Path(self.env['STEWARD_DATA_ROOT']) / 'state.db'
        with closing(sqlite3.connect(state)) as db, db:
            task_id, ref = db.execute("SELECT task_id,task_ref FROM task WHERE status='active'").fetchone()
        self.send('cancel-' + boundary, 'Capabilities: ' + json.dumps([
            {'name': 'builtin__task_manage', 'arguments': {'operation': 'cancel', 'taskRef': ref}}]))
        control = subprocess.Popen(self.args('chat', 'work'), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, env=self.env, cwd=self.root)
        self.children.append(control)

        def stopped(_):
            with closing(sqlite3.connect(state)) as db, db:
                return db.execute('SELECT status FROM task WHERE task_id=?', (task_id,)).fetchone()[0]

        self.wait_state(lambda status: status == 'cancelled', 'Main cancelled Computer Task', reader=stopped, timeout=15)
        release.touch()
        child.communicate(timeout=30)
        _, stderr = control.communicate(timeout=30)
        require(control.returncode == 0, 'Main cancellation failed: ' + stderr)
        self.command('chat', 'work')
        return {'boundary': boundary, 'task_id': task_id, 'held': held, 'old_owner_exit': child.returncode,
                'after_cancel': self.command('computer', 'status')}

    def cancelled_state(self, task_id):
        with closing(sqlite3.connect(Path(self.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
            task = db.execute('SELECT status,task_epoch FROM task WHERE task_id=?', (task_id,)).fetchone()
            runs = db.execute('SELECT run_id,status,desired_state FROM run WHERE task_id=? ORDER BY run_id', (task_id,)).fetchall()
            codes = db.execute('SELECT s.execution_id,s.state,s.desired_state,s.claim_token IS NULL FROM code_execution_state s JOIN tool_invocation i ON i.invocation_id=s.execution_id JOIN run r USING(run_id) WHERE r.task_id=? ORDER BY s.execution_id', (task_id,)).fetchall()
            tools = db.execute('SELECT invocation_id,i.status FROM tool_invocation i JOIN run USING(run_id) WHERE task_id=? ORDER BY invocation_id', (task_id,)).fetchall()
        require(task[0] == 'cancelled' and runs and all(r[1] == 'cancelled' for r in runs), 'cancelled Task/Run revived')
        require(codes and all(c[1:] == ('cancelled', 'cancelled', 1) for c in codes), 'cancelled Code retained authority')
        require(all(t[1] in ('succeeded', 'failed', 'cancelled', 'unknown') for t in tools), 'cancelled nested work revived')
        return {'task': task, 'runs': runs, 'codes': codes, 'tools': tools}


def request(endpoint, path, value=None):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    data = None if value is None else json.dumps(value).encode()
    req = urllib.request.Request(endpoint + path, data=data,
                                 headers={'Content-Type': 'application/json'})
    with opener.open(req, timeout=10) as response:
        return json.load(response)


def run(binary):
    report = {'version': 1, 'scope': 'offline lifecycle HTTP/SSH and durable CLI contracts; not real VM compatibility',
              'checks': [], 'crashes': [], 'cleanup': False}
    with tempfile.TemporaryDirectory(prefix='steward-computer-') as temp:
        root = Path(temp).resolve()
        vm_root, source = root / 'vm', root / 'source'
        vm_root.mkdir(mode=0o700)
        source.mkdir(mode=0o700)
        for name in ('disk.img', 'nvram.bin', 'config.json'):
            (source / name).write_text('base-' + name)
        (source / 'config.json').write_text(json.dumps({'macAddress': '02:00:00:00:00:01', 'machineIdentifier': 'fixture-base', 'cpuCount': 4}))
        h = ComputerHarness(binary, root, fixture='computer')
        h.env['STEWARD_COMPUTER_FIXTURE_PASSWORD'] = 'fixture-only'
        server = subprocess.Popen([str(binary), '-test-computer-server', str(vm_root)],
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                  env=h.env, cwd=root)
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(server.stdout, selectors.EVENT_READ)
                require(selector.select(10), 'doctor: fixture HTTP/SSH peer not ready')
            line = server.stdout.readline()
            require(line, 'doctor: fixture peer exited')
            computer = json.loads(line)
            node = shutil.which('node')
            require(node, 'doctor: Node required')
            script = Path(__file__).resolve().parents[2] / 'runtime/javascript/src/sidecar.mjs'
            config = {'version': 1, 'javascript': {'command': node, 'script': str(script)},
                      'computer': computer}
            config_path = root / 'config.yaml'
            config_path.write_text(json.dumps(config))
            require(h.command('computer', 'prepare-base', str(source))['base'] == 'sealed', 'base not sealed')
            h.setup()
            artifact = h.command('artifact', 'save', 'computer-proof.txt', 'text/plain', text='stable Artifact across VM instances')
            computer['manifest']['artifact_refs'] = [artifact['artifact_ref']]
            computer['manifest']['applications'] = [{'bundle_id': 'com.apple.TextEdit', 'documents': ['/Users/lume/work.txt']}]
            config_path.write_text(json.dumps(config))
            plan = json.dumps([{'name': 'builtin__codemode_execute', 'arguments': {'source': 'return await cua.getState();'}}])
            h.send('first', 'Delegate capabilities: ' + plan)
            child = h.hold('computer_ready')
            before = h.command('computer', 'status')
            contents = {'Disk': 'synthetic user file content', 'NVRAM': 'changed-NVRAM', 'Config': 'changed-config'}
            request(computer['endpoint'], '/fixture/write', contents)
            child.kill()
            child.communicate(timeout=10)
            require(child.returncode < 0, 'fault process did not die abnormally')
            h.command('chat', 'work')
            after = h.command('computer', 'status')
            require(before['computer_id'] == after['computer_id'] and after['instance_epoch'] == before['instance_epoch'] + 1,
                    'Host restart changed Computer ID or failed to fence old instance')
            require(after['phase'] == 'checkpointed', 'idle did not commit checkpoint and release instance')
            expected = {'disk.img': contents['Disk'], 'nvram.bin': contents['NVRAM'], 'config.json': contents['Config']}

            def evidence(state):
                checkpoint = vm_root / 'checkpoints' / state['checkpoint']['checkpoint_id']
                receipt = json.loads((checkpoint / 'receipt.json').read_text())
                hashes = {}
                for name, text in expected.items():
                    payload = (checkpoint / name).read_bytes()
                    require(payload == text.encode(), 'inconsistent checkpoint unit: ' + name)
                    hashes[name] = hashlib.sha256(payload).hexdigest()
                    require(receipt['files'][name] == hashes[name], 'checkpoint receipt mismatch')
                require(set(p.name for p in checkpoint.iterdir()) == {*expected, 'receipt.json'}, 'session/heap files entered checkpoint')
                require(receipt['manifest']['artifact_refs'] == [artifact['artifact_ref']], 'stable Artifact reference lost')
                require(not list((vm_root / 'vms').glob('steward-*')), 'disposable instance retained')
                require(not list((vm_root / 'vms').glob('.stage-*')) and not list((vm_root / 'checkpoints').glob('.stage-*')), 'abandoned staging unit retained')
                require(request(computer['endpoint'], '/fixture/status')['running'] == 0, 'CPU/RAM lock not released')
                opened = subprocess.run(h.args('artifact', 'open', artifact['artifact_ref']), env=h.env,
                                        capture_output=True, text=True, timeout=20)
                require(opened.returncode == 0 and opened.stdout == 'stable Artifact across VM instances', 'Artifact bytes/identity lost')
                with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
                    phases = db.execute('SELECT kind,status FROM computer_operation ORDER BY rowid').fetchall()
                    nested = db.execute("SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__computer_discover' AND parent_invocation_id IS NOT NULL AND status='succeeded'").fetchone()[0]
                    require(nested >= 1, 'Computer bypassed ToolInvocation')
                    require(not db.execute('PRAGMA foreign_key_check').fetchone(), 'foreign key integrity failed')
                return {'state': state, 'files': hashes, 'manifest': receipt['manifest'], 'operations': phases,
                        'nested_tool_invocations': nested}

            report['checks'].append(evidence(after))
            report['crashes'].append({'boundary': 'computer_ready', 'native_exit': child.returncode,
                                      'before': before, 'after': after})
            for index, boundary in enumerate(('computer_before_checkpoint_publish', 'computer_after_checkpoint_publish',
                                               'computer_after_destroy'), 2):
                h.send(str(index), 'Delegate capabilities: ' + plan)
                child = h.hold(boundary)
                held = h.command('computer', 'status')
                if boundary == 'computer_after_destroy':
                    require(held['phase'] == 'checkpointing' and held['checkpoint'] is not None,
                            'post-disposal boundary lacks committed progress')
                    require(not list((vm_root / 'vms').glob('steward-*')), 'post-disposal instance still exists')
                child.kill()
                child.communicate(timeout=10)
                h.command('chat', 'work')
                recovered = h.command('computer', 'status')
                require(recovered['computer_id'] == before['computer_id'] and recovered['phase'] == 'checkpointed', 'checkpoint crash lost identity/cleanup')
                if boundary == 'computer_after_destroy':
                    require(recovered['checkpoint'] == held['checkpoint'], 'disposed checkpoint replaced during recovery')
                report['checks'].append(evidence(recovered))
                report['crashes'].append({'boundary': boundary, 'native_exit': child.returncode,
                                          'before': held, 'after': recovered})
            # Prove a new cell can use the recovered Computer, rather than only
            # observing a terminal phase left by the interrupted cell.
            h.send('fresh-after-disposal', 'Delegate capabilities: ' + plan)
            h.command('chat', 'work')
            fresh = h.command('computer', 'status')
            require(fresh['computer_id'] == recovered['computer_id'] and fresh['instance_epoch'] == recovered['instance_epoch'] + 1,
                    'fresh cell cannot restore after interrupted disposal')
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
                results = db.execute("SELECT status FROM tool_invocation WHERE tool_name='builtin__codemode_execute' ORDER BY created_at").fetchall()
                require(len(results) == 5 and all(row[0] == 'succeeded' for row in results),
                        'resumed or fresh cell failed after disposal')
            report['checks'].append(evidence(fresh))
            # A new authorized invocation must reconcile abandoned idle work;
            # cancellation leaves the original Task/Run/Code terminal forever.
            report['cancellations'] = []
            for boundary in ('computer_ready', 'computer_after_destroy'):
                cancelled = h.cancel_at(boundary, plan)
                held = cancelled['held']
                terminal = h.cancelled_state(cancelled['task_id'])
                if boundary == 'computer_after_destroy':
                    require(held['phase'] == 'checkpointing' and held['checkpoint'] is not None,
                            'cancel boundary lacks committed checkpoint')
                    require(not list((vm_root / 'vms').glob('steward-*')), 'cancelled source instance retained')
                    cancelled['committed_unit'] = evidence(held)
                h.send('fresh-after-cancel-' + boundary, 'Delegate capabilities: ' + plan)
                h.command('chat', 'work')
                restored = h.command('computer', 'status')
                require(restored['computer_id'] == held['computer_id'] and restored['instance_epoch'] == held['instance_epoch'] + 1,
                        'fresh invocation lost cancelled Computer identity/epoch')
                require(restored['phase'] == 'checkpointed', 'fresh invocation failed after Task cancellation')
                require(h.cancelled_state(cancelled['task_id']) == terminal, 'recovery changed cancelled terminal state')
                with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
                    status = db.execute("SELECT status FROM tool_invocation WHERE tool_name='builtin__codemode_execute' ORDER BY created_at DESC LIMIT 1").fetchone()[0]
                    require(status == 'succeeded', 'fresh cell failed after cancellation')
                    if boundary == 'computer_after_destroy':
                        cp = db.execute("SELECT checkpoint_json FROM computer_operation WHERE kind='ensure' ORDER BY rowid DESC LIMIT 1").fetchone()[0]
                        require(json.loads(cp) == held['checkpoint'], 'takeover replaced disposed checkpoint')
                cancelled.update(terminal=terminal, after_fresh=restored)
                report['cancellations'].append(cancelled)
                report['checks'].append(evidence(restored))
            report['peer'] = request(computer['endpoint'], '/fixture/status')
            require(report['peer']['running'] == 0 and report['peer']['reopens'] >= 3, 'Guest session/app intent not restored')
        except Exception:
            print(json.dumps({'computer_failure': h.failure_state()}, ensure_ascii=False), file=sys.stderr)
            raise
        finally:
            h.cleanup()
            server.terminate()
            server.communicate(timeout=10)
            require(server.returncode == 0, 'fixture server cleanup failed')
        report['cleanup'] = True
    return report


def run_live(binary, live_root, config_path):
    from lume_live import ISSUE, MARKER, VM
    live_root = live_root.absolute()
    require(not any(p.is_symlink() for p in [live_root, *live_root.parents]), 'live root must be an owned plain directory')
    require(json.loads((live_root / MARKER).read_text()) == {'issue_id': ISSUE, 'vm': VM}, 'missing explicit L2 ownership')
    require(config_path.absolute().parent == live_root and not config_path.is_symlink(), 'live config must be in the owned root')
    config = json.loads(config_path.read_text())
    require(config['computer']['root'] == str(live_root / 'product'), 'live adapter must use the dedicated product store')
    require(config['computer']['guest']['password_ref'] == 'env:STEWARD_COMPUTER_GUEST_TEST_LOGIN', 'live fixture uses only the public test Guest login')
    root = live_root / 'product-cli'
    root.mkdir(mode=0o700, exist_ok=True)
    h = ComputerHarness(binary, root, fixture='computer')
    h.env['STEWARD_COMPUTER_GUEST_TEST_LOGIN'] = 'lume'
    path = '/Users/lume/steward24-product-fixture.txt'
    payload = 'STEWARD-24 production Provider durable fixture.\n'
    expected = hashlib.sha256(payload.encode()).hexdigest()
    config['computer']['manifest'] = {'version': 1, 'applications': [{'bundle_id': 'com.apple.TextEdit', 'documents': [path]}], 'artifact_refs': []}
    target = root / 'config.yaml'
    if target.exists():
        saved = json.loads(target.read_text())
        require(saved['computer']['root'] == str(live_root / 'product') and saved['computer']['guest'] == config['computer']['guest'], 'resumed test configuration changed')
        config = saved
    else:
        target.write_text(json.dumps(config))
    report = {'version': 1, 'scope': 'real CLI/Code Mode/Lume/macOS Guest production lifecycle; deterministic model fixture only',
              'guest_file_sha256': expected, 'checks': [], 'cleanup': False}
    script = Path(__file__).resolve().parent / 'lume_live.py'
    plan = json.dumps([{'name': 'builtin__codemode_execute', 'arguments': {'source': 'return await cua.getState();'}}])

    def guest(command):
        with closing(sqlite3.connect(root / 'data/state.db')) as db, db:
            instance = json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
        result = subprocess.run([shutil.which('python3'), str(script), '--root', str(live_root),
                                 '--profile', 'product', '--vm', instance['native_name'], 'guest', command],
                                capture_output=True, text=True, timeout=80)
        require(result.returncode == 0, 'owned Guest fixture proof failed: ' + result.stderr)
        return result.stdout

    def hardware_identity():
        with closing(sqlite3.connect(root / 'data/state.db')) as db, db:
            instance = json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
        cfg = json.loads((live_root / 'product/vms' / instance['native_name'] / 'config.json').read_text())
        return {key: cfg[key] for key in ('macAddress', 'machineIdentifier')}

    def prove_reopened(digest):
        output = guest('/usr/bin/shasum -a 256 ' + path)
        windows = json.loads(guest("/Applications/CuaDriver.app/Contents/MacOS/cua-driver call list_windows '{}'"))
        titles = [w.get('title') for w in windows.get('windows', []) if w.get('app_name') == 'TextEdit']
        require(digest in output and any('steward24-product-fixture.txt' in title for title in titles if title), 'Guest file or semantic document restoration failed')
        return {'file_readback': output.strip(), 'TextEdit_window_titles': titles}

    try:
        if h.command('status')['agent_ingress_enabled']:
            refs = config['computer']['manifest']['artifact_refs']
            require(len(refs) == 1, 'resume requires the frozen fixture Artifact')
            artifact = h.command('artifact', 'describe', refs[0])
        else:
            h.setup()
            artifact = h.command('artifact', 'save', 'computer-proof.txt', 'text/plain', text=payload)
            config['computer']['manifest']['artifact_refs'] = [artifact['artifact_ref']]
            target.write_text(json.dumps(config))
        # Resume this explicitly owned fixture without rewriting its frozen
        # requests or abandoning an accepted lifecycle operation.
        prior = h.command('computer', 'status')
        h.command('chat', 'work')
        report['prior_state'] = prior
        attempts = root / 'attempt.json'
        attempt = (json.loads(attempts.read_text()) if attempts.exists() else 0) + 1
        attempts.write_text(json.dumps(attempt))
        release = root / 'release'
        release.unlink(missing_ok=True)
        started = time.monotonic()
        h.send(f'live-{attempt}-fresh', 'Delegate capabilities: ' + plan)
        child = h.hold('computer_ready', extra_flags=('-test-main-release', str(release)), timeout=295)
        first = h.command('computer', 'status')
        identity = hardware_identity()
        base = json.loads((live_root / 'product/base/config.json').read_text())
        require(all(identity[key] != base[key] for key in identity), 'initial Computer reused base MAC or machine identity')
        report['hardware_identity_sha256'] = hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest()
        output = guest("printf '%s\\n' 'STEWARD-24 production Provider durable fixture.' > " + path + '; /usr/bin/shasum -a 256 ' + path + '; /bin/test ! -e /Users/lume/steward24-fixtures; /bin/test ! -e /Users/lume/Movies/steward24-live')
        require(expected in output, 'Guest fixture write hash mismatch')
        release.touch()
        stdout, stderr = child.communicate(timeout=295)
        require(child.returncode == 0, 'first native checkpoint failed: ' + stderr)
        cold = h.command('computer', 'status')
        require(cold['phase'] == 'checkpointed', 'first native checkpoint did not release the instance')
        report['checks'].append({'action': 'allocate-write-graceful-checkpoint-destroy', 'before': first, 'after': cold, 'guest': output,
                                 'elapsed_seconds': round(time.monotonic() - started, 3)})

        release.unlink()
        h.send(f'live-{attempt}-restore-and-crash', 'Delegate capabilities: ' + plan)
        child = h.hold('computer_ready', timeout=295)
        second = h.command('computer', 'status')
        require(hardware_identity() == identity, 'restore changed logical Computer hardware identity')
        reopened = prove_reopened(expected)
        changed = payload + 'Saved after the last checkpoint, before Agent SIGKILL.\n'
        changed_hash = hashlib.sha256(changed.encode()).hexdigest()
        written = guest("printf '%s\\n' 'STEWARD-24 production Provider durable fixture.' 'Saved after the last checkpoint, before Agent SIGKILL.' > " + path + '; /usr/bin/shasum -a 256 ' + path)
        require(changed_hash in written, 'uncheckpointed Guest fixture write failed')
        child.kill()
        child.communicate(timeout=10)
        require(child.returncode < 0, 'Agent fault did not terminate the owned child')
        report['checks'].append({'action': 'restore-login-driver-app-document', 'state': second, 'guest': reopened, 'native_exit': child.returncode, 'saved_before_crash_sha256': changed_hash})
        child = h.hold('computer_ready', extra_flags=('-test-main-release', str(release)), timeout=295)
        third = h.command('computer', 'status')
        require(hardware_identity() == identity, 'crash recovery changed logical Computer hardware identity')
        reopened = prove_reopened(changed_hash)
        release.touch()
        stdout, stderr = child.communicate(timeout=295)
        require(child.returncode == 0, 'recovered native checkpoint failed: ' + stderr)
        final = h.command('computer', 'status')
        require(first['computer_id'] == second['computer_id'] == third['computer_id'] == final['computer_id'], 'native restart changed logical identity')
        epoch = first['instance_epoch']
        require([first['instance_epoch'], second['instance_epoch'], third['instance_epoch']] == [epoch, epoch + 1, epoch + 2], 'native instance epochs were not fenced')
        require(final['phase'] == 'checkpointed' and not list((live_root / 'product/vms').glob('steward-*')), 'native instance or CPU/RAM retained')
        checkpoint = live_root / 'product/checkpoints' / final['checkpoint']['checkpoint_id']
        receipt = json.loads((checkpoint / 'receipt.json').read_text())
        hashes = {}
        for name in ('disk.img', 'nvram.bin', 'config.json'):
            hashes[name] = sha256_file(checkpoint / name)
            require(hashes[name] == receipt['files'][name], 'independent native unit digest mismatch')
        opened = subprocess.run(h.args('artifact', 'open', artifact['artifact_ref']), capture_output=True,
                                text=True, env=h.env, timeout=20)
        require(opened.returncode == 0 and opened.stdout == payload and receipt['manifest']['artifact_refs'] == [artifact['artifact_ref']], 'Artifact identity/content failed across native restart')
        report['checks'].append({'action': 'Host-SIGKILL-recover-checkpoint-destroy', 'state': final, 'guest': reopened,
                                 'checkpoint_files': hashes, 'artifact_ref': artifact['artifact_ref'], 'artifact_sha256': expected})

        release.unlink()
        h.send(f'live-{attempt}-post-disposal', 'Delegate capabilities: ' + plan)
        child = h.hold('computer_after_destroy', timeout=295)
        disposed = h.command('computer', 'status')
        require(disposed['phase'] == 'checkpointing' and disposed['checkpoint'] is not None and
                not list((live_root / 'product/vms').glob('steward-*')), 'native post-disposal boundary not reached')
        child.kill()
        child.communicate(timeout=10)
        require(child.returncode < 0, 'post-disposal fault did not terminate Agent')
        # The same hook also follows verification of an already absent instance.
        # Waiting through the maintained boundary allows full native disk hashing.
        child = h.hold('computer_after_destroy', extra_flags=('-test-main-release', str(release)), timeout=295)
        resumed = h.command('computer', 'status')
        require(resumed['checkpoint'] == disposed['checkpoint'], 'native recovery replaced committed checkpoint')
        release.touch()
        _, stderr = child.communicate(timeout=295)
        require(child.returncode == 0, 'native post-disposal recovery failed: ' + stderr)
        resumed = h.command('computer', 'status')
        require(resumed['phase'] == 'checkpointed', 'native disposal did not finish after takeover')
        report['checks'].append({'action': 'SIGKILL-after-disposal-before-final-commit', 'native_exit': -9,
                                 'before': disposed, 'after': resumed})

        release.unlink()
        h.send(f'live-{attempt}-fresh-after-disposal', 'Delegate capabilities: ' + plan)
        child = h.hold('computer_ready', extra_flags=('-test-main-release', str(release)), timeout=295)
        fresh = h.command('computer', 'status')
        require(hardware_identity() == identity and fresh['computer_id'] == first['computer_id'] and
                fresh['instance_epoch'] == disposed['instance_epoch'] + 1, 'fresh native cell lost restored identity')
        reopened = prove_reopened(changed_hash)
        release.touch()
        _, stderr = child.communicate(timeout=295)
        require(child.returncode == 0, 'fresh native cell failed after disposal recovery: ' + stderr)
        final = h.command('computer', 'status')
        require(final['phase'] == 'checkpointed' and not list((live_root / 'product/vms').glob('steward-*')),
                'fresh native cell retained resources')
        with closing(sqlite3.connect(root / 'data/state.db')) as db, db:
            results = db.execute("SELECT status FROM tool_invocation WHERE tool_name='builtin__codemode_execute' ORDER BY created_at DESC LIMIT 2").fetchall()
            require(len(results) == 2 and all(r[0] == 'succeeded' for r in results), 'native resumed/fresh Tool result failed')
        report['checks'].append({'action': 'fresh-cell-after-disposal-recovery', 'state': final, 'guest': reopened,
                                 'artifact_ref': artifact['artifact_ref']})
        report['cleanup'] = True
    finally:
        h.cleanup()
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, default=Path('evidence/computer.json'))
    parser.add_argument('--live-root', type=Path)
    parser.add_argument('--live-config', type=Path)
    args = parser.parse_args()
    require(bool(args.live_root) == bool(args.live_config), 'both live root and config are required')
    report = (run_live(args.binary.resolve(), args.live_root, args.live_config) if args.live_root else run(args.binary.resolve()))
    report['binary_sha256'] = sha256_file(args.binary)
    repository = Path(__file__).resolve().parents[2]
    revision = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=repository, capture_output=True, text=True, timeout=10)
    report['source_revision'] = revision.stdout.strip() if revision.returncode == 0 else None
    changes = subprocess.run(['git', 'diff', '--name-only', 'HEAD', '--', 'cmd', 'internal', 'go.mod', 'go.sum', 'runtime/javascript'],
                             cwd=repository, capture_output=True, text=True, timeout=10)
    report['source_changes'] = changes.stdout.splitlines() if changes.returncode == 0 else None
    report['recorded_at'] = datetime.now(timezone.utc).isoformat()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'evidence': str(args.evidence), 'checks': len(report['checks']), 'cleanup': report['cleanup']}))


if __name__ == '__main__':
    main()
