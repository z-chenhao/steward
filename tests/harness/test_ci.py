"""Check partition completeness and the fail-closed required status."""
import unittest
import re
import shlex
import subprocess
from pathlib import Path
from unittest.mock import DEFAULT, patch

import ci
import verify_channels
from runner import CORE


class CIChecksTests(unittest.TestCase):
    def test_required_web_lane_reaches_settings_and_task_regressions(self):
        # Standalone scenarios protect a flow only when the required lane runs it.
        scenarios = ('preference_settings', 'memory_inspection', 'attention_workspace',
                     'sending_feedback', 'live_updates', 'streaming_replies',
                     'inspection_relationships', 'capability_settings',
                     'dependency_recovery', 'native_components', 'rich_replies',
                     'account_presentation', 'native_trace_inspection',
                     'page_transitions', 'model_connections', 'task_control_components')
        lanes = {row['lane']: row for row in ci.plan('system')['include']}
        self.assertEqual(lanes['channels-web']['browser'], 'true')
        self.assertIn('--group web', ci.command('channels-web'))
        report = dict(checks=[], scenarios={})
        binary, evidence = Path('/unused/steward'), Path('/unused/evidence/report.json')
        with patch.multiple(verify_channels, **dict.fromkeys(scenarios, DEFAULT)) as checks:
            ci.CHANNEL_GROUPS['web'](binary, evidence, report)
        for name in ('preference_settings', 'page_transitions', 'model_connections',
                     'task_control_components', 'capability_settings'):
            checks[name].assert_called_once_with(binary, evidence, report)

    def test_connected_apps_and_browser_regressions_are_required_software_lanes(self):
        lanes = {row['lane']: row for row in ci.plan('software')['include']}
        self.assertEqual(ci.command('connected-apps'), 'make test-connected-apps')
        self.assertIn('connected-apps', lanes)
        self.assertIn('make test-browser', ci.command('browser-infra'))
        self.assertEqual(lanes['browser-infra']['browser'], 'true')
        self.assertEqual(lanes['browser-infra']['frontend'], 'true')
        dry_run = subprocess.check_output(['make', '-n', 'test-browser'],
                                          cwd=Path(__file__).resolve().parents[2], text=True)
        patterns = []
        for line in dry_run.splitlines():
            tokens = shlex.split(line)
            if '-run' in tokens:
                patterns.append(tokens[tokens.index('-run') + 1])
        for test in ('TestAppsBrowserConnectionLifecycle', 'TestComposerModelsBrowserLoadingAndRefresh',
                     'TestMemoryMaintenanceBrowser'):
            self.assertTrue(any(re.fullmatch(pattern, test) for pattern in patterns),
                            f'{test} is missing from the required browser recipe')


    def test_all_core_gates_run_once(self):
        gates = [gate for group in ci.SYSTEM.values() for gate in group]
        self.assertCountEqual(gates + ['channels'], CORE)
        self.assertEqual(len(gates), len(set(gates)))
        for kind in ('software', 'system'):
            for lane in ci.plan(kind)['include']:
                self.assertTrue(ci.command(lane['lane']))
        lanes = {lane['lane'] for lane in ci.plan('system')['include']}
        self.assertEqual({name.removeprefix('channels-') for name in lanes if name.startswith('channels-')}, set(ci.CHANNEL_GROUPS))

    def test_intelligent_ui_uses_required_go_browser_and_race_checks(self):
        self.assertIn('test-go', ci.command('go'))
        self.assertIn('intelligent-ui', ci.SYSTEM['startup-ui'])
        self.assertIn('ui-interactions', ci.SYSTEM['startup-ui'])
        lanes = {row['lane']: row for row in ci.plan('system')['include']}
        self.assertEqual(lanes['startup-ui']['browser'], 'true')
        race = ci.command('capabilities-race')
        for package in ('agent', 'agent/sqlite', 'interaction/web', 'llm/bridge'):
            self.assertIn('./internal/' + package + ' ', ci.RACE)
        self.assertIn(ci.RACE, race)
        self.assertIn('.*UI.*', ci.RACE)
        self.assertIn('ReplyStream.*', ci.RACE)

    def test_memory_privacy_race_and_system_gates_are_required(self):
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('memory-context', lanes)
        self.assertEqual(lanes['memory-context']['browser'], 'false')
        command = ci.command('memory-context')
        self.assertIn('make test-memory build', command)
        self.assertEqual(ci.MEMORY_GATES, ('memory-maintenance', 'recall'))
        for gate in ci.MEMORY_GATES:
            self.assertEqual(command.count('--gate ' + gate), 1)
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_required_memory_lane_covers_identity_and_candidate_boundaries(self):
        makefile = (Path(__file__).resolve().parents[2] / 'Makefile').read_text()
        commands = makefile.split('\ntest-memory:\n', 1)[1].split('\n\n', 1)[0]
        self.assertIn('memory-context', {lane['lane'] for lane in ci.plan('system')['include']})
        self.assertEqual(commands.count('go test -race '), 4)
        for package in ('memory/...', 'recall/...', 'conversation/...', 'tool', 'llm/bridge', 'app'):
            self.assertIn('./internal/' + package + ' ', commands)
        for regression in ('Recall', 'Forget', 'Evidence', 'ObjectIDs', 'Layered', 'RootFailureNotice', 'NativeFragmented', 'MemoryExternalSecrets'):
            self.assertIn(regression, commands)
        # Existence in test-go alone must not silently remove race coverage.
        dry_run = subprocess.check_output(['make', '-n', 'test-memory'],
                                          cwd=Path(__file__).resolve().parents[2], text=True)
        agent_patterns = []
        for line in dry_run.splitlines():
            tokens = shlex.split(line)
            if './internal/agent/sqlite' in tokens and '-run' in tokens:
                agent_patterns.append(tokens[tokens.index('-run') + 1])
        for test in ('TestMemoryWorkerEvidencePageBoundaryAndContinuation',
                     'TestMemoryMissingFieldsSurviveNativeAndCodeModeReceipts',
                     'TestMemoryDreamDoesNotBlindlyRecreateOversizedInjectedHead'):
            self.assertTrue(any(re.search(pattern, test) for pattern in agent_patterns),
                            f'{test} is missing from required memory race checks')

    def test_artifact_round_trips_run_in_required_tools_lane(self):
        self.assertIn('artifacts', CORE)
        self.assertEqual(ci.command('tools-provider').count('--gate artifacts'), 1)

    def test_required_check_rejects_missing_failed_and_cancelled_work(self):
        for docs in ('true', 'false'):
            selected = 'skipped' if docs == 'true' else 'success'
            valid = dict(scope=dict(result='success', outputs=dict(docs_only=docs)),
                         software=dict(result=selected), system=dict(result=selected))
            self.assertTrue(ci.passed(valid))
            for name in valid:
                with self.subTest(docs=docs, job=name):
                    self.assertFalse(ci.passed({key: value for key, value in valid.items() if key != name}))
                    for status in ('failure', 'cancelled', 'skipped' if selected == 'success' else 'success'):
                        self.assertFalse(ci.passed({**valid, name: dict(result=status)}))
        self.assertFalse(ci.passed(dict(scope=dict(result='success', outputs={}),
                                       software=dict(result='success'), system=dict(result='success'))))
