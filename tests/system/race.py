#!/usr/bin/env python3
"""Run every race package; isolate tight deadlines and audit SQLite partitions.

SQLite tests own separate stores/temp roots. Separate processes also isolate
test globals and environment. Every discovered root runs exactly once, with
unchanged race/count/deadline flags and bounded CPU admission.
"""
from collections import Counter
from functools import partial
from concurrent.futures import ThreadPoolExecutor
import argparse
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import threading
import time

from runner import run_command, source_state

BRIDGE = 'github.com/z-chenhao/assistant/internal/llm/bridge'
RECORDER = 'github.com/z-chenhao/assistant/internal/observability/sqlite'
SQLITE = 'github.com/z-chenhao/assistant/internal/agent/sqlite'
APP = 'github.com/z-chenhao/assistant/internal/app'
APP_PATTERN = '^Test(ConnectedApps|Apps)'
APPS_SQLITE_PATTERN = r'^Test(ConnectedApps|ConnectedAppNamespaces|Apps|Editorial|CatalogueOrder|InventoryOrder|Input(Stream|Chunks|Rejects)|AppLogo|ToolDescriptor|RegistryGroup|Client)'
GITHUB_SQLITE_PATTERN = r'^Test(GitHub.*|StartupReconcilesRemovedOrChangedGitHubScopeBeforeActor|ShippedPluginDistributionRejectsPartialNativeFallback|MigrationCatalogContainsEveryForwardSource)$'
CAPABILITIES_SQLITE_PATTERN = 'Test(MainQueuesSecondRequest|SerialMain|Recall.*|ConversationalRecall.*|.*UI.*|ReplyStream.*|InboundAdmission|Files|AccountFiles|.*Refresh.*|.*ModelMenu.*|Capability.*|Guided.*|FeishuStatus.*|FeedbackProjection.*|FeedbackUnavailableProjection.*|DeliveryProjection.*|FrozenDelivery.*|ManagedCard.*|CardPresentation.*|TerminalInterface.*|ModelPicker.*|ModelConnection.*|ModelSettings.*|SettingsDraft.*)'
SQLITE_PATTERNS = {'apps-sqlite': APPS_SQLITE_PATTERN, 'github-sqlite': GITHUB_SQLITE_PATTERN,
                   'capabilities-sqlite': CAPABILITIES_SQLITE_PATTERN}
APP_BATCH_SIZE = 12
RACE = ['go', 'test', '-race', '-count=1', '-timeout=15m']
MEMORY_PATTERN = 'Memory|Layered|Semantic|AutomaticRecall|Recall|Forget|Evidence|ObjectIDs|RootFailureNotice'
TEN_MINUTE_RACE = ['go', 'test', '-race', '-count=1', '-timeout=10m']
# The native and managed forms share one unchanged five-second context across
# three approval/Drain phases. Give both the full CPU budget before overlap.
SERIAL_SQLITE = ['TestChildApprovalIsPerInvocationAndWaitingTreeReleasesLeases',
                 'TestDelegationPluginApprovalIsPerInvocationAndWaitingTreeReleasesLeases']
# Independently compiled plugin and retained-version cases have enlarged this
# package. Bound process batches rather than relaxing its aggregate alarm.
SQLITE_BATCH_SIZE = 8


def show_failure(path, code):
    # Keep the initiating failure visible in CI; cancelled siblings must not
    # bury it in their partial Go JSON output. Full logs remain in artifacts.
    if code not in (0, 130) and path.is_file():
        print(path.read_text(errors='replace')[-8000:], file=sys.stderr, flush=True)


def groups(packages, part=None):
    if (not packages or len(set(packages)) != len(packages) or
            not {BRIDGE, RECORDER, SQLITE} <= set(packages) or
            any(not isinstance(p, str) or not p.startswith('github.com/z-chenhao/assistant/') or
                any(c.isspace() for c in p) for p in packages)):
        raise ValueError('invalid or incomplete Go package inventory')
    if part == 'apps':
        if APP not in packages:
            raise ValueError('missing App package inventory')
        return [('app', [APP])]
    if part in ('sqlite', 'memory') or part in SQLITE_PATTERNS:
        return [('remainder', [SQLITE])]
    # The recorder intentionally drops diagnostics after 100 ms. Its 60 ms
    # lock-release contract needs CPU time, just like Bridge's RPC deadlines;
    # do not overlap either package with the instrumented SQLite partitions.
    isolated = [('bridge', [BRIDGE]), ('recorder', [RECORDER])]
    if APP in packages:
        isolated.append(('app', [APP]))
    if part == 'packages':
        others = [p for p in packages if p not in (BRIDGE, RECORDER, SQLITE, APP)]
        return isolated + ([('other-packages', others)] if others else [])
    if part is not None:
        raise ValueError('unknown race part')
    return isolated + [('remainder', [p for p in packages if p not in (BRIDGE, RECORDER, APP)])]


def cpu_budget():
    limits = [4, os.cpu_count() or 1]
    if hasattr(os, 'sched_getaffinity'):
        limits.append(len(os.sched_getaffinity(0)))
    if os.environ.get('GOMAXPROCS'):
        limits.append(int(os.environ['GOMAXPROCS']))
    if min(limits) < 1:
        raise ValueError('invalid race CPU budget')
    return min(limits)


def test_names(text, package=SQLITE):
    names, summaries = [], 0
    for line in text.splitlines():
        if re.fullmatch(r'(Test|Example|Fuzz)[A-Za-z0-9_]*', line):
            if summaries:
                raise ValueError('tests follow the discovery terminator')
            names.append(line)
        elif re.fullmatch(r'Benchmark[A-Za-z0-9_]*', line):
            continue  # Ordinary go test does not execute benchmarks.
        elif line.split()[:2] == ['ok', package]:
            summaries += 1
        elif not line.strip():
            continue
        else:
            raise ValueError('unrecognized Go test discovery output')
    if not names or len(names) != len(set(names)) or summaries != 1:
        raise ValueError('missing or duplicate Go root inventory')
    return names


def timing_hints():
    return json.loads(Path(__file__).with_name('race_timings.json').read_text())['seconds']


def partitions(names, workers, seconds=None):
    if not names or len(names) != len(set(names)) or workers < 1:
        raise ValueError('invalid SQLite partition inventory')
    count = min(workers, len(names))
    if seconds is None:
        return [names[i::workers] for i in range(count)]
    if any(isinstance(value, bool) or not isinstance(value, (int, float)) or
           not math.isfinite(value) or value <= 0 for value in seconds.values()):
        raise ValueError('invalid SQLite timing hint')
    known = [seconds[name] for name in names if name in seconds]
    fallback = sum(known) / len(known) if known else 1
    # Hints affect placement only. Discovery admits every new Test/Example/Fuzz
    # root; removed names in historical measurements cannot enter execution.
    costs = {name: seconds.get(name, fallback) for name in names}
    slices, loads = [[] for _ in range(count)], [0] * count
    for name in sorted(names, key=lambda name: (-costs[name], name)):
        target = min(range(count), key=lambda i: (loads[i], len(slices[i]), i))
        slices[target].append(name)
        loads[target] += costs[name]
    return slices


def root_verdict(path, expected, package):
    observed, packages, outcomes = Counter(), [], {}
    for line in path.read_text().splitlines():
        event = json.loads(line)
        if not isinstance(event, dict):
            raise ValueError('malformed Go test event')
        name, action = event.get('Test'), event.get('Action')
        if name and '/' not in name and action in ('pass', 'skip', 'fail'):
            if event.get('Package') != package:
                raise ValueError('foreign Go root evidence')
            observed[name] += 1
            outcomes[name] = dict(outcome=action, elapsed_seconds=event.get('Elapsed'))
        elif not name and event.get('Package') == package and action in ('pass', 'skip', 'fail'):
            packages.append(action)
    if (observed != Counter(expected) or packages != ['pass'] or
            any(row['outcome'] == 'fail' for row in outcomes.values())):
        raise ValueError('missing, duplicate, unexpected or failed Go roots')
    return outcomes


def drive_batch(job, folder, stopped, package):
    name, command, expected, cpus = job
    output, metrics = folder / (name + '.log'), {}
    env = dict(os.environ, GOMAXPROCS=str(cpus))
    began = time.monotonic()
    code = run_command(command, env, output, stopped, metrics)
    result = dict(name=name, command=command, returncode=code,
                  wall_seconds=time.monotonic() - began, gomaxprocs=cpus, **metrics)
    if expected is not None:
        result['expected_tests'] = expected
        if code == 0:
            try:
                result['tests'] = root_verdict(output, expected, package)
            except (ValueError, OSError, KeyError) as error:
                result.update(returncode=125, evidence_error=str(error))
    if result['returncode']:
        stopped.set()
        show_failure(output, result['returncode'])
    print(f"[race] {name}: {result['wall_seconds']:.2f}s (exit {result['returncode']})", flush=True)
    return result


def run_remainder(selected, folder, stopped, memory=False, pattern=None):
    started = time.monotonic()
    flags = TEN_MINUTE_RACE if memory or pattern == CAPABILITIES_SQLITE_PATTERN else RACE
    discovery, metrics = flags + ['-list', '.', SQLITE], {}
    listing = folder / 'sqlite-list.log'
    code = run_command(discovery, os.environ.copy(), listing, stopped, metrics)
    row = dict(name='remainder', packages=selected, discovery=discovery,
               discovery_metrics=metrics, returncode=code, workers=[])
    if code:
        show_failure(listing, code)
        return dict(row, wall_seconds=time.monotonic() - started)
    names = test_names(listing.read_text())
    if not set(SERIAL_SQLITE) <= set(names):
        raise ValueError('timing-sensitive SQLite inventory changed')
    if memory or pattern:
        names = [name for name in names if re.search(MEMORY_PATTERN if memory else pattern, name)]
        if not names:
            raise ValueError('missing selected SQLite race inventory')
    serial = [name for name in SERIAL_SQLITE if name in names]
    budget = cpu_budget()
    seconds = timing_hints()
    sqlite_workers = max(1, min(3, budget - 1))
    slices = [group[start:start + SQLITE_BATCH_SIZE]
              for group in partitions([n for n in names if n not in serial], sqlite_workers, seconds)
              for start in range(0, len(group), SQLITE_BATCH_SIZE)]
    row.update(partition_strategy='longest_measured_first', timing_hints=seconds,
               unmeasured_tests=[n for n in names if n not in seconds],
               sqlite_workers=sqlite_workers, maximum_batch_roots=SQLITE_BATCH_SIZE)
    jobs = [(f'sqlite-{i + 1}', flags + ['-json', '-run', '^(' + '|'.join(group) + ')$', SQLITE], group, 1)
            for i, group in enumerate(slices)]
    others = [p for p in selected if p != SQLITE]
    if others:
        jobs.append(('other-packages', flags + ['-p=1', *others], None, 1))
    drive = partial(drive_batch, folder=folder, stopped=stopped, package=SQLITE)
    workers = [drive(('sqlite-serial', flags + ['-json', '-run', '^(' + '|'.join(serial) + ')$', SQLITE],
                      serial, budget))] if serial else []
    if not workers or workers[0]['returncode'] == 0:
        with ThreadPoolExecutor(max_workers=min(budget, sqlite_workers + bool(others))) as pool:
            workers.extend(pool.map(drive, jobs))
    observed = Counter(name for worker in workers for name in worker.get('tests', {}))
    drained = metrics.get('owned_group_drained') is True and all(
        worker.get('owned_group_drained') is True for worker in workers)
    # wait4 in run_command measures each owned child independently, so summing
    # concurrent workers does not double-count global RUSAGE_CHILDREN deltas.
    costs = [metrics.get('cpu_seconds'), *[w.get('cpu_seconds') for w in workers]]
    return dict(row, expected_tests=names, isolated_tests=serial, cpu_budget=budget, workers=workers,
                selection_complete=observed == Counter(names),
                returncode=0 if drained and observed == Counter(names) and all(w['returncode'] == 0 for w in workers) else 1,
                owned_group_drained=drained,
                cpu_seconds=sum(costs) if all(c is not None for c in costs) else None,
                wall_seconds=time.monotonic() - started)



def run_app(selected, folder, stopped, pattern='.'):
    started = time.monotonic()
    discovery = RACE + ['-list', pattern, APP]
    listing, metrics = folder / 'app-list.log', {}
    code = run_command(discovery, os.environ.copy(), listing, stopped, metrics)
    row = dict(name='app', packages=selected, discovery=discovery, discovery_metrics=metrics)
    if code:
        show_failure(listing, code)
        return dict(row, returncode=code, wall_seconds=time.monotonic() - started)
    names = test_names(listing.read_text(), APP)
    batches = [names[start:start + APP_BATCH_SIZE] for start in range(0, len(names), APP_BATCH_SIZE)]
    jobs = [(f'app-{i + 1}', RACE + ['-p=1', '-json', '-run', '^(' + '|'.join(batch) + ')$', APP], batch, 1)
            for i, batch in enumerate(batches)]
    budget = min(2, cpu_budget())
    drive = partial(drive_batch, folder=folder, stopped=stopped, package=APP)
    with ThreadPoolExecutor(max_workers=budget) as pool:
        workers = list(pool.map(drive, jobs))
    observed = Counter(name for worker in workers for name in worker.get('tests', {}))
    drained = metrics.get('owned_group_drained') is True and all(
        worker.get('owned_group_drained') is True for worker in workers)
    costs = [metrics.get('cpu_seconds'), *[w.get('cpu_seconds') for w in workers]]
    return dict(row, expected_tests=names, maximum_batch_roots=APP_BATCH_SIZE, cpu_budget=budget, workers=workers,
                selection_complete=observed == Counter(names), owned_group_drained=drained,
                returncode=0 if drained and observed == Counter(names) and all(w['returncode'] == 0 for w in workers) else 1,
                cpu_seconds=sum(costs) if all(c is not None for c in costs) else None,
                wall_seconds=time.monotonic() - started)


def main(part=None):
    source = source_state()
    packages = subprocess.check_output(['go', 'list', './...'], text=True).splitlines()
    plan = groups(packages, part)
    selected_packages = [p for _, selected in plan for p in selected]
    folder = Path('evidence/race-apps' if part == 'apps' else 'evidence/race')
    folder.mkdir(parents=True, exist_ok=True)
    for name in ('bridge', 'recorder', 'remainder', 'other-packages'):
        (folder / (name + '.log')).unlink(missing_ok=True)
    for pattern in ('sqlite-*.log', 'app-*.log'):
        for path in folder.glob(pattern):
            path.unlink()
    (folder / 'inventory.json').unlink(missing_ok=True)
    stopped = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stopped.set())
    rows = []
    passed = True
    try:
        for name, selected in plan:
            if not selected:
                continue
            if name == 'remainder':
                row = run_remainder(selected, folder, stopped, memory=part == 'memory',
                                    pattern=SQLITE_PATTERNS.get(part))
            elif name == 'app':
                row = run_app(selected, folder, stopped, APP_PATTERN if part == 'apps' else '.')
            else:
                command, metrics = RACE + (['-p=1'] if name == 'other-packages' else []) + selected, {}
                env = dict(os.environ, GOMAXPROCS='1') if name == 'other-packages' else os.environ.copy()
                started = time.monotonic()
                code = run_command(command, env, folder / (name + '.log'), stopped, metrics)
                row = dict(name=name, packages=selected, command=command, returncode=code,
                           wall_seconds=time.monotonic() - started, **metrics)
                show_failure(folder / (name + '.log'), code)
            rows.append(row)
            if row['returncode']:
                passed = False
                break
    finally:
        stable = source == source_state()
        executed = [p for row in rows if row['returncode'] == 0 for p in row['packages']]
        passed = (passed and stable and not stopped.is_set() and sorted(executed) == sorted(selected_packages)
                  and all(row.get('owned_group_drained') is True for row in rows))
        report = dict(version=1, **source, source_stable=stable, packages=packages,
                      part=part, selected_packages=selected_packages, stages=rows, passed=passed)
        (folder / 'inventory.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if passed else 1


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--part', choices=['packages', 'sqlite', 'memory', 'apps', *SQLITE_PATTERNS])
    raise SystemExit(main(parser.parse_args().part))
