#!/usr/bin/env python3
"""Install a native release; drive bare steward through the maintained real PTY.

Launch/Doctor/Drive/Evidence/Cleanup includes actual Pi/QuickJS dependencies,
local JetStream, explicit model setup, Tool execution and SIGKILL/restart.
Only the model HTTP peer is deterministic. No online account or user state.
"""
import argparse
import hashlib
from http.server import ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
from contextlib import closing
import sqlite3
import tarfile
import tempfile
import threading

from terminal_driver import Terminal
from fixture_pool import run as owned_run, run_cases
from provider_endpoint import Endpoint
from verify_main import MainHarness, assert_final, require
from verify_tui import local_setup

ROOT = Path(__file__).resolve().parents[2]


class StartupHarness(MainHarness):
    def args(self, *args):
        return [str(self.binary), *args]

    def state(self):
        state = super().state()
        # Default startup also resumes independent Curator work. Its newly
        # frozen models are not replayed Main runs. Check ownership before
        # comparing every Run-owned snapshot and the complete execution ledgers.
        run_models = {run['model_snapshot_id'] for run in state['runs']}
        with closing(sqlite3.connect(Path(self.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
            curator_models = {row[0] for row in db.execute('SELECT model_snapshot_id FROM memory_generation')}
        require(all(snapshot['snapshot_id'] in run_models | curator_models for snapshot in state['snapshots']),
                'startup created an unowned model snapshot')
        state['snapshots'] = [snapshot for snapshot in state['snapshots'] if snapshot['snapshot_id'] in run_models]
        return state


def install_environment(root):
    env = {k: v for k, v in os.environ.items() if not k.startswith('STEWARD_')}
    # npm enables Node's compile cache. Keep that cache and every subprocess
    # scratch file under this fixture, never beside it in the CI tmpfs root.
    temporary = root / 'temporary'
    temporary.mkdir()
    env.update(TMPDIR=str(temporary), NODE_COMPILE_CACHE=str(root / 'node-compile-cache'),
               npm_config_offline='true')
    # CI's copied public SDK is immutable. Product builds use its original
    # Host SDK, as in ci-inputs; do not populate the fixture SDK's archives.
    base_sdk = os.environ.get('STEWARD_CI_BASE_GOROOT')
    if base_sdk:
        env['GOROOT'] = base_sdk
        env['PATH'] = str(Path(base_sdk) / 'bin') + os.pathsep + env['PATH']
    return env


def verify_install():
    report = dict(version=1, checks=[], live_model='not_run')
    with tempfile.TemporaryDirectory(prefix='steward-start-') as tmp:
        root = Path(tmp)
        env = install_environment(root)
        def run(*args):
            result = owned_run(args, cwd=ROOT, env=env, text=True,
                                    capture_output=True, timeout=120)
            require(result.returncode == 0, result.stdout + result.stderr)
            return result.stdout

        # Package through the same source installer used by contributors.
        package_log = run('sh', str(ROOT / 'scripts/package.sh'), str(root / 'dist'))
        archive, = (root / 'dist').glob('*.tar.gz')
        require(archive.with_suffix('.gz.sha256').read_text().split()[0] == hashlib.sha256(archive.read_bytes()).hexdigest(),
                'native package checksum does not match archive')
        with tarfile.open(archive) as bundle:
            bundle.extractall(root / 'unpacked', filter='data')
        installer = root / 'unpacked/steward/install.sh'
        prefix = root / 'prefix with spaces'
        install_log = run('sh', str(installer), '--prefix', str(prefix))
        installed = prefix / 'lib/steward/current'
        for name in ('LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md'):
            require((installed/name).read_bytes() == (ROOT/name).read_bytes(), 'installation lost project notice: '+name)
        require((installed/'licenses/web.txt').read_bytes() == (ROOT/'internal/interaction/web/assets/licenses.txt').read_bytes(),
                'installation lost embedded Web notices')
        require((installed/'licenses/model-catalog/LICENSE').read_bytes() == (ROOT/'internal/llm/provider/LICENSE').read_bytes(), 'model catalog license missing')
        require((installed/'licenses/model-catalog/catalog/NOTICE.md').read_bytes() == (ROOT/'internal/llm/provider/catalog/NOTICE.md').read_bytes(), 'model catalog provenance missing')
        modules = json.loads((installed/'licenses/go-modules.json').read_text())
        require(any(m['module']=='modernc.org/sqlite' for m in modules), 'linked SQLite license absent')
        require(all(m['files'] and all((installed/'licenses'/f).is_file() for f in m['files']) for m in modules),
                'linked dependency notices missing from archive/install')
        report['checks'].append('project, Web and linked Go notices survive source packaging and release installation')
        binary = prefix / 'bin/steward'
        folder = root / 'workspace'
        folder.mkdir()
        h = StartupHarness(binary, folder)
        h.env.update(TMPDIR=env['TMPDIR'], NODE_COMPILE_CACHE=env['NODE_COMPILE_CACHE'])
        diagnosed = h.command('doctor', ok=False)
        report['doctor'] = diagnosed.stdout
        require(diagnosed.returncode == 0 and 'FAIL' not in report['doctor'], 'installed doctor failed')
        require(not (folder / 'data').exists(), 'doctor created application state')
        report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
        report['archive_sha256'] = hashlib.sha256(archive.read_bytes()).hexdigest()
        report['checks'].append('source install -> native archive -> release install -> read-only doctor; launch from unrelated cwd with spaces in install path')

        # Refuse an unrelated executable before changing any installation.
        protected = root / 'protected'
        (protected / 'bin').mkdir(parents=True)
        sentinel = protected / 'bin/steward'
        sentinel.write_text('user executable')
        rejected = owned_run(['sh', str(installer), '--prefix', str(protected)],
                                  cwd=folder, env=env, capture_output=True, text=True, timeout=10)
        require(rejected.returncode != 0 and sentinel.read_text() == 'user executable',
                'installer overwrote an unrelated executable')
        require(not (protected / 'lib').exists(), 'rejected install left partial state')

        server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
        server.daemon_threads = True
        server.requests, server.mode = [], 'success'
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            # Bare steward, no YAML, no preselected model, no external broker.
            with Terminal(h.args(), h.env, folder) as terminal:
                terminal.expect('State: setup_required')
                require(h.command('status')['readiness'] == 'setup_required', 'startup selected an implicit model')
                local_setup(terminal, f'http://127.0.0.1:{server.server_port}/v1')
                terminal.send('One-command startup verification')
                terminal.expect('assistant: PROBE_RESULT_OK')
                # History updates can redraw the prompt before Actor.Drain
                # commits the terminal Run state; wait for its public signal.
                terminal.expect('Steward: idle')
                terminal.expect('You> ')
                before = h.state()
                assert_final(before, 1)
                require(len(before['tools']) == 2 and {t['tool_name'] for t in before['tools']} == {'builtin__codemode_execute','builtin__artifact_manage'} and all(t['attempt_count'] == 1 for t in before['tools']), 'startup bypassed Code Mode or the nested Tool executor')
                require(terminal.stop(signal.SIGKILL) == -signal.SIGKILL, 'missing crash evidence')
                report['first_launch'] = terminal.transcript()
            with Terminal(h.args(), h.env, folder) as terminal:
                terminal.expect_all(['State: ready', 'assistant: PROBE_RESULT_OK'])
                terminal.expect('You> ')
                restarted = h.state()
                changed = [key for key in before if restarted[key] != before[key]]
                require(not changed, 'restart duplicated or changed completed state: '+','.join(changed))
                require(terminal.stop() == 0, 'foreground cleanup failed')
                report['restart'] = terminal.transcript()
            # Reinstallation changes only the runtime, retaining the data root.
            run('sh', str(installer), '--prefix', str(prefix))
            require(h.state() == before, 'reinstallation changed application data')
            report['state'] = before
            report['checks'].append('explicit local setup -> real Pi Probe/select -> Main -> Direct Tool -> durable reply; SIGKILL and bare-command restart preserve completed state; /quit joins runtime; reinstall preserves user data')
            report['checks'].append('unmanaged executable protected; no shell-profile or credential writes')
        finally:
            h.cleanup()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)
            require(not thread.is_alive(), 'local model peer did not stop')
        report['package_log'] = package_log
        report['install_log'] = install_log
    require(not root.exists(), 'startup fixtures leaked')
    report['cleanup'] = 'all owned processes joined; temporary installation, broker, database and archive removed'
    return report


def verify():
    # Reuse the project process owner for failed installs, timeouts and PTYs too.
    # Retirement checks the entire owned session, including Node descendants.
    return run_cases([None], lambda _: verify_install(), jobs=1)[0]


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    # The suite supplies its binary argument; this gate builds and installs the
    # native package from the same checkout to exercise the real installer.
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--evidence', type=Path, default=ROOT / 'evidence/startup.json')
    args = parser.parse_args()
    result = verify()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(result, indent=2) + '\n')
    print('Startup verification passed:', args.evidence)
