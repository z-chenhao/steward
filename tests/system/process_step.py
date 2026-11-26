#!/usr/bin/env python3
"""Record one CI phase, including failure and graceful cancellation evidence."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform
import signal
import tempfile
import threading
import time

from runner import run_command, source_state, runner_hardware


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--name', required=True)
    parser.add_argument('--phase', choices=['setup', 'build', 'test', 'cleanup'], required=True)
    parser.add_argument('--evidence', type=Path, default=Path('evidence/steps'), help='phase record directory')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command or not args.name.replace('-', '').isalnum():
        parser.error('command and a simple step name required')
    folder = args.evidence
    folder.mkdir(parents=True, exist_ok=True)
    stopped = threading.Event()
    cancel_received_at = None
    def stop(signum, *_):
        nonlocal cancel_received_at
        if cancel_received_at is None:
            cancel_received_at = datetime.now(timezone.utc).isoformat()
            stopped.set()
            receipt = folder / (args.name + '-cancel.json')
            temporary = receipt.with_suffix('.tmp')
            temporary.write_text(json.dumps(dict(version=1, name=args.name,
                cancel_received_at=cancel_received_at, signal=signum, owner_pid=os.getpid())) + '\n')
            temporary.replace(receipt)
        stopped.set()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, stop)
    metrics = {}
    source = source_state()
    started_at = datetime.now(timezone.utc).isoformat()
    started = time.monotonic()
    code = 127
    try:
        env = os.environ.copy()
        if env.get('STEWARD_TEST_BUILD_CACHE_DIR'):
            code = run_command(command, env, folder / (args.name + '.log'), stopped, metrics)
        else:
            with tempfile.TemporaryDirectory(prefix='steward-step-bins-') as build_cache:
                env['STEWARD_TEST_BUILD_CACHE_DIR'] = build_cache
                code = run_command(command, env, folder / (args.name + '.log'), stopped, metrics)
    finally:
        stable = source == source_state()
        if not stable:
            code = 125
        record = dict(version=1, name=args.name, phase=args.phase, command=command, returncode=code,
                      **source, source_stable=stable,
                      started_at=started_at, finished_at=datetime.now(timezone.utc).isoformat(),
                      cancel_received_at=cancel_received_at,
                      wall_seconds=time.monotonic() - started, runner=platform.platform(),
                      image_os=os.environ.get('ImageOS'), image_version=os.environ.get('ImageVersion'),
                      hardware=runner_hardware(),
                      go_cache_hit=os.environ.get('CI_GO_CACHE_HIT') or None,
                      npm_cache_hit=os.environ.get('CI_NPM_CACHE_HIT') or None, **metrics)
        (folder / (args.name + '.json')).write_text(json.dumps(record, indent=2) + '\n')
        print(json.dumps(record), flush=True)
        if code:
            print((folder / (args.name + '.log')).read_text()[-12000:], flush=True)
    return code


if __name__ == '__main__':
    raise SystemExit(main())
