#!/usr/bin/env python3
"""Required real-container regressions; absence of Docker is a failure."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time

from runner import source_state
from verify_coding_model import verify as verify_model

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    args.evidence.mkdir(parents=True, exist_ok=False)
    source = source_state()
    fixture = json.loads((ROOT / 'tests/fixtures/coding-container.json').read_text())
    socket = os.environ.get('STEWARD_TEST_PROJECT_CONTAINER_SOCKET', '/var/run/docker.sock')
    if not socket.startswith('/'):
        raise ValueError('only a local Unix socket is supported')
    report = dict(outcome='failed', protocol=fixture['protocol_reference'], source=source)
    at = time.monotonic()
    work = (args.evidence / 'work').resolve()
    work.mkdir(exist_ok=True)
    base = None
    cleanup_error = None
    config = tempfile.TemporaryDirectory(prefix='steward-coding-docker-')
    try:
        client = Path(shutil.which('docker') or '/missing-docker').resolve(strict=True)
        report['cli_path'] = str(client)
        report['cli_sha256'] = hashlib.sha256(client.read_bytes()).hexdigest()
        base = [str(client), '--config', config.name, '--host', 'unix://' + socket]
        version = json.loads(subprocess.check_output(base + ['version', '--format', '{{json .}}'], text=True))
        report['version'] = version
        server = version['Server']
        if server['Os'] != 'linux' or server['Arch'] not in fixture['artifacts']:
            raise ValueError('unsupported container platform')
        artifact = fixture['artifacts'][server['Arch']]
        filename = f"alpine-minirootfs-{fixture['version']}-{artifact['arch']}.tar.gz"
        url = fixture['source'] + artifact['arch'] + '/' + filename
        tar = args.evidence / filename
        subprocess.run(['curl', '--fail', '--location', '--silent', '--show-error', '--max-time', '90', url, '-o', str(tar)], check=True)
        actual = hashlib.sha256(tar.read_bytes()).hexdigest()
        if actual != artifact['sha256']:
            raise ValueError('official fixture digest mismatch')
        report['artifact'] = dict(url=url, sha256=actual, bytes=tar.stat().st_size)
        image = subprocess.check_output(base + ['image', 'import', str(tar)], text=True).strip()
        if not image.startswith('sha256:') or len(image) != 71:
            raise ValueError('missing immutable fixture image')
        report['image'] = image
        env = dict(os.environ, STEWARD_TEST_PROJECT_CONTAINER_SOCKET=socket,
                   STEWARD_TEST_PROJECT_CONTAINER_IMAGE=image, TMPDIR=str(work))
        command = ['go', 'test', '-json', '-race', '-p', '1', './internal/computer/projectcontainer',
                   './internal/agent/sqlite', '-run', '^Test(CodingContainer|CodingProject)',
                   '-count=1', '-timeout=15m']
        report['command'] = command
        with (args.evidence / 'regressions.log').open('w') as output:
            with subprocess.Popen(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True) as child:
                try:
                    child.wait(timeout=900)
                    result = child
                finally:
                    try:
                        os.killpg(child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    child.wait()
        report['returncode'] = result.returncode
        events = [json.loads(line) for line in (args.evidence / 'regressions.log').read_text().splitlines()]
        roots = sorted(event['Test'] for event in events if event.get('Action') == 'pass'
                       and 'Test' in event and '/' not in event['Test'])
        skipped = sorted(event.get('Test', event['Package']) for event in events if event.get('Action') == 'skip')
        report.update(test_roots=roots, skipped=skipped)
        required = {'TestCodingContainerActualParallelPortsStateAndSourceIsolation',
                    'TestCodingContainerActualCancellationKillsDetachedDescendants',
                    'TestCodingProjectExactApprovedLifecycleUsesFrozenImage',
                    'TestCodingProjectRealCodeModeKeepsOneApprovedInvocationAndImage',
                    'TestCodingProjectRecoveryCleansOriginalResourceWithoutReverification',
                    'TestCodingProjectStatusSeparatesCurrentEnvironmentFromRetainedPass',
                    'TestCodingProjectStaleOwnerCannotPublishReceipt',
                    'TestCodingProjectRecoveryDoesNotLoseDelayedRealCreate',
                    'TestCodingProjectFirstEnvironmentMustMatchConcreteApproval',
                    'TestCodingProjectEvidenceUsesManagedAtomicArtifactsAfterContainerRemoval',
                    'TestCodingProjectEvidenceTerminalFailureRollsBackRefsWithoutReexecution',
                    'TestCodingProjectEvidenceLateForgetReleasesResourcesWithoutPublishing',
                    'TestCodingProjectMissingEvidenceDoesNotTurnZeroExitIntoAcceptance'}
        if not result.returncode and (skipped or not required.issubset(roots)):
            raise RuntimeError('required actual container scenarios did not all run')
        if result.returncode:
            print((args.evidence / 'regressions.log').read_text()[-12000:])
            return result.returncode
        report['model'] = verify_model(args.evidence / 'model', env, roots)['outcome']
        report['outcome'] = 'passed'
        return 0
    finally:
        if base is not None:
            try:
                ids = subprocess.check_output(base + ['ps', '-aq', '--filter', 'label=ai.steward.project.invocation'], text=True, timeout=10).split()
                for identity in ids:
                    current = json.loads(subprocess.check_output(base + ['inspect', identity], text=True, timeout=10))[0]
                    token = current['Config'].get('Labels', {}).get('ai.steward.project.invocation', '')
                    expected = '/steward-project-' + hashlib.sha256(token.encode()).hexdigest()[:32]
                    mounts = current.get('Mounts', [])
                    scoped = any(m.get('Destination') == '/workspace' and Path(m.get('Source', '/')).resolve().is_relative_to(work) for m in mounts)
                    if token and current.get('Name') == expected and scoped:
                        subprocess.run(base + ['rm', '--force', current['Id']], check=True, timeout=10, stdout=subprocess.DEVNULL)
                report['owned_cleanup'] = 'confirmed'
            except (subprocess.SubprocessError, KeyError, ValueError):
                report['owned_cleanup'] = 'unconfirmed'
                report['outcome'] = 'failed'
                cleanup_error = True
        config.cleanup()
        report['source_stable'] = source == source_state()
        if not report['source_stable']:
            report['outcome'] = 'failed'
        report['wall_seconds'] = time.monotonic() - at
        (args.evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
        if cleanup_error:
            raise RuntimeError('owned container cleanup could not be confirmed')
        if not report['source_stable']:
            raise RuntimeError('source changed during the container gate')


if __name__ == '__main__':
    raise SystemExit(main())
