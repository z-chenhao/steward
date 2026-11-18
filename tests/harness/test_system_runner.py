"""Exercise failure, fresh evidence and source drift through actual child processes."""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import runner


class SystemRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / 'tests/system'
        scripts.mkdir(parents=True)
        shutil.copy2(runner.ROOT / 'tests/system/entrypoint.py', scripts / 'entrypoint.py')
        self.script = scripts / 'fixture.py'
        self.binary = self.root / 'steward'
        self.binary.touch()
        self.output = self.root / 'evidence'

    def drive(self, gates=('fixture',), drift=False, journey=None):
        registry = {'fixture': 'fixture.py', 'second': 'second.py'}
        before = {'source': 'one'}
        after = {'source': 'changed'} if drift else before
        argv = ['--binary', str(self.binary), '--evidence', str(self.output)]
        if journey is None:
            for gate in gates:
                argv += ['--gate', gate]
        else:
            argv += ['--journey', journey]
        with patch.object(runner, 'ROOT', self.root), \
             patch.object(runner, 'REGISTRY', registry), \
             patch.object(runner, 'source_state', side_effect=[before, after]):
            return runner.main(argv)

    def test_untracked_source_identity_includes_kind_and_executable_bit(self):
        subprocess.run(['git', 'init', '-q'], cwd=self.root, check=True)
        subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                        'commit', '--allow-empty', '-qm', 'fixture'], cwd=self.root, check=True)
        entry = self.root / 'entry'
        entry.write_text('fixture.py')
        with patch.object(runner, 'ROOT', self.root):
            before = runner.source_state()
            entry.chmod(0o755)
            executable = runner.source_state()
            self.assertNotEqual(before, executable)
            entry.unlink()
            entry.symlink_to('fixture.py')
            self.assertNotEqual(before, runner.source_state())

    def test_project_preparation_stops_on_first_failed_copy(self):
        command = json.loads((runner.ROOT / 'coding.project.json').read_text())['prepare']['command']
        shim = self.root / 'cp'
        shim.write_text('#!/bin/sh\necho missing-cache >&2\nexit 71\n')
        shim.chmod(0o755)
        result = subprocess.run(['/bin/sh', '-c', command], cwd=self.root,
                                env={'PATH': f'{self.root}:/usr/bin:/bin'}, text=True, capture_output=True)
        self.assertEqual(result.returncode, 71)
        self.assertEqual(result.stderr.strip(), 'missing-cache')

    def write_success(self):
        self.script.write_text('import json,sys\nfrom pathlib import Path\n'
            'Path(sys.argv[sys.argv.index("--evidence")+1]).write_text(json.dumps({"checks":["ran"]}))\n')

    def test_nonzero_child_stops_the_sequence_and_retains_failure(self):
        self.script.write_text('raise SystemExit(7)\n')
        marker = self.root / 'should-not-run'
        self.script.with_name('second.py').write_text(f'from pathlib import Path\nPath({str(marker)!r}).touch()\n')
        self.assertEqual(self.drive(('fixture', 'second')), 1)
        result = json.loads((self.output / 'summary.json').read_text())
        self.assertEqual(result['results'][0]['returncode'], 7)
        self.assertFalse(result['passed'])
        self.assertFalse(marker.exists())

    def test_zero_exit_without_a_report_cannot_pass(self):
        self.script.write_text('pass\n')
        self.assertEqual(self.drive(), 1)
        self.assertFalse(json.loads((self.output / 'summary.json').read_text())['passed'])

    def test_success_is_invalidated_by_source_drift(self):
        self.write_success()
        self.assertEqual(self.drive(drift=True), 1)
        result = json.loads((self.output / 'summary.json').read_text())
        self.assertEqual(result['results'][0]['returncode'], 0)
        self.assertFalse(result['source_stable'])

    def test_previous_evidence_is_preserved_and_rejected(self):
        self.output.mkdir()
        previous = self.output / 'fixture-1.json'
        previous.write_text('previous failed observation')
        with self.assertRaises(SystemExit):
            self.drive()
        self.assertEqual(previous.read_text(), 'previous failed observation')

    def test_success_requires_the_selected_process_to_execute(self):
        self.write_success()
        self.assertEqual(self.drive(), 0)
        result = json.loads((self.output / 'summary.json').read_text())
        self.assertTrue(result['passed'])
        self.assertTrue(result['results'][0]['owned_group_drained'])

    def test_journey_runs_each_gate_in_order_and_retains_both_reports(self):
        self.write_success()
        shutil.copy2(self.script, self.script.with_name('second.py'))
        with patch.object(runner, 'JOURNEYS', {'sample': ('second', 'fixture')}):
            self.assertEqual(self.drive(journey='sample'), 0)
        result = json.loads((self.output / 'summary.json').read_text())
        self.assertEqual([row['gate'] for row in result['results']], ['second', 'fixture'])
        self.assertTrue(result['passed'])
        for name in ('second', 'fixture'):
            observed = json.loads((self.output / f'{name}-1.json').read_text())
            self.assertEqual(observed['checks'], ['ran'])
