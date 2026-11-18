#!/usr/bin/env python3
"""Required real database/browser/recovery gate; no unavailable-image skips."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time

from fixture_pool import run_json_lines
from runner import source_state

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from prepare_coding_image import check_output_path, prepare, stop, snapshot_source


def verify_preparation_cancellation(evidence, cache, socket, docker):
    """The real owner prep must release its observed container within CI grace."""
    log = evidence.parent / 'preparation-cancel.log'
    command = [sys.executable, str(ROOT / 'scripts/prepare_coding_image.py'),
               '--evidence', str(evidence), '--cache', str(cache), '--socket', socket]
    original = None
    with log.open('w') as output, subprocess.Popen(command, cwd=ROOT, stdout=output,
            stderr=subprocess.STDOUT, start_new_session=True) as child:
        try:
            deadline = time.monotonic() + 120
            while time.monotonic() < deadline:
                progress = evidence / 'progress.json'
                try:
                    facts = json.loads(progress.read_text())
                except (FileNotFoundError, json.JSONDecodeError):
                    facts = {}
                identity = facts.get('container_id')
                if identity:
                    actual = json.loads(subprocess.check_output(docker + ['inspect', identity], text=True, timeout=3))[0]
                    name = facts['container_name']
                    if actual['Id'] != identity or actual['Name'] != '/' + name or actual['Config']['Labels'].get('ai.steward.coding-preparation') != name:
                        raise RuntimeError('original preparation ownership differs')
                    original = identity
                    if actual['State']['Running']:
                        break
                if child.poll() is not None:
                    raise RuntimeError('preparation exited before real cancellation')
                time.sleep(.05)
            else:
                raise RuntimeError('original preparation did not become running')
            started = time.monotonic()
            os.killpg(child.pid, signal.SIGTERM)
            child.wait(timeout=5)
            cancelled = json.loads((evidence / 'report.json').read_text())
            # Check immediately after cancellation; no repair precedes this assertion.
            remaining = subprocess.check_output(docker + ['ps', '-aq', '--no-trunc', '--filter', 'id=' + original], text=True, timeout=3).split()
            if child.returncode == 0 or cancelled.get('outcome') != 'failed' or not cancelled.get('interrupted') or cancelled.get('owned_cleanup') != 'confirmed' or remaining:
                raise RuntimeError('cancelled preparation retained its resource or claimed ready')
            return dict(outcome='passed', container_id=original, seconds=time.monotonic() - started)
        finally:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            child.wait()
            if original:
                # Failure cleanup preserves the failed assertion above.
                subprocess.run(docker + ['rm', '--force', original], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)


def main():
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, stop)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--image', default=os.environ.get('STEWARD_TEST_CODING_DEVELOPMENT_IMAGE'))
    parser.add_argument('--socket', default=os.environ.get('STEWARD_TEST_PROJECT_CONTAINER_SOCKET', '/var/run/docker.sock'))
    parser.add_argument('--cache', type=Path, default=ROOT / 'evidence/coding-image-inputs')
    args = parser.parse_args()
    args.evidence = args.evidence.resolve()
    check_output_path(args.evidence)
    check_output_path(args.cache)
    args.evidence.mkdir(parents=True, exist_ok=False)
    source = source_state()
    report = dict(outcome='failed', source=source)
    started = time.monotonic()
    config = tempfile.TemporaryDirectory(prefix='steward-development-docker-')
    docker = None
    work = args.evidence / 'work'
    work.mkdir()
    try:
        if not Path(args.socket).is_absolute():
            raise ValueError('local Unix socket required')
        client = Path(shutil.which('docker') or '/missing-docker').resolve(strict=True)
        report['cli_sha256'] = hashlib.sha256(client.read_bytes()).hexdigest()
        docker = [str(client), '--config', config.name, '--host', 'unix://' + args.socket]
        report['version'] = json.loads(subprocess.check_output(docker + ['version', '--format', '{{json .}}'], text=True))
        image = args.image
        if image is None:
            report['preparation_cancellation'] = verify_preparation_cancellation(args.evidence / 'preparation-cancel', args.cache.resolve(), args.socket, docker)
            result = prepare(args.evidence / 'preparation', args.cache.resolve(), args.socket)
            image = result['image']
            report['preparation'] = 'passed'
        else:
            report['preparation'] = 'explicit prebuilt image; this run does not prove cold preparation'
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', image):
            raise ValueError('an immutable prepared image ID is required')
        report['image'] = image
        checkout=args.evidence/'checkout'
        inventory=snapshot_source(checkout)
        (args.evidence/'source-inventory.json').write_text(json.dumps(inventory,indent=2)+'\n')
        env = dict(os.environ, STEWARD_TEST_PROJECT_CONTAINER_SOCKET=args.socket,
                   STEWARD_TEST_CODING_DEVELOPMENT_IMAGE=image, STEWARD_TEST_CODING_SOURCE=str(checkout),
                   STEWARD_CODING_DEVELOPMENT_EVIDENCE=str(args.evidence / 'flows'), TMPDIR=str(work))
        command = ['go', 'test', '-json', '-race', '-p', '1', './internal/agent/sqlite',
                   '-run', '^TestCodingDevelopment', '-count=1', '-timeout=10m']
        report['command'] = command
        log = args.evidence / 'regressions.log'
        returncode, events = run_json_lines(command, log, cwd=ROOT, env=env, timeout=620)
        report['returncode'] = returncode
        passed = sorted(event['Test'] for event in events if event.get('Action') == 'pass' and 'Test' in event and '/' not in event['Test'])
        skipped = [event.get('Test', event.get('Package')) for event in events if event.get('Action') == 'skip']
        report.update(test_roots=passed, skipped=skipped)
        required = {'TestCodingDevelopmentActualSQLiteBrowserRecoveryAndRetainedArtifacts',
                    'TestCodingDevelopmentParallelPortDatabaseAndBrowserProfiles',
                    'TestCodingDevelopmentExactFreshStewardBuild'}
        if returncode or skipped or not required.issubset(passed):
            print(log.read_text()[-12000:])
            print(log.with_suffix('.stderr.log').read_text()[-4000:])
            raise RuntimeError('required real development scenarios did not all pass')
        report['flows'] = {}
        for marker in ('candidate-alpha', 'parallel-alpha', 'parallel-beta'):
            folder = args.evidence / 'flows' / marker
            facts = json.loads((folder / 'assertions.json').read_text())
            receipt = json.loads((folder / 'receipt.json').read_text())
            picture = folder / 'flow.png'
            if facts['persistedRows'] != [marker] or not facts['browserProfileRetained'] or receipt['outcome'] != 'passed' or picture.stat().st_size < 1024:
                raise RuntimeError('actual persisted effects or nonempty browser evidence missing')
            report['flows'][marker] = dict(facts=facts, png_sha256=hashlib.sha256(picture.read_bytes()).hexdigest(), candidate_sha256=receipt['candidate_sha256'], environment_sha256=receipt['environment_sha256'])
        report['outcome'] = 'passed'
        return 0
    finally:
        try:
            if docker:
                ids = subprocess.check_output(docker + ['ps', '-aq', '--filter', 'label=ai.steward.project.invocation'], text=True, timeout=10).split()
                for identity in ids:
                    current = json.loads(subprocess.check_output(docker + ['inspect', identity], text=True, timeout=10))[0]
                    token = current['Config'].get('Labels', {}).get('ai.steward.project.invocation', '')
                    name = '/steward-project-' + hashlib.sha256(token.encode()).hexdigest()[:32]
                    scoped = any(m.get('Destination') == '/workspace' and Path(m.get('Source', '/')).resolve().is_relative_to(work) for m in current.get('Mounts', []))
                    if token and current.get('Name') == name and scoped:
                        subprocess.run(docker + ['rm', '--force', current['Id']], check=True, timeout=10, stdout=subprocess.DEVNULL)
                report['owned_cleanup'] = 'confirmed'
        except BaseException:
            report.update(outcome='failed', owned_cleanup='unconfirmed')
            raise
        finally:
            config.cleanup()
            report['source_stable'] = source == source_state()
            if not report['source_stable']:
                report['outcome'] = 'failed'
            report['wall_seconds'] = time.monotonic() - started
            (args.evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
        if not report['source_stable']:
            raise RuntimeError('source changed during the development gate')


if __name__ == '__main__':
    raise SystemExit(main())
