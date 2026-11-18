#!/usr/bin/env python3
"""Actual stock owner CLI/default40 and App; no model or user credentials.

Optional archived29 also proves actual Main/Root/Child cold recovery after
committed-Child SIGKILL, inherited Grandchild versions and original file
read/publication. The existing scripted model provides protocol evidence only.
"""
import argparse
from owner_inventory import check_default_owner
import base64
from contextlib import closing
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import tempfile

from fixture_pool import current, run_cases, run as owned_run
from verify_main import require
from verify_tool_discovery_plugins import (owner_state, activate_owner, ArchivedTreeHarness,
    config as tree_config, tree_witness)

ROOT = Path(__file__).resolve().parents[2]
APP_TESTS = ('TestArtifactDefaultProductionOwnerWithoutModelAndColdLifecycle',
    'TestArtifactDefaultProductionHTTPChannelsFreezeCreationTypeAcrossVersions')


class OwnerCLI:
    def __init__(self, binary, root):
        self.binary, self.root = binary, root
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('STEWARD_')}
        self.env['STEWARD_DATA_ROOT'] = str(root / 'data')
        self.scope = current()

    def raw(self, *args, data=None, ok=True):
        result = self.scope.run([str(self.binary), *args], cwd=self.root, env=self.env,
                                input=data, capture_output=True, timeout=90)
        require(not ok or result.returncode == 0, f'{args}: {result.stderr!r}')
        return result

    def command(self, *args, data=None, text=None):
        if text is not None:
            require(data is None, 'duplicate CLI input')
            data = text.encode()
        return json.loads(self.raw(*args, data=data).stdout)

    def rows(self, sql, args=()):
        with closing(sqlite3.connect(self.root / 'data/state.db')) as db, db:
            return db.execute(sql, args).fetchall()


def owner_default(binary, explicit=False):
    with tempfile.TemporaryDirectory(prefix='steward-artifact-owner-cli-') as tmp:
        root = Path(tmp)
        if explicit:
            (root / 'data').mkdir()
            (root / 'data/config.yaml').write_text(json.dumps(dict(version=1,
                artifact=dict(retention_days=7, cleanup_batch=1))))
        cli = OwnerCLI(binary, root)
        payload = 'original owner λ\nbytes'.encode()
        file = cli.command('artifact', 'save', 'owner.txt', 'text/plain', data=payload)
        ref = file['artifact_ref']
        require(cli.raw('artifact', 'open', ref).stdout == payload, 'original bytes changed')
        broken = root / 'broken-execution.yaml'
        broken.write_text(json.dumps(dict(version=1, javascript=dict(command='/missing/node', script='/missing/sidecar.mjs'))))
        page = cli.command('-config', str(broken), 'evidence', 'read', data=json.dumps(dict(ref=ref, limit=4096)).encode())
        require(page['text'].encode() == payload and page['ref'] == ref, 'managed original evidence needed execution/model or changed bytes')
        graph = cli.rows('SELECT active_graph FROM rsi_control WHERE singleton=1')[0][0]
        bindings = json.loads(cli.rows('SELECT graph_json FROM rsi_graph WHERE revision=?', (graph,))[0][0])['bindings']
        policy = json.loads(cli.rows('SELECT p.policy_json FROM rsi_policy p JOIN rsi_candidate c USING(policy_id) WHERE c.target=?', (graph,))[0][0])
        owner_cases = check_default_owner({'bindings': bindings}, policy)
        require(cli.rows('SELECT count(*) FROM model_profile')[0][0] == 0 and
                cli.rows('SELECT count(*) FROM run')[0][0] == 0,
                'owner operation initialized model/runtime work')
        days = 7 if explicit else 30
        require(cli.rows('SELECT semantic_kind,method_graph,expires_at-created_at FROM artifact WHERE artifact_ref=?', (ref,)) ==
                [('file', graph, days * 86400000000)], 'actual accepted creation facts missing')
        require('semantic_kind' not in file and 'method_graph' not in file,
                'private metadata leaked into descriptor protocol')
        png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jZ3sAAAAASUVORK5CYII=')
        picture = cli.command('artifact', 'save', 'pixel.png', 'image/png', data=png)
        require(cli.rows('SELECT semantic_kind FROM artifact WHERE artifact_ref=?', (picture['artifact_ref'],)) == [('image',)],
                'stock creation did not use accepted media policy')
        dry = cli.command('artifact', 'cleanup')
        require(dry['dry_run'] and not dry['artifacts'], 'live files became cleanup candidates')
        selected = cli.command('plugin', 'status')
        template = owner_state(cli, selected)
        cli.command('plugin', 'deactivate', data=json.dumps(dict(operation_id='owner-disable-artifact', expected_graph_revision=selected['graph_revision'])).encode())
        failed = cli.raw('artifact', 'save', 'blocked.txt', 'text/plain', data=b'blocked', ok=False)
        require(failed.returncode != 0, 'cold disabled owner restored native creation')
        require(cli.raw('artifact', 'cleanup', 'apply', ok=False).returncode != 0,
                'disabled cleanup restored native selection')
        require(cli.raw('artifact', 'open', ref).stdout == payload,
                'independent raw read required a current plugin')
        cli.command('artifact', 'delete', ref, str(file['version']))
        require(cli.rows('SELECT count(*) FROM artifact WHERE name=?', ('blocked.txt',))[0][0] == 0,
                'rejected owner call created partial metadata')
        return dict(stock_owner_no_model=True, full40=True, independent_cases=owner_cases,
                    explicit_retention=explicit, immutable_bytes=True,
                    disabled_no_native=True, independent_read_delete=True), template


def unbundled(binary):
    with tempfile.TemporaryDirectory(prefix='steward-artifact-unbundled-') as tmp:
        root = Path(tmp)
        executable = root / 'unbundled/steward'
        executable.parent.mkdir()
        shutil.copy2(binary, executable)
        cli = OwnerCLI(executable, root)
        result = cli.raw('artifact', 'save', 'blocked.txt', 'text/plain', data=b'new bytes', ok=False)
        require(result.returncode != 0 and cli.rows('SELECT count(*) FROM artifact')[0][0] == 0,
                'fresh unbundled production admitted native output')
        return dict(fresh_unbundled_rejected=True, copied_binary_sha256=hashlib.sha256(executable.read_bytes()).hexdigest())



def install_owner_bundle(cli, binary, graph):
    # Preserve the resource choices captured in the accepted owner graph,
    # including an unavailable parser. Recapture would be a different change
    # and can require different independent expected cases.
    target = json.loads(json.dumps(graph))
    for binding in target['bindings']:
        installed = cli.command('plugin', 'install', str(binary.parent / 'plugins' / binding['instance_id']))
        require(installed['digest'] == binding['package_digest'], 'owner upgrade substituted package bytes')
    return target


def archived_owner(binary, prior, template):
    if prior is None:
        return 'not_run; requires actual original29 binary for full local acceptance', None
    with tempfile.TemporaryDirectory(prefix='steward-artifact-actual29-') as tmp:
        root = Path(tmp)
        old = OwnerCLI(prior, root)
        payload = b'actual original29 bytes\x00\xce\xbb'
        original = old.command('artifact', 'save', 'original.bin', 'application/octet-stream', data=payload)
        require(old.rows('PRAGMA user_version')[0][0] == 68, 'archive is not the original schema68 carrier')
        selected = old.command('plugin', 'status')
        old_graph, old_policy = owner_state(old, selected)
        new = OwnerCLI(binary, root)
        require(new.command('artifact', 'describe', original['artifact_ref']) == original,
                'old descriptor changed during forward migration')
        require(new.raw('artifact', 'open', original['artifact_ref']).stdout == payload,
                'actual prior29 bytes/reference changed')
        require(new.rows('SELECT method_graph,semantic_kind FROM artifact WHERE artifact_ref=?', (original['artifact_ref'],)) == [(None, None)],
                'migration invented a new method for old artifact')
        require(new.command('plugin', 'status') == selected, 'new Host automatically replaced old owner selection')
        legacy = new.command('artifact', 'save', 'prior-owner.txt', 'text/plain', data=b'original owner boundary')
        require(new.rows('SELECT method_graph,semantic_kind FROM artifact WHERE artifact_ref=?', (legacy['artifact_ref'],)) == [(None, None)],
                'retained old owner choice was silently widened')
        graph, policy = template
        target = install_owner_bundle(new, binary, graph)
        upgraded = activate_owner(new, target, policy, selected['graph_revision'], 'artifact-owner-full40')
        created = new.command('artifact', 'save', 'new.txt', 'text/plain', data=b'new managed owner output')
        require(new.rows('SELECT method_graph IS NOT NULL FROM artifact WHERE artifact_ref=?', (created['artifact_ref'],)) == [(1,)],
                'current owner creation stayed in native after default bootstrap')
        require(new.command('artifact', 'describe', original['artifact_ref']) == original,
                'new method rewrote prior reference')
        activate_owner(new, old_graph, old_policy, upgraded['graph_revision'], 'artifact-owner-rollback29')
        require(new.raw('artifact', 'save', 'rollback-blocked.txt', 'text/plain', data=b'blocked', ok=False).returncode != 0,
                'rollback29 revived the permanently closed native owner boundary')
        require(new.raw('artifact', 'open', original['artifact_ref']).stdout == payload and
                new.raw('artifact', 'open', created['artifact_ref']).stdout == b'new managed owner output',
                'rollback lost original or current raw references')
        return dict(binary=str(prior), sha256=hashlib.sha256(prior.read_bytes()).hexdigest(),
                    scope='actual original29 owner bytes/ref/descriptor/NULL semantics;not old Run/Child proof',
                    original_ref=original['artifact_ref'], exact=True, explicit_full40_upgrade=True, rollback_no_native=True), (old_graph, old_policy)



def artifact_tree_witness(h):
    result = tree_witness(h)
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db:
        db.row_factory = sqlite3.Row
        db.execute('BEGIN')
        result['runs'] = {r['run_id']: dict(r) for r in db.execute(
            'SELECT r.run_id,r.kind,r.worker_position,r.parent_run_id,r.root_run_id,r.task_id,r.depth,'
            'r.capability_snapshot_id,r.model_snapshot_id,r.history_boundary,r.created_at,'
            'b.graph_revision,b.managed_services_json FROM run r JOIN rsi_run b USING(run_id)')}
        result['completed_invocations'] = {r['invocation_id']: dict(r) for r in db.execute(
            "SELECT invocation_id,run_id,tool_name,arguments_json,result_json,attempt_count FROM tool_invocation WHERE status='succeeded'")}
        result['artifacts'] = {r['artifact_ref']: dict(r) for r in db.execute(
            'SELECT artifact_ref,owner_user_id,owner_agent_id,task_id,run_id,name,media_type,byte_size,'
            'content_hash,storage_key,visibility,created_at,expires_at,version FROM artifact WHERE deleted_at IS NULL')}
    return result


def archived_execution(binary, prior, template, archived, artifact_output):
    # Existing scripted children model only: real archived Host/packages, accepted
    # invocations, Artifact bytes, SIGKILL, forward migration and cold recovery.
    with tempfile.TemporaryDirectory(prefix='steward-artifact-original29-tree-') as tmp:
        root = Path(tmp).resolve()
        tree_config(root)
        h = ArchivedTreeHarness(prior, root, fixture='children')
        h.env.pop('COMPOSIO_API_KEY', None)
        try:
            h.setup()
            old_graph, old_policy = archived
            original = activate_owner(h, install_owner_bundle(h, prior, old_graph), old_policy,
                '', 'artifact29-tree-initial')
            require(len(old_graph['bindings']) == 29, 'tree did not start with actual original29 composition')
            marker = 'Child artifact' if artifact_output else 'Child tree'
            h.send('artifact29-original-tree', 'Delegate: ' + marker)
            held = h.hold('after_child_accept_commit', timeout=30)
            held.kill()
            held.wait(timeout=10)
            require(held.returncode == -9, 'archived execution did not stop at committed Child')
            current().retire(held)
            held.communicate(timeout=10)
            before = artifact_tree_witness(h)
            require(len(before['workers']) == 2 and len(before['runs']) >= 3 and
                    any(r['kind'] == 'main' for r in before['runs'].values()) and before['generations'],
                    'original tree lacks actual Main/Root/Child or completed generation evidence')
            require(bool(before['artifacts']) == artifact_output,
                    'archived artifact scenario did not create its original native output')
            descriptors = {ref: h.command('artifact', 'describe', ref) for ref in before['artifacts']}
            h.binary = binary
            require(h.command('plugin', 'status') == original and artifact_tree_witness(h) == before,
                    'forward migration rewrote original execution or artifact facts')
            graph, policy = template
            selected = activate_owner(h, install_owner_bundle(h, binary, graph), policy,
                original['graph_revision'], 'artifact29-tree-upgrade30')
            h.command('chat', 'work')
            after = artifact_tree_witness(h)
            for key in ('workers', 'runs', 'generations', 'completed_invocations', 'artifacts'):
                require(all(after[key].get(identity) == value for identity, value in before[key].items()),
                        'cold recovery changed original ' + key)
            workers = list(after['workers'].values())
            require(len(workers) == (2 if artifact_output else 3) and
                    all(r['graph_revision'] == original['graph_revision'] and
                        'steward.artifact-recipe/1' not in json.loads(r['managed_services_json']) for r in workers),
                    'old or newly admitted descendant acquired a current Artifact method')
            for child in workers:
                if child['parent_run_id'] is None:
                    continue
                parent = after['workers'].get(child['parent_run_id'])
                require(parent is not None and child['depth'] == parent['depth'] + 1 and
                        all(child[key] == parent[key] for key in ('task_id', 'task_epoch', 'model_snapshot_id',
                            'tools_json', 'data_scope', 'policy_json', 'graph_revision', 'managed_services_json')),
                        'new descendant changed original inherited capability/model/context policy')
            state = Path(h.env['STEWARD_DATA_ROOT']) / 'state.db'
            with closing(sqlite3.connect(state)) as db:
                db.execute('BEGIN')
                require(db.execute("SELECT count(*) FROM run WHERE kind='worker' AND status!='completed'").fetchone()[0] == 0,
                        'actual old subtree did not complete')
                for row in workers:
                    require(db.execute('SELECT count(*) FROM run_artifact_request WHERE run_id=?', (row['run_id'],)).fetchone()[0] == 0 and
                            db.execute('SELECT count(*) FROM run_artifact_recipe WHERE run_id=?', (row['run_id'],)).fetchone()[0] == 0,
                            'new admission backfilled a current recipe into old ancestry')
                for ref, descriptor in descriptors.items():
                    current_descriptor = h.command('artifact', 'describe', ref)
                    require({k: v for k, v in current_descriptor.items() if k != 'expires_at'} ==
                            {k: v for k, v in descriptor.items() if k != 'expires_at'} and
                            db.execute('SELECT method_graph,semantic_kind FROM artifact WHERE artifact_ref=?', (ref,)).fetchone() == (None, None),
                            'original published file was reinterpreted: ' + json.dumps(dict(before=descriptor, after=current_descriptor)))
                    # A one-shot Task may finish during recovery. Its public
                    # expiration then starts the ORIGINAL TTL after that end;
                    # the immutable stored creation/expiry facts stay exact.
                    source = before['artifacts'][ref]
                    tasks = list(db.execute('SELECT status,coalesce(completed_at,cancelled_at,created_at) FROM task t WHERE task_id=? OR EXISTS(SELECT 1 FROM artifact_reference r WHERE r.artifact_ref=? AND r.holder_kind=\'task\' AND r.holder_id=t.task_id)', (source['task_id'], ref)))
                    if any(status in ('active', 'paused') for status, _ in tasks):
                        require(current_descriptor.get('expires_at') is None, 'active Task lost original Artifact protection')
                    else:
                        ended = max((end for _, end in tasks), default=0)
                        expected = max(source['expires_at'], ended + source['expires_at'] - source['created_at'])
                        actual = datetime.fromisoformat(current_descriptor['expires_at'].replace('Z', '+00:00')) - datetime(1970, 1, 1, tzinfo=timezone.utc)
                        micros = (actual.days * 86400 + actual.seconds) * 1000000 + actual.microseconds
                        require(micros == expected, 'completed Task changed original Artifact retention')
                    # The original children fixture saved these exact bytes before
                    # accepting the Child. Read them through the independent CLI.
                    payload = h.processes.run(h.args('artifact', 'open', ref), env=h.env, cwd=h.root,
                        capture_output=True, timeout=30)
                    require(payload.returncode == 0 and payload.stdout == b'explicit artifact input',
                            'original archived Artifact bytes changed')
                if artifact_output:
                    require(db.execute("SELECT count(*) FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name='builtin__artifact_manage' AND i.status='succeeded'").fetchone()[0] >= 2,
                            'old Child did not actually read and publish its original file')
                else:
                    require(any(r['depth'] == 2 and r['run_id'] not in before['workers'] for r in workers),
                            'upgrade did not admit an actual Grandchild under old inheritance')
            return dict(actual_archive29=True, original_graph=original['graph_revision'], current_graph=selected['graph_revision'],
                original_main_root_child=len(before['runs']), original_completed_generations=len(before['generations']),
                original_invocations_retained=len(before['completed_invocations']), original_artifacts_retained=len(before['artifacts']),
                workers_after_recovery=len(workers), new_old_version_grandchild=not artifact_output,
                archived_child_actual_read_publish=artifact_output, boundary='after_child_accept_commit', crash_exit=-9,
                no_old_recipe_backfill=True, cleanup=True,
                scope='actual archived29 Main/Root/Child snapshots,completed generations/invocations and original refs;existing scripted children model;no model quality claim')
        finally:
            h.cleanup()


def actual_app(binary, evidence, app_test=None, test2json=None):
    env = dict(os.environ, STEWARD_TEST_APP_DEFAULT_BUNDLE=str(binary))
    pattern = '^('+'|'.join(APP_TESTS)+')$'
    require((app_test is None) == (test2json is None), 'precompiled App and test2json must be supplied together')
    command = (['go', 'test', './internal/app', '-run', pattern, '-count=1', '-json'] if app_test is None else
        [str(test2json), '-t', '-p', 'internal/app', str(app_test), '-test.run='+pattern, '-test.v=test2json', '-test.timeout=6m'])
    result = owned_run(command, cwd=ROOT if app_test is None else ROOT / 'internal/app',
                       env=env, capture_output=True, text=True, timeout=369)
    log = evidence.with_suffix('.app.jsonl')
    log.write_text(result.stdout + result.stderr)
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    terminal = {name: [e['Action'] for e in events if e.get('Test') == name and e['Action'] in ('pass', 'fail', 'skip')] for name in APP_TESTS}
    require(result.returncode == 0 and all(actions == ['pass'] for actions in terminal.values()), 'actual Artifact App failed/skipped: '+result.stdout[-8000:])
    return dict(actual_app_no_model_cold_lifecycle=True, actual_http_media_versions_and_auth=True, terminal=terminal, log=str(log))


def run(binary, evidence, prior=None, app_test=None, test2json=None):
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    default, template = owner_default(binary)
    explicit, _ = owner_default(binary, True)
    archived_report, old_template = archived_owner(binary, prior, template)
    report = dict(checks=[default, explicit, unbundled(binary)],
                  actual_prior29=archived_report, actual_prior29_executions='not_run;requires actual original29 binary', cleanup=False)
    if prior is not None:
        report['actual_prior29_executions'] = [archived_execution(binary, prior, template, old_template, output) for output in (False, True)]
    report['checks'].append(actual_app(binary, evidence, app_test, test2json))
    require(hashlib.sha256(binary.read_bytes()).hexdigest() == digest, 'Artifact gate changed fixed Host')
    report.update(binary_sha256=digest, cleanup=True,
                  scope='stock CLI/default40 owner creation+semantic facts+independent controls and actual App;owning method/effect/privacy races separate')
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--prior29-binary', type=Path)
    parser.add_argument('--app-test-binary', type=Path)
    parser.add_argument('--test2json-binary', type=Path)
    args = parser.parse_args()
    if bool(args.app_test_binary) != bool(args.test2json_binary):
        parser.error('supply --app-test-binary and --test2json-binary together')
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    report = run_cases([None], lambda _: run(args.binary.resolve(), args.evidence,
        args.prior29_binary.resolve() if args.prior29_binary else None,
        args.app_test_binary.resolve() if args.app_test_binary else None,
        args.test2json_binary.resolve() if args.test2json_binary else None), jobs=1)[0]
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps(dict(checks=len(report['checks']), cleanup=report['cleanup'], actual_prior29=bool(args.prior29_binary))))
