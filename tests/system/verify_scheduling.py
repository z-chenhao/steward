#!/usr/bin/env python3
"""M4 Direct Tool batch/fairness/cancel evidence via the maintained CLI harness."""
import argparse
import hashlib
import json
from pathlib import Path
from contextlib import closing
import sqlite3
import subprocess
import sys
import tempfile
import time

from terminal_driver import Terminal
from verify_artifacts import ArtifactHarness
from verify_children import state as child_state, check
from verify_main import require
from verify_tasks import select_managed_tasks


def state(h):
    value = child_state(h)
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
        db.row_factory = sqlite3.Row
        db.execute('BEGIN')
        for name, query in {
            'scheduler': 'SELECT * FROM scheduler_tree ORDER BY root_run_id',
            'health': 'SELECT * FROM scheduler_provider ORDER BY provider_key',
            'tree_usage': 'SELECT * FROM run_tree_usage ORDER BY root_run_id',
            'task_usage': 'SELECT * FROM task_usage ORDER BY task_id',
            'account_usage': 'SELECT * FROM account_usage ORDER BY provider_key',
            'run_events': "SELECT type,subject,occurred_at,event_id,payload_json FROM event_outbox WHERE type IN ('ai.steward.run.claimed.v1','ai.steward.run.released.v1','ai.steward.run.waiting.v1','ai.steward.run.completed.v1','ai.steward.run.failed.v1','ai.steward.run.cancelled.v1') ORDER BY occurred_at,event_id",
        }.items():
            value[name] = [dict(row) for row in db.execute(query)]
    return value


def concurrency(value, limit=16):
    nodes = {r['run_id']: r for r in value['runs']}
    live = set()
    peak = 0
    for event in value['run_events']:
        node = nodes[event['subject']]
        if node['kind'] != 'worker':
            continue
        if event['type'] == 'ai.steward.run.claimed.v1':
            require(node['run_id'] not in live, 'duplicate live lease')
            live.add(node['run_id'])
        else:
            live.discard(node['run_id'])
        require(len(live) <= 32, 'global Worker capacity exceeded')
        for tree in value['trees']:
            count = sum(nodes[i]['root_run_id'] == tree['root_run_id'] and nodes[i]['parent_run_id'] is not None for i in live)
            peak = max(peak, count)
            require(count <= limit, 'Child concurrency exceeded configuration')
    return {'peak_children_per_tree': peak, 'remaining_worker_leases': sorted(live)}


def setup(h):
    h.setup(metadata={'name': 'batch', 'provider_kind': 'openai_compatible',
                      'endpoint': 'http://127.0.0.1:1/v1', 'wire_api': 'openai_completions',
                      'model_id': 'fixture-model', 'context_window': 131072,
                      'max_output_tokens': 8192, 'generation_defaults': {},
                      'compatibility': {'tool_result_format': 'openai'},
                      'capabilities': {'text_input': True}})
    select_managed_tasks(h, delegation=True)


def run(binary, evidence=None):
    report = {'version': 1, 'scope': 'M4 Direct Tool scheduling fragments GC-16/17/18/20; semantic scores, JavaScript, CodeCall replay and Computer binding are not_applicable', 'checks': []}
    with tempfile.TemporaryDirectory(prefix='steward-scheduling-') as folder:
        root = Path(folder)

        def scenario(name, action):
            path = root / name
            path.mkdir()
            h = ArtifactHarness(binary, path, fixture='children')
            try:
                setup(h)
                return action(h)
            except BaseException as error:
                if evidence is not None:
                    report['passed'] = False
                    report['failure'] = dict(scenario=name, error=type(error).__name__, message=str(error))
                    try:
                        report['failure']['state'] = state(h)
                    except Exception as diagnostic:
                        report['failure']['diagnostic_error'] = type(diagnostic).__name__
                        if hasattr(h, 'scheduling_diagnostic_state'):
                            report['failure']['state'] = h.scheduling_diagnostic_state
                            report['failure']['snapshot'] = 'last observed before owned-process cancellation'
                    report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
                    evidence.parent.mkdir(parents=True, exist_ok=True)
                    evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
                raise
            finally:
                h.cleanup()

        def batch(h):
            with Terminal(h.args('tui'), h.env, h.root) as t:
                t.expect('You> ')
                t.send('Delegate: Batch 100')
                t.expect('Main follow-up:', timeout=30)
                t.expect('Steward: idle')
                t.send('/status')
                t.expect('Children: queued 0, running 0, succeeded 98, failed 2, waiting 0')
                require(t.stop() == 0, 'batch PTY cleanup')
                transcript = t.transcript()
            value = state(h)
            check(value, children=100)
            children = [r for r in value['runs'] if r['parent_run_id']]
            require(sum(r['status'] == 'completed' for r in children) == 98 and sum(r['status'] == 'failed' for r in children) == 2, 'business failures aborted or duplicated batch')
            invocations = [i for i in value['tools'] if i['tool_name'] == 'builtin__agent_run']
            require({i['provider_call_id'] for i in invocations} == {f'row-{i:03d}' for i in range(1, 101)}, 'row adoption missing or duplicated')
            for invocation in invocations:
                row = int(invocation['provider_call_id'].removeprefix('row-'))
                require(json.loads(invocation['arguments_json']) == {'task': f'Batch item {row}'}, 'row input drift')
                output = json.loads(invocation['result_json'])
                if row in (37, 74):
                    require(invocation['status'] == 'failed' and output['error']['code'] == 'PROVIDER_UNAVAILABLE', 'wrong failed row adopted')
                else:
                    expected = 'Needs input: confirm region' if row in (7, 8) else f'row {row:03d}: classified'
                    require(invocation['status'] == 'succeeded' and json.loads(output['text']) == {'text': expected, 'artifactRefs': []}, 'wrong row result adopted')
            result = value['updates'][0]['content']
            require(result == 'Batch aggregate: 98 results, 2 failed. Needs input: confirm region', 'aggregate counts or blocker deduplication')
            replies = [m for m in value['conversation'] if m['role'] == 'assistant']
            require(replies[-1]['content'] == [{'type': 'text', 'text': 'Main follow-up: ' + result}], 'aggregate did not reach the user through Main')
            root_id = value['trees'][0]['root_run_id']
            require(sum(g['run_id'] == root_id for g in value['generations']) == 5, 'Child completions caused model storm')
            require(value['tree_usage'][0]['model_calls'] == 105 and value['task_usage'][0]['model_calls'] == 105, 'Root tree/Task usage omitted calls')
            require(value['tree_usage'][0]['missing_token_usage'] == 105 and value['tree_usage'][0]['input_tokens'] is None, 'missing provider usage became zero-cost claim')
            require(value['account_usage'][0]['model_calls'] == len(value['usage']), 'account ledger omitted Main or Worker calls')
            measured = concurrency(value)
            require(not measured['remaining_worker_leases'], 'terminal batch kept leases')
            h.command('chat', 'work')
            require(state(h) == value, 'duplicate drain mutated finished batch')
            return {'state': value, 'observed': measured, 'transcript': transcript}

        report['batch'] = scenario('batch', batch)
        report['checks'].append('PTY: exactly one Task/Root/100 Child, 98 results + 2 failures, 100 unique row calls, one aggregate blocker and follow-up, five Root model calls, aggregate /status and duplicate-drain stability')

        def restart(h):
            h.send('batch', 'Delegate: Batch 100')
            work = h.hold('after_child_accept_commit')
            work.kill()
            work.communicate(timeout=10)
            before = state(h)
            require(len(before['child_links']) == 25, 'kill did not reach atomic batch admission')
            h.command('chat', 'work')
            after = state(h)
            check(after, children=100)
            require({i['invocation_id']: i['child_run_id'] for i in before['child_links']}.items() <= {i['invocation_id']: i['child_run_id'] for i in after['child_links']}.items(), 'restart replaced admitted Children')
            completed = {g['generation_id']: g for g in before['generations'] if g['status'] == 'completed'}
            require(all(g['attempt_count'] == completed[g['generation_id']]['attempt_count'] for g in after['generations'] if g['generation_id'] in completed), 'restart regenerated completed model result')
            require(after['updates'][0]['content'] == 'Batch aggregate: 98 results, 2 failed. Needs input: confirm region', 'restart lost aggregate')
            return {'before_kill': before, 'after_restart': after, 'boundary': 'after_child_accept_commit', 'signal': 'SIGKILL'}

        report['restart'] = scenario('restart', restart)
        report['checks'].append('SIGKILL after 25 atomic Child admissions: restart reuses invocation/Child identities and completed generations; exactly 100 total Children and one aggregate')

        def fair_cancel(h, pending_followup=False):
            h.send('batch', 'Delegate: Batch hold')
            if pending_followup:
                release = h.root / 'release-followup'
                generation = h.root / 'release-generation'
                generation.touch()
                observed = dict(started=time.monotonic())
                def progress():
                    value = state(h)
                    h.scheduling_diagnostic_state = value
                    if 'held' not in observed:
                        if sum(c['token'] is not None and any(r['run_id'] == c['run_id'] and r['depth'] == 2 for r in value['runs']) for c in value['claims']) == 16:
                            observed.update(held=value, second=time.monotonic())
                            h.send('second', 'Delegate: Child tree')
                    elif 'progressed' not in observed:
                        if len(value['tasks']) == 2 and any(t['status'] == 'completed' for t in value['tasks']):
                            observed['progressed'] = value
                    phase = observed.get('cancel', observed.get('second', observed['started']))
                    require(time.monotonic() - phase < 30, 'controlled fair/cancel phase exceeded original thirty-second bound')
                def boundary(name):
                    if name == 'after_followup_commit':
                        progress()
                        require('progressed' in observed, 'follow-up preceded independent Task completion')
                        generation.unlink()
                        observed['frozen_followup'] = True
                        release.touch()
                    elif name == 'before_generation_dispatch' and observed.get('frozen_followup') and 'cancel' not in observed:
                        value = state(h)
                        mains = {r['run_id'] for r in value['runs'] if r['kind'] == 'main'}
                        frozen = [g for g in value['generations'] if g['run_id'] in mains and g['status'] == 'pending'
                                  and any(m.get('origin') == 'runtime' for m in json.loads(g['request_json'])['messages'])]
                        require(len(frozen) == 1, 'controlled boundary did not freeze the real Main follow-up generation')
                        receipt = h.send('cancel', 'Cancel: Batch hold')
                        require(receipt['run_id'] == frozen[0]['run_id'], 'cancel did not append to the frozen follow-up Main')
                        observed.update(cancel=time.monotonic(), frozen_generation=frozen[0])
                        generation.touch()
                work = h.drive_boundaries({'after_followup_commit': release, 'before_generation_dispatch': generation},
                                          boundary, progress, timeout=90)
                require('cancel' in observed, 'controlled cancellation was never submitted')
                held, progressed = observed['held'], observed['progressed']
                stdout, stderr = work.stdout, work.stderr
            else:
                start = h.processes.start if h.processes is not None else subprocess.Popen
                work = start(h.args('chat', 'work'), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    text=True, env=h.env, cwd=h.root)
                h.children.append(work)
                held = h.wait_state(lambda v: sum(c['token'] is not None and any(r['run_id'] == c['run_id'] and r['depth'] == 2 for r in v['runs']) for c in v['claims']) == 16,
                                '16 running grandchildren', reader=state, timeout=30)
                # New independent Task progresses while the batch occupies its full
                # Child allowance. Main always has its reserved execution lane.
                h.send('second', 'Delegate: Child tree')
                progressed = h.wait_state(lambda v: len(v['tasks']) == 2 and any(t['status'] == 'completed' for t in v['tasks']),
                                     'fair second Task completion', reader=state, timeout=30)
                h.send('cancel', 'Cancel: Batch hold')
                try:
                    stdout, stderr = work.communicate(timeout=30)
                except subprocess.TimeoutExpired:
                    print(json.dumps({'scheduling_failure': h.failure_state()}), file=sys.stderr)
                    raise
            require(any(r['depth'] == 2 for r in held['runs']), 'recursive delegation missing')
            require(any(g['status'] == 'pending' for g in progressed['generations']), 'first Task did not remain active during second Task progress')
            require(work.returncode == 0, 'fair/cancel drain: ' + stderr)
            after = state(h)
            cancelled = next(t for t in after['tasks'] if t['status'] == 'cancelled')
            tree = [r for r in after['runs'] if r['task_id'] == cancelled['task_id']]
            require(tree and all(r['status'] in ('cancelled', 'completed') for r in tree) and all(r['status'] == 'cancelled' for r in tree if r['depth'] in (0, 2)), 'recursive stop left live descendants')
            require(all(i['status'] not in ('queued', 'running', 'waiting') for i in after['tools'] if any(r['run_id'] == i['run_id'] for r in tree)), 'recursive stop left live invocations')
            require(all(c['token'] is None for c in after['claims']), 'recursive cancellation retained leases')
            require(not any(u['task_id'] == cancelled['task_id'] for u in after['updates']), 'cancelled tree delivered late result')
            measured = concurrency(after)
            require(measured['peak_children_per_tree'] == 16 and not measured['remaining_worker_leases'], 'saturation/cleanup evidence missing')
            h.command('chat', 'work')
            require(state(h) == after, 'cancelled tree revived')
            return {'saturated': held, 'second_task_progress': progressed, 'cancelled': after, 'observed': measured, 'stdout': stdout,
                    **({'frozen_followup_generation': observed['frozen_generation']} if pending_followup else {})}

        report['fair_cancel'] = scenario('fair-cancel', fair_cancel)
        report['checks'].append('16 live grandchildren + queued work: independent second Task completes; Main cancels first tree; all descendants and invocations settle, no late follow-up, restart cannot revive old epoch')
        report['fair_cancel_followup'] = scenario('fair-cancel-followup', lambda h: fair_cancel(h, pending_followup=True))
        report['checks'].append('Controlled follow-up/user-input overlap: Main consumes the appended cancellation with the independent completion notice; same saturation, thirty-second drain and recursive cleanup assertions')
    report['cleanup'] = 'all owned PTYs/processes joined; disposable data roots removed'
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    args.evidence.unlink(missing_ok=True)
    binary = args.binary.resolve()
    result = run(binary, args.evidence)
    result['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'checks': result['checks'], 'evidence': str(args.evidence), 'cleanup': result['cleanup']}))


if __name__ == '__main__':
    main()
