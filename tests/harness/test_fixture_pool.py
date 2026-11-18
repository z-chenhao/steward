"""Independent fixtures retain every case and cancel nested owned sessions."""
import json
import os
from pathlib import Path
import signal
import shlex
import socket
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

import subprocess
import terminal_driver

import fixture_pool as fixtures
import runner as suite


class FixturePoolTests(unittest.TestCase):

    def test_json_events_preserve_cold_download_diagnostics(self):
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / 'regressions.log'
            command = [sys.executable, '-c',
                       'import json, sys; print("go: downloading fixture", file=sys.stderr); '
                       'print(json.dumps({"Action": "pass", "Test": "TestOriginal"}))']
            status, events = fixtures.run_json_lines(command, log, cwd=suite.ROOT,
                                                     env=os.environ.copy(), timeout=5)
            self.assertEqual(status, 0)
            self.assertEqual(events, [{'Action': 'pass', 'Test': 'TestOriginal'}])
            self.assertEqual(log.with_suffix('.stderr.log').read_text(), 'go: downloading fixture\n')

    def test_timeout_kills_descendant_holding_captured_pipes(self):
        scope = fixtures.Scope()
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / 'descendant.json'
            descendant = ('import json,os,socket,time\n'
                          'from pathlib import Path\n'
                          's=socket.socket();s.bind(("127.0.0.1",0));s.listen()\n'
                          f'Path({str(ready)!r}).write_text(json.dumps([os.getpid(),s.getsockname()[1]]))\n'
                          'time.sleep(30)\n')
            leader = ('import subprocess,sys,time\n'
                      f'subprocess.Popen([sys.executable,"-c",{descendant!r}])\n'
                      'time.sleep(30)\n')
            started = time.monotonic()
            try:
                with self.assertRaises(subprocess.TimeoutExpired):
                    scope.run([sys.executable, '-c', leader], capture_output=True, text=True, timeout=.5)
                self.assertLess(time.monotonic() - started, 2, 'descendant retained captured pipes after timeout')
                self.assertFalse(scope.children)
                pid, port = json.loads(ready.read_text())
                with socket.socket() as probe:
                    self.assertNotEqual(probe.connect_ex(('127.0.0.1', port)), 0)
            finally:
                # The failed pre-fix path must still clean its captured fixture.
                if ready.exists():
                    pid, _ = json.loads(ready.read_text())
                    try:
                        os.kill(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def test_cancelled_terminal_launch_closes_all_pty_descriptors(self):
        descriptors = []
        openpty, dup = terminal_driver.pty.openpty, os.dup
        def allocate():
            pair = openpty()
            descriptors.extend(pair)
            return pair
        def duplicate(fd):
            result = dup(fd)
            descriptors.append(result)
            return result
        scope = fixtures.Scope()
        scope.cancel()
        with patch.object(terminal_driver, 'current', return_value=scope), \
             patch.object(terminal_driver.pty, 'openpty', side_effect=allocate), \
             patch.object(terminal_driver.os, 'dup', side_effect=duplicate):
            with self.assertRaisesRegex(RuntimeError, 'cancelled'):
                terminal_driver.Terminal([sys.executable, '-c', 'pass'], os.environ.copy(), Path.cwd())
        self.assertEqual(len(descriptors), 3)
        for fd in descriptors:
            with self.assertRaises(OSError):
                os.fstat(fd)

    def test_failed_fixture_does_not_stop_an_unrelated_process(self):
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time; print("ready",flush=True); time.sleep(30)'],
                                     stdout=subprocess.PIPE, text=True)
        try:
            self.assertEqual(unrelated.stdout.readline(), 'ready\n')
            def fail(_):
                raise RuntimeError('fixture failure')
            with self.assertRaisesRegex(RuntimeError, 'fixture failure'):
                fixtures.run_cases([1, 2], fail, 2)
            self.assertIsNone(unrelated.poll())
            self.assertIsNone(fixtures.current())
        finally:
            unrelated.kill()
            unrelated.communicate(timeout=5)

    def test_complete_ordered_results_and_bound(self):
        lock, running, peak = threading.Lock(), 0, 0
        def action(value):
            nonlocal running, peak
            with lock:
                running += 1
                peak = max(peak, running)
            result = fixtures.run([sys.executable, '-c', 'import time; time.sleep(.03); print("done")'],
                                  capture_output=True, text=True, timeout=5, check=True)
            with lock:
                running -= 1
            self.assertEqual(result.stdout, 'done\n')
            return value
        self.assertEqual(fixtures.run_cases(list(range(20)), action, 2), list(range(20)))
        self.assertEqual(peak, 2)
        peak = 0
        self.assertEqual(fixtures.run_cases(list(range(20)), action, 4), list(range(20)))
        self.assertEqual(peak, 4)
        self.assertIsNone(fixtures.current())
        for invalid in [0, 3, 8, True, '2']:
            with self.assertRaises(ValueError):
                fixtures.run_cases([], action, invalid)

    def test_cancelled_scope_cannot_launch_a_late_process(self):
        scope = fixtures.Scope()
        scope.cancel()
        with self.assertRaisesRegex(RuntimeError, 'cancelled'):
            scope.start([sys.executable, '-c', 'raise SystemExit(0)'])
        self.assertFalse(scope.children)

    def test_sigterm_joins_two_stubborn_sessions_and_cleans_state(self):
        self.check_stubborn_session_cleanup()

    def test_step_wrapping_make_joins_sessions_and_cleans_state(self):
        self.check_stubborn_session_cleanup(make_step=True)

    def test_repeated_termination_cannot_interrupt_profiled_fixture_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            script = root / 'fixture.py'
            script.write_text(
                'import time\nfrom pathlib import Path\n'
                f'root=Path({str(root)!r})\n'
                '(root/"state").mkdir()\n'
                'try:\n'
                ' (root/"ready").touch()\n'
                ' while True: time.sleep(.01)\n'
                'finally:\n'
                ' (root/"cleanup").touch()\n'
                ' while not (root/"release").exists(): time.sleep(.01)\n'
                ' (root/"state").rmdir()\n')
            env = dict(os.environ, STEWARD_TEST_METRICS_DIR=str(root / 'profiles'))
            with (root / 'log').open('w') as log:
                child = subprocess.Popen([sys.executable, 'tests/system/entrypoint.py', str(script)],
                                         cwd=suite.ROOT, env=env, stdout=log, stderr=subprocess.STDOUT,
                                         start_new_session=True)
                try:
                    for marker in ('ready', 'cleanup'):
                        deadline = time.monotonic() + 3
                        while not (root / marker).exists() and time.monotonic() < deadline:
                            time.sleep(.01)
                        self.assertTrue((root / marker).exists())
                        os.kill(child.pid, signal.SIGTERM)
                    # Cleanup is deliberately held. A second notification must
                    # not terminate its finally block before release.
                    with self.assertRaises(subprocess.TimeoutExpired):
                        child.wait(timeout=.2)
                    (root / 'release').touch()
                    self.assertEqual(child.wait(timeout=3), 143)
                    self.assertFalse((root / 'state').exists())
                    self.assertTrue((root / 'profiles/fixture.json').is_file())
                finally:
                    (root / 'release').touch()
                    if child.poll() is None:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait(timeout=3)

    def test_actions_entry_sigint_reaches_make_owner_and_cleans_two_sessions(self):
        self.check_stubborn_session_cleanup(make_step=True, actions_shell=True)

    def test_cancelled_parallel_pty_waits_join_before_owner_deadline(self):
        self.check_stubborn_session_cleanup(make_step=True, actions_shell=True, pty_wait=True)

    def check_stubborn_session_cleanup(self, make_step=False, actions_shell=False, pty_wait=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = root / 'state'
            child = ('import json,os,signal,socket,sys,time\nfrom pathlib import Path\n'
                     'signal.signal(signal.SIGTERM, signal.SIG_IGN)\n'
                     's=socket.socket();s.bind(("127.0.0.1",0));s.listen()\n'
                     f'p=Path({str(root)!r})/("ready-"+sys.argv[1]+".json")\n'
                     'p.with_suffix(".tmp").write_text(json.dumps([os.getpid(),s.getsockname()[1]]))\n'
                     'p.with_suffix(".tmp").replace(p)\n'
                     'time.sleep(60)\n')
            program = ('import shutil,sys\nfrom pathlib import Path\n'
                       'from fixture_pool import run_cases,run\n'
                       f'state=Path({str(state)!r});state.mkdir()\n'
                       'def action(index):\n'
                       f' result=run([sys.executable,"-c",{child!r},str(index)],capture_output=True,text=True,timeout=20)\n'
                       ' if result.returncode: raise RuntimeError("cancelled child")\n'
                       ' return index\n'
                       'try: run_cases(list(range(20)),action,2)\n'
                       'finally: shutil.rmtree(state)\n')
            if pty_wait:
                program = ('import os,shutil,sys\nfrom pathlib import Path\n'
                           'from fixture_pool import run_cases\nfrom terminal_driver import Terminal\n'
                           f'state=Path({str(state)!r});state.mkdir()\n'
                           'def action(index):\n'
                           f' with Terminal([sys.executable,"-c",{child!r},str(index)],os.environ.copy(),state) as terminal:\n'
                           '  terminal.expect("never emitted",timeout=20)\n'
                           'try: run_cases(list(range(20)),action,2)\n'
                           'finally: shutil.rmtree(state)\n')
            script = root / 'fixture.py'
            script.write_text(program)
            command = [sys.executable, 'tests/system/entrypoint.py', str(script)]
            step_name = 'fixture-cancel-' + root.name.replace('_', '-')
            if make_step:
                makefile = root / 'Makefile'
                makefile.write_text('.PHONY: fixture\nfixture:\n\t' + shlex.join(command) + '\n')
                command = [sys.executable, 'tests/system/process_step.py', '--name', step_name, '--phase', 'test',
                           '--', 'make', '-f', str(makefile), 'fixture']
            entry, entry_log = None, None
            if actions_shell:
                workflow = (suite.ROOT / '.github/workflows/ci.yml').read_text()
                line = next(line.strip() for line in workflow.splitlines()
                            if 'tests/system/ci.py --lane' in line)
                # Exercise the actual lane -> exec(process_step) entry as well
                # as Actions' exec shell. Only the fixture command is replaced.
                lane_command = shlex.join(['make', '-f', str(makefile), 'fixture'])
                command = [sys.executable, '-c',
                           f'import ci,sys; ci.SOFTWARE[{step_name!r}]={lane_command!r}; '
                           f'sys.argv=["ci.py","--lane",{step_name!r}]; ci.main()']
                shell = root / 'step.sh'
                shell.write_text(('exec ' if line.startswith('run: exec ') else '') + shlex.join(command) + '\n')
                entry_log = (root / 'log').open('w')
                env = dict(os.environ, PYTHONPATH=str(suite.ROOT / 'tests/system'), STEWARD_TEST_METRICS_DIR=str(root / 'profiles'))
                entry = subprocess.Popen(['bash','--noprofile','--norc','-e','-o','pipefail',str(shell)],
                                         cwd=suite.ROOT,env=env,stdout=entry_log,stderr=subprocess.STDOUT,
                                         start_new_session=True)
            stopped = threading.Event()
            ready = []
            def interrupt():
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    paths = list(root.glob('ready-*.json'))
                    if len(paths) == 2:
                        ready.extend(json.loads(p.read_text()) for p in paths)
                        break
                    time.sleep(.01)
                if actions_shell:
                    # GitHub signals the entry PID, not its process group.
                    os.kill(entry.pid, signal.SIGINT)
                else:
                    stopped.set()
            waiter = threading.Thread(target=interrupt)
            waiter.start()
            metrics = {}
            started = time.monotonic()
            try:
                env = dict(os.environ, PYTHONPATH=str(suite.ROOT / 'tests/system'), STEWARD_TEST_METRICS_DIR=str(root / 'profiles'))
                if actions_shell:
                    code = entry.wait(timeout=7.5)
                    metrics.update(json.loads((suite.ROOT / 'evidence/steps' / (step_name + '.json')).read_text()))
                else:
                    code = suite.run_command(command, env, root / 'log', stopped, metrics)
                self.assertNotEqual(code, 0)
                self.assertEqual(len(ready), 2, (root / 'log').read_text())
                self.assertEqual(len(list(root.glob('ready-*.json'))), 2, 'queued fixtures started after cancellation')
                self.assertFalse(state.exists(), (root / 'log').read_text())
                self.assertTrue(metrics['owned_group_drained'])
                self.assertLess(time.monotonic() - started, 15)
                self.assertTrue((root / 'profiles/fixture.json').is_file())
                if make_step:
                    record = json.loads((suite.ROOT / 'evidence/steps' / (step_name + '.json')).read_text())
                    self.assertEqual(record['returncode'], 130)
                    self.assertIsNotNone(record['cancel_received_at'])
                    self.assertTrue(record['owned_group_drained'])
                for _, port in ready:
                    with socket.socket() as probe:
                        self.assertNotEqual(probe.connect_ex(('127.0.0.1', port)), 0)
            finally:
                waiter.join(timeout=6)
                if entry is not None and entry.poll() is None:
                    os.killpg(entry.pid, signal.SIGTERM)
                    entry.wait(timeout=8)
                    deadline = time.monotonic() + 5
                    while not (suite.ROOT / 'evidence/steps' / (step_name + '.json')).exists() and time.monotonic() < deadline:
                        time.sleep(.02)
                if entry_log is not None:
                    entry_log.close()
                if make_step:
                    for suffix in ('.json', '.log', '-cancel.json'):
                        (suite.ROOT / 'evidence/steps' / (step_name + suffix)).unlink(missing_ok=True)
                for pid, _ in ready:
                    try:
                        os.kill(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass


if __name__ == '__main__':
    unittest.main()
