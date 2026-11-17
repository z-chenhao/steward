#!/usr/bin/env python3
"""Deterministic application regressions; no model evaluation or release grading."""
import argparse
import errno
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import sys
import threading
import time
from dataclasses import dataclass

ROOT = Path(__file__).resolve().parents[2]

@dataclass(frozen=True)
class Gate:
    name: str
    script: str
    attempts: int = 1

CORE = ('startup', 'main', 'tasks', 'children', 'tools', 'artifacts', 'code-recovery',
        'local-provider', 'account', 'model-menu-plugins', 'feishu-plugins', 'capability-form-plugins', 'channels', 'intelligent-ui', 'ui-interactions')
REGISTRY = {'capability-form-plugins': 'verify_capability_form_plugins.py', 'feishu-plugins': 'verify_feishu_plugins.py', 'model-menu-plugins': 'verify_model_menu_plugins.py', 'ui-interactions': 'verify_ui_interactions.py', 'intelligent-ui': 'verify_intelligent_ui.py', 'foundation': 'verify.py', 'account': 'verify_account.py', 'artifacts': 'verify_artifacts.py', 'automation': 'verify_automation.py', 'capabilities': 'verify_capabilities.py', 'capacity': 'verify_capacity.py', 'channels': 'verify_channels.py', 'children': 'verify_children.py', 'code-recovery': 'verify_code_recovery.py', 'codemode': 'verify_codemode.py', 'computer': 'verify_computer.py', 'computer-use': 'verify_computer_use.py', 'computer-plugins': 'verify_computer_plugins.py', 'web-plugins': 'verify_web_plugins.py', 'mcp-plugins': 'verify_mcp_plugins.py', 'connected-apps-plugins': 'verify_connected_apps_plugins.py', 'tool-discovery-plugins': 'verify_tool_discovery_plugins.py', 'github-plugins': 'verify_github_plugins.py', 'artifact-plugins': 'verify_artifact_plugins.py', 'delivery': 'verify_delivery.py', 'local-provider': 'verify_local_provider.py', 'main': 'verify_main.py', 'memory-maintenance': 'verify_memory_maintenance.py', 'operations': 'verify_operations.py', 'recall': 'verify_recall.py', 'scheduling': 'verify_scheduling.py', 'spend': 'verify_spend.py', 'startup': 'verify_startup.py', 'task-control': 'verify_task_control.py', 'tasks': 'verify_tasks.py', 'terminal-input': 'verify_terminal_input.py', 'tools': 'verify_tools.py', 'tui': 'verify_tui.py'}


def source_state():
    source = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).strip().decode()
    diff = subprocess.check_output(['git', 'diff', 'HEAD', '--binary'], cwd=ROOT)
    untracked = subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
    digest = hashlib.sha256(diff)
    for name in sorted(n for n in untracked if n):
        path = ROOT / name
        digest.update(name.encode() + b'\0')
        digest.update(os.readlink(path).encode() if path.is_symlink() else path.read_bytes())
    return dict(source_sha=source, source_dirty=bool(diff or any(untracked)), source_diff_sha256=digest.hexdigest())


def runner_hardware():
    info = dict(logical_cpus=os.cpu_count(), affinity_cpus=None, cpu_model=platform.processor() or None)
    try:
        if hasattr(os, 'sched_getaffinity'):
            info['affinity_cpus'] = len(os.sched_getaffinity(0))
        if sys.platform == 'linux':
            for line in Path('/proc/cpuinfo').read_text().splitlines():
                key, separator, value = line.partition(':')
                if separator and key.strip() == 'model name':
                    info['cpu_model'] = value.strip()
                    break
        elif sys.platform == 'darwin':
            info['cpu_model'] = subprocess.check_output(['sysctl', '-n', 'machdep.cpu.brand_string'], text=True).strip()
    except (OSError, subprocess.CalledProcessError) as error:
        info['unavailable'] = type(error).__name__
    return info


def group_alive(pgid):
    """Ignore exited zombies; they own no running work or listening sockets."""
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # Darwin can return EPERM as an orphaned group disappears. Confirm
        # absence/zombie state with ps; an unreadable process table still fails
        # closed in run_command. Other platforms retain their strict error.
        if sys.platform != 'darwin':
            raise
    rows = subprocess.check_output(['ps', '-axo', 'pgid=,stat='], text=True)
    if not rows.strip():
        raise OSError(errno.EIO, 'process table is empty')
    return any(int(parts[0]) == pgid and not parts[1].startswith('Z')
               for row in rows.splitlines() if len(parts := row.split()) == 2)


def signal_group(pgid, sig):
    try:
        os.killpg(pgid, sig)
    except ProcessLookupError:
        pass
    except PermissionError:
        if sys.platform != 'darwin' or group_alive(pgid):
            raise


def run_command(args, env, output, stopped, metrics=None):
    """Reap owned groups on interruption; Python harnesses unwind cleanup."""
    metrics = metrics if metrics is not None else {}
    metrics.update(cpu_seconds=None, cancel_cleanup_seconds=None)
    if stopped.is_set():
        return 130
    interrupted = None
    with output.open("a") as log:
        with subprocess.Popen(args, cwd=ROOT, env=env, stdout=log,
                              stderr=subprocess.STDOUT, start_new_session=True) as child:
            try:
                while True:
                    pid, status, usage = os.wait4(child.pid, os.WNOHANG)
                    if pid:
                        child.returncode = os.waitstatus_to_exitcode(status)
                        metrics['cpu_seconds'] = usage.ru_utime + usage.ru_stime
                        break
                    if stopped.is_set() and interrupted is None:
                        interrupted = time.monotonic()
                        signal_group(child.pid, signal.SIGTERM)
                    # Leave time to drain and retain evidence before Actions'
                    # 7.5-second SIGINT grace expires at the entry process.
                    if interrupted is not None and time.monotonic() - interrupted > 5:
                        signal_group(child.pid, signal.SIGKILL)
                    time.sleep(.05)
            finally:
                # The group leader can exit before an uncooperative child.
                # Waiting only for the leader used to leave that child alive.
                signal_group(child.pid, signal.SIGKILL)
                deadline = time.monotonic() + 2
                try:
                    while group_alive(child.pid) and time.monotonic() < deadline:
                        time.sleep(.02)
                    metrics['owned_group_drained'] = not group_alive(child.pid)
                except (OSError, subprocess.CalledProcessError):
                    metrics['owned_group_drained'] = False
                if interrupted is not None:
                    metrics['cancel_cleanup_seconds'] = time.monotonic() - interrupted
            if not metrics['owned_group_drained']:
                return 125
            return child.returncode if interrupted is None else 130



def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--suite', choices=('core', 'full'), default='core')
    parser.add_argument('--gate', action='append')
    parser.add_argument('--repeat', type=int, choices=range(1, 6), default=1)
    parser.add_argument('--list', action='store_true')
    parser.add_argument('--binary', type=Path, default=Path('bin/steward'))
    parser.add_argument('--evidence', type=Path, default=Path('evidence/e2e'))
    args = parser.parse_args(argv)
    names = args.gate if args.gate is not None else list(CORE if args.suite == 'core' else REGISTRY)
    if not names or len(names) != len(set(names)) or set(names) - REGISTRY.keys():
        parser.error('select distinct registered gates')
    if args.list:
        print(json.dumps([dict(name=n, script=REGISTRY[n], repetitions=args.repeat) for n in names], indent=2))
        return 0
    binary, evidence = args.binary.resolve(), args.evidence.resolve()
    if not binary.is_file():
        parser.error('binary missing; run make build')
    if evidence.exists() and any(evidence.iterdir()):
        parser.error('choose a fresh evidence directory to retain prior results')
    evidence.mkdir(parents=True, exist_ok=True)
    (evidence / 'logs').mkdir(exist_ok=True)
    before = source_state()
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    stopped = threading.Event()
    handlers = {sig: signal.signal(sig, lambda *_: stopped.set()) for sig in (signal.SIGINT, signal.SIGTERM)}
    rows = []
    started = time.monotonic()
    try:
        for name in names:
            for attempt in range(1, args.repeat + 1):
                output = evidence / 'logs' / f'{name}-{attempt}.log'
                report = evidence / f'{name}-{attempt}.json'
                command = [sys.executable, str(ROOT / 'tests/system/entrypoint.py'),
                           str(ROOT / 'tests/system' / REGISTRY[name]),
                           '--binary', str(binary), '--evidence', str(report)]
                metrics = {}
                tick = time.monotonic()
                try:
                    code = run_command(command, os.environ.copy(), output, stopped, metrics)
                except OSError as error:
                    output.write_text(str(error) + '\n')
                    code = 127
                # A successful launcher must have produced this run's report.
                if code == 0 and not report.is_file():
                    code = 125
                rows.append(dict(gate=name, attempt=attempt, returncode=code,
                                 elapsed_seconds=time.monotonic()-tick, log=str(output), **metrics))
                print(f'{name} {attempt}/{args.repeat}: exit {code}', flush=True)
                if code:
                    if output.exists(): print(output.read_text()[-8000:], file=sys.stderr)
                    stopped.set()
                    break
            if stopped.is_set(): break
    finally:
        for sig, handler in handlers.items(): signal.signal(sig, handler)
    stable = before == source_state() and binary_hash == hashlib.sha256(binary.read_bytes()).hexdigest()
    passed = (not stopped.is_set() and stable and len(rows) == len(names) * args.repeat
              and all(r['returncode'] == 0 for r in rows))
    result = dict(version=1, selected=names, repetitions=args.repeat, results=rows,
                  source=before, source_stable=stable, binary_sha256=binary_hash,
                  elapsed_seconds=time.monotonic()-started, passed=passed,
                  scope='deterministic software regression; no Agent quality score')
    (evidence / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
    return 0 if passed else 1

if __name__ == '__main__':
    raise SystemExit(main())
