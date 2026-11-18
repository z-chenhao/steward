"""Check the bounded model and replay its stale-owner mutant in actual SQLite.

Called by the mandatory real-container gate after its unmodified Go regressions.
The Go overlay changes only the claim token/epoch SQL predicates. Production
sources and databases are never edited by mutation testing.
"""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess

from coding_verification_model import explore

ROOT = Path(__file__).resolve().parents[2]
REPLAY = 'TestCodingProjectStaleOwnerCannotPublishReceipt'


def verify(evidence, env, baseline_roots):
    evidence.mkdir(parents=True, exist_ok=False)
    baseline = explore()
    mutant = explore(stale_receipt=True)
    report = dict(outcome='failed', bounds=dict(invocations=1, ownership_epochs=2),
                  model_sha256=hashlib.sha256((ROOT / 'tests/system/coding_verification_model.py').read_bytes()).hexdigest(),
                  baseline=baseline, mutant=mutant, actual_replay=REPLAY)
    try:
        if not baseline['valid'] or mutant['valid'] or REPLAY not in baseline_roots:
            raise RuntimeError('model baseline, counterexample or real baseline missing')
        source = ROOT / 'internal/agent/sqlite/runs.go'
        original = source.read_text()
        before = "WHERE c.run_id=? AND c.token=? AND c.epoch=? AND c.expires_at>? AND r.status='running'"
        after = "WHERE c.run_id=? AND ? IS NOT NULL AND ?>=0 AND c.expires_at>? AND r.status='running'"
        if original.count(before) != 1:
            raise RuntimeError('claim fence mutation no longer maps to exactly one source boundary')
        changed = evidence / '_runs-mutant.go'
        changed.write_text(original.replace(before, after, 1))
        overlay = evidence / 'overlay.json'
        overlay.write_text(json.dumps(dict(Replace={str(source): str(changed.resolve())})))
        report['original_sha256'] = hashlib.sha256(original.encode()).hexdigest()
        report['mutant_sha256'] = hashlib.sha256(changed.read_bytes()).hexdigest()
        command = ['go', 'test', '-json', '-race', '-p', '1', '-overlay', str(overlay.resolve()),
                   './internal/agent/sqlite', '-run', '^' + REPLAY + '$', '-count=1', '-timeout=3m']
        report['command'] = command
        log = evidence / 'actual-mutant.log'
        with log.open('w') as output:
            with subprocess.Popen(command, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True) as child:
                try:
                    child.wait(timeout=200)
                finally:
                    try:
                        os.killpg(child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    child.wait()
        report['returncode'] = child.returncode
        events = [json.loads(line) for line in log.read_text().splitlines()]
        rejected = any(event.get('Action') == 'fail' and event.get('Test') == REPLAY for event in events)
        diagnostic = 'model counterexample: stale owner published a container receipt'
        reproduced = any(diagnostic in event.get('Output', '') for event in events)
        report.update(mutant_rejected=rejected, counterexample_reproduced=reproduced)
        if child.returncode != 1 or not rejected or not reproduced:
            raise RuntimeError('actual mutation did not reproduce the model counterexample')
        if source.read_text() != original:
            raise RuntimeError('production source changed during counterexample replay')
        report['outcome'] = 'passed'
    finally:
        (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    return report
