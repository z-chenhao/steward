from collections import Counter
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import tempfile
import threading
import sys
import time
import unittest
from unittest.mock import patch

import race as race
from runner import run_command


def events(names):
    rows = [dict(Action='pass', Package=race.SQLITE, Test=n, Elapsed=.01) for n in names]
    rows.append(dict(Action='pass', Package=race.SQLITE))
    return rows


class SQLiteRaceInventoryTests(unittest.TestCase):
    def test_memory_selection_admits_new_roots_and_rejects_lost_or_repeated_evidence(self):
        names = [*race.SERIAL_SQLITE, 'TestUnrelated', 'TestFreshMemoryRegression',
                 'TestRecallEvidence', 'TestAutomationForget', 'TestObjectIDs']
        expected = {'TestFreshMemoryRegression', 'TestRecallEvidence', 'TestAutomationForget', 'TestObjectIDs'}
        for fault in (None, 'missing', 'duplicate'):
            with self.subTest(fault=fault), tempfile.TemporaryDirectory() as directory:
                commands = []
                def execute(command, env, output, stopped, metrics):
                    commands.append(command)
                    metrics.update(owned_group_drained=True, cpu_seconds=.01)
                    if '-list' in command:
                        output.write_text('\n'.join(names) + '\nok\t' + race.SQLITE + '\t0.01s\n')
                    else:
                        self.assertEqual(env['GOMAXPROCS'], '1')
                        selected = command[command.index('-run') + 1][2:-2].split('|')
                        if 'TestFreshMemoryRegression' in selected:
                            if fault == 'missing':
                                selected.remove('TestFreshMemoryRegression')
                            elif fault == 'duplicate':
                                selected.append('TestFreshMemoryRegression')
                        output.write_text('\n'.join(map(json.dumps, events(selected))))
                    return 0
                with patch.object(race, 'run_command', side_effect=execute), \
                     patch.object(race, 'cpu_budget', return_value=4):
                    result = race.run_remainder([race.SQLITE], Path(directory), threading.Event(), memory=True)
                self.assertEqual(set(result['expected_tests']), expected)
                self.assertLessEqual(len(result['workers']), 3)
                self.assertFalse(result['isolated_tests'])
                self.assertEqual(result['returncode'] == 0, fault is None)
                self.assertEqual(result['selection_complete'], fault is None)
                for command in commands:
                    for flag in ('-race', '-count=1', '-timeout=10m'):
                        self.assertIn(flag, command)

    def test_deadline_packages_are_isolated_without_losing_or_repeating_inventory(self):
        extra = 'github.com/z-chenhao/assistant/new/pkg'
        packages = [race.SQLITE, extra, race.RECORDER, race.BRIDGE]
        for ordered in (packages, list(reversed(packages))):
            complete = race.groups(ordered)
            self.assertEqual(complete[:2], [('bridge', [race.BRIDGE]), ('recorder', [race.RECORDER])])
            self.assertEqual(Counter(p for _, group in complete for p in group), Counter(packages))
            partial = race.groups(ordered, 'packages') + race.groups(ordered, 'sqlite')
            self.assertEqual(Counter(p for _, group in partial for p in group), Counter(packages))
            for name, group in complete[2:]:
                self.assertFalse({race.BRIDGE, race.RECORDER} & set(group))
        for missing in (race.BRIDGE, race.RECORDER, race.SQLITE):
            with self.subTest(missing=missing), self.assertRaises(ValueError):
                race.groups([p for p in packages if p != missing])

    def test_discovery_includes_examples_and_fuzz_seeds_but_not_benchmarks(self):
        listing = 'TestA\nExampleB\nFuzzC\nBenchmarkD\nok\t' + race.SQLITE + '\t0.01s\n'
        self.assertEqual(race.test_names(listing), ['TestA', 'ExampleB', 'FuzzC'])
        for broken in ('', 'TestA\n', listing + 'TestLate\n',
                       listing.replace('TestA\n', 'TestA\nTestA\n'),
                       listing.replace('TestA', 'TestA|TestInjected'),
                       listing.replace('ok\t', 'unknown\t')):
            with self.subTest(broken=broken), self.assertRaises(ValueError):
                race.test_names(broken)

    def test_partitions_cover_every_root_exactly_once_at_every_capacity(self):
        for count in range(1, 20):
            names = [f'Test{i}' for i in range(count)]
            for workers in (1, 2, 3):
                groups = race.partitions(names, workers)
                self.assertEqual(Counter(n for g in groups for n in g), Counter(names))
                self.assertLessEqual(len(groups), workers)
        for names, workers in (([], 3), (['TestA', 'TestA'], 3), (['TestA'], 0)):
            with self.assertRaises(ValueError):
                race.partitions(names, workers)

    def test_missing_duplicate_foreign_failed_or_partial_execution_rejects_verdict(self):
        names = ['TestA', 'ExampleB', 'FuzzC']
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'events.log'
            good = events(names)
            path.write_text('\n'.join(map(json.dumps, good)))
            self.assertEqual(set(race.sqlite_verdict(path, names)), set(names))
            broken = [good[1:], good + [good[0]], good[:-1],
                      good + [dict(Action='pass', Package=race.SQLITE, Test='TestUnexpected')],
                      [dict(good[0], Action='fail'), *good[1:]],
                      [dict(good[0], Package='foreign/pkg'), *good[1:]],
                      [*good[:-1], dict(good[-1], Action='skip')]]
            for rows in broken:
                path.write_text('\n'.join(map(json.dumps, rows)))
                with self.subTest(rows=rows), self.assertRaises(ValueError):
                    race.sqlite_verdict(path, names)

    def test_measured_costs_balance_long_tests_without_changing_inventory(self):
        costs = dict(TestA=6, TestB=5, TestC=4, TestD=3, TestE=2, TestF=1)
        names = list(costs)
        groups = race.partitions(names, 3, costs)
        self.assertEqual(Counter(n for g in groups for n in g), Counter(names))
        self.assertEqual([sum(costs[n] for n in g) for g in groups], [7, 7, 7])
        self.assertEqual(groups, race.partitions(list(reversed(names)), 3, costs))

    def test_timing_hints_cannot_select_tests_or_exclude_new_roots(self):
        costs = dict(TestOld=20, TestRemoved=100)
        names = ['TestOld', 'TestNew', 'ExampleNew', 'FuzzNew']
        for workers in (1, 2, 3, 8):
            groups = race.partitions(names, workers, costs)
            self.assertEqual(Counter(n for g in groups for n in g), Counter(names))
            self.assertLessEqual(len(groups), workers)
        for value in (0, -1, float('nan'), float('inf'), True, '1'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                race.partitions(names, 3, dict(TestOld=value))

    def test_retained_ubuntu_timings_reproduce_and_reduce_partition_skew(self):
        costs = race.timing_hints()
        # Discovery, not this historical file, remains the execution inventory.
        names = [n for n in costs if n not in race.SERIAL_SQLITE]
        peak = lambda groups: max(sum(costs[n] for n in g) for g in groups)
        before = peak(race.partitions(names, 3))
        after = peak(race.partitions(names, 3, costs))
        self.assertLess(after, before - 30)
        self.assertLess(after, sum(costs[n] for n in names) / 3 + max(costs.values()) / 10)

    def test_cpu_budget_respects_affinity_parent_limit_and_four_core_ceiling(self):
        with patch.object(race.os, 'cpu_count', return_value=16), \
             patch.object(race.os, 'sched_getaffinity', return_value=set(range(8)), create=True):
            for configured, expected in (('4', 4), ('2', 2), ('1', 1)):
                with patch.dict(os.environ, GOMAXPROCS=configured):
                    self.assertEqual(race.cpu_budget(), expected)
            with patch.dict(os.environ, GOMAXPROCS='0'), self.assertRaises(ValueError):
                race.cpu_budget()

    def test_real_dispatch_bounds_workers_and_audits_complete_selection(self):
        names = [*race.SERIAL_SQLITE, 'TestA', 'TestB', 'TestC', 'ExampleD', 'FuzzE', 'TestF']
        lock, barrier = threading.Lock(), threading.Barrier(4)
        active = peak = 0
        isolated_done = threading.Event()
        calls = []
        def execute(command, env, output, stopped, metrics):
            nonlocal active, peak
            metrics.update(owned_group_drained=True, cpu_seconds=.01)
            calls.append(command)
            if '-list' in command:
                output.write_text('\n'.join(names) + '\nok\t' + race.SQLITE + '\t0.01s\n')
                return 0
            selected = command[command.index('-run') + 1][2:-2].split('|') if '-run' in command else None
            if selected == race.SERIAL_SQLITE:
                self.assertEqual(active, 0)
                self.assertEqual(env['GOMAXPROCS'], '4')
                output.write_text('\n'.join(map(json.dumps, events(selected))))
                isolated_done.set()
                return 0
            self.assertTrue(isolated_done.is_set())
            self.assertEqual(env['GOMAXPROCS'], '1')
            with lock:
                active += 1
                peak = max(peak, active)
            barrier.wait(timeout=3)
            if '-run' in command:
                selected = command[command.index('-run') + 1][2:-2].split('|')
                output.write_text('\n'.join(map(json.dumps, events(selected))))
            else:
                self.assertIn('-p=1', command)
                output.write_text('other package passed\n')
            with lock:
                active -= 1
            return 0
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(race, 'run_command', side_effect=execute), \
             patch.object(race, 'cpu_budget', return_value=4):
            result = race.run_remainder([race.SQLITE, 'github.com/z-chenhao/assistant/new/pkg'],
                                        Path(directory), threading.Event())
        self.assertEqual(result['returncode'], 0)
        self.assertTrue(result['selection_complete'])
        self.assertTrue(result['owned_group_drained'])
        self.assertEqual(peak, 4)
        self.assertEqual(Counter(n for w in result['workers'] for n in w.get('tests', {})), Counter(names))
        for command in calls:
            for required in ('-race', '-count=1', '-timeout=15m'):
                self.assertIn(required, command)

    def test_locally_valid_partition_cannot_omit_a_global_root(self):
        names = [*race.SERIAL_SQLITE, 'TestA', 'TestB']
        def execute(command, env, output, stopped, metrics):
            metrics.update(owned_group_drained=True, cpu_seconds=.01)
            if '-list' in command:
                output.write_text('\n'.join(names) + '\nok\t' + race.SQLITE + '\t0.01s\n')
            else:
                selected = command[command.index('-run') + 1][2:-2].split('|')
                output.write_text('\n'.join(map(json.dumps, events(selected))))
            return 0
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(race, 'run_command', side_effect=execute), \
             patch.object(race, 'partitions', return_value=[['TestA']]), \
             patch.object(race, 'cpu_budget', return_value=4):
            result = race.run_remainder([race.SQLITE], Path(directory), threading.Event())
        self.assertNotEqual(result['returncode'], 0)
        self.assertFalse(result['selection_complete'])

    def test_cancellation_drains_all_four_real_owned_process_groups(self):
        with tempfile.TemporaryDirectory() as directory:
            folder, stopped = Path(directory), threading.Event()
            def execute(command, env, output, stop, metrics):
                if '-list' in command:
                    metrics.update(owned_group_drained=True, cpu_seconds=0)
                    output.write_text('TestA\nTestB\nTestC\n' + race.SERIAL_SQLITE[0] + '\nok\t' + race.SQLITE + '\t0.01s\n')
                    return 0
                selected = command[command.index('-run') + 1][2:-2].split('|') if '-run' in command else None
                if selected == race.SERIAL_SQLITE:
                    metrics.update(owned_group_drained=True, cpu_seconds=0)
                    output.write_text('\n'.join(map(json.dumps, events(selected))))
                    return 0
                ready = folder / (output.stem + '.ready')
                source = ('import os,signal,time; from pathlib import Path; '
                          'signal.signal(signal.SIGTERM,signal.SIG_IGN); '
                          f'Path({str(ready)!r}).write_text(str(os.getpid())); '
                          'time.sleep(60)')
                return run_command([sys.executable, '-c', source], env, output, stop, metrics)
            with patch.object(race, 'run_command', side_effect=execute), \
                 patch.object(race, 'cpu_budget', return_value=4), \
                 ThreadPoolExecutor(max_workers=1) as pool:
                future = pool.submit(race.run_remainder,
                                     [race.SQLITE, 'github.com/z-chenhao/assistant/new/pkg'], folder, stopped)
                try:
                    deadline = time.monotonic() + 3
                    while len(list(folder.glob('*.ready'))) != 4 and time.monotonic() < deadline:
                        time.sleep(.02)
                    self.assertEqual(len(list(folder.glob('*.ready'))), 4)
                finally:
                    stopped.set()
                    result = future.result(timeout=12)
            self.assertNotEqual(result['returncode'], 0)
            self.assertTrue(result['owned_group_drained'])
            self.assertEqual(len(result['workers']), 5)
            for worker in result['workers']:
                self.assertEqual(worker['returncode'], 0 if worker['name'] == 'sqlite-serial' else 130)
                self.assertTrue(worker['owned_group_drained'])
