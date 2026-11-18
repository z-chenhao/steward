#!/usr/bin/env python3
"""M3 stdio MCP, progressive Skills and actual controlled coding CLI proof."""
import argparse
from contextlib import closing
from datetime import datetime
import hashlib
import json
import os
import shutil
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path

from verify_main import MainHarness, require
from fixture_pool import run as fixture_run, run_cases


class CapabilityHarness(MainHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'),
                '-test-bridge', 'capabilities', *args]

    def command(self, *args, text=None, ok=True, timeout=320):
        run = self.processes.run if self.processes is not None else subprocess.run
        result = run(self.args(*args), input=text, text=True, capture_output=True,
                                env=self.env, cwd=self.root, timeout=timeout)
        if os.environ.get('STEWARD_CAPABILITY_DIAGNOSTICS') and (result.returncode or result.stderr):
            self.capture_diagnostic(args, result)
        require(not ok or result.returncode == 0, f'{args}: {result.stderr}')
        return json.loads(result.stdout) if ok else result

    def capture_diagnostic(self, args, result):
        # Opt-in disposable Fake fixtures only. Never copy credential stores,
        # configuration, stdin, or an arbitrary data-root directory.
        folder = Path(os.environ['STEWARD_CAPABILITY_DIAGNOSTICS']) / uuid.uuid4().hex
        folder.mkdir(parents=True)
        if not hasattr(self, 'diagnostic_binary_sha256'):
            self.diagnostic_binary_sha256 = digest(self.binary)
        record = dict(command=list(args), returncode=result.returncode,
                      stderr=result.stderr, binary_sha256=self.diagnostic_binary_sha256, databases={})
        if result.returncode:
            for name in ('state.db', 'conversation.db'):
                source = self.root / 'data' / name
                if source.is_file():
                    try:
                        deadline = time.monotonic() + 5
                        def progress(*_):
                            if time.monotonic() > deadline:
                                raise TimeoutError('fixture backup deadline')
                        with closing(sqlite3.connect(source.as_uri() + '?mode=ro', uri=True)) as db, \
                             closing(sqlite3.connect(folder / name)) as backup:
                            db.backup(backup, pages=128, progress=progress)
                        record['databases'][name] = digest(folder / name)
                    except (sqlite3.Error, TimeoutError) as error:
                        record['databases'][name] = {'capture_error': str(error)}
        (folder / 'command.json').write_text(json.dumps(record, indent=2) + '\n')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree(root):
    return {str(p.relative_to(root)): digest(p) for p in sorted(root.rglob('*')) if p.is_file()}


def prepare(binary, root, mode='edit', metadata=None):
    repo = root / 'repo'
    shutil.copytree(Path(__file__).resolve().parents[2] / 'tests/fixtures/coding-v1', repo)
    # The production sandbox rejects symlinked roots. macOS tempfile paths
    # commonly use /var -> /private/var; configure the actual canonical root.
    repo = repo.resolve()
    for args in [('init', '-q', '-b', 'feature/eval'), ('add', '.'),
                 ('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.test',
                  'commit', '-qm', 'fixture')]:
        fixture_run(['git', '-c', 'maintenance.auto=false', '-c', 'gc.auto=0', *args], cwd=repo, check=True, capture_output=True,
                       env={'PATH': '/usr/bin:/bin', 'HOME': '/nonexistent',
                            'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null'})
    repo.chmod(0o700)
    (repo / 'docs/notes.md').write_text('USER_UNCOMMITTED_SENTINEL\n')
    hook = repo / '.git/hooks/pre-commit'
    hook.write_text('#!/bin/sh\necho must-not-run > hook-ran\nexit 1\n')
    hook.chmod(0o700)
    skills = root / 'skills/review'
    skills.mkdir(parents=True)
    (skills / 'SKILL.md').write_text('---\nname: review\ndescription: Inspect code and preserve user files.\n---\nPRIVATE_BODY_MARKER: Read AGENTS.md and cite symbols.\n')
    (skills / 'assets').mkdir()
    (skills / 'assets/opaque.bin').write_bytes(b'\x00\xffSKILL_ASSET')
    (root / 'provider').mkdir()
    shutil.copyfile(Path(__file__).resolve().parents[2] / 'tests/fixtures/m3-capabilities-v1.json',
                    root / 'provider/mcp-data.json')
    toolchains = [str(Path(shutil.which('go')).resolve().parent.parent)]
    if sys.platform == 'darwin':
        # Select the trusted developer Git directly. /usr/bin/git is a shim
        # that can launch Xcode's configuration UI outside the sandbox scope.
        git = Path(fixture_run(['/usr/bin/xcrun', '--no-cache', '--find', 'git'],
                               text=True, capture_output=True, check=True).stdout.strip()).resolve()
        require(git.is_file() and any(git.is_relative_to(Path(p)) for p in
                ('/Library/Developer', '/Applications/Xcode.app/Contents/Developer')),
                'doctor: developer Git is outside the trusted toolchain scope')
        toolchains.append(str(git.parent.parent))
    config = ('version: 1\nmcp:\n  - name: fixture\n    command: ' + json.dumps(str(binary)) +
              '\n    directory: ' + json.dumps(str(root / 'provider')) +
              '\n    args: ["-test-mcp-server", ' + json.dumps(str(root / 'provider')) + ']\n' +
              '    tools: {read: read_only, send: external_effect}\n' +
              'skills:\n  root: ' + json.dumps(str(root / 'skills')) + '\n  scope: workspace\n' +
              'execution:\n  root: ' + json.dumps(str(repo)) + '\n  toolchain_roots: [' +
              ', '.join(json.dumps(p) for p in toolchains) + ']\n')
    from codemode_peer import runtime_yaml
    config += runtime_yaml()
    (root / 'config.yaml').write_text(config)
    h = CapabilityHarness(binary, root, fixture='capabilities')
    if os.environ.get('STEWARD_CAPABILITY_DIAGNOSTICS'):
        h.env['STEWARD_SQL_FIXTURE_DIAGNOSTIC'] = '1'
    h.setup(metadata=metadata)
    return h


def call(h, key, name, arguments, worker=False, approve=True, recall_barrier=False):
    plan = json.dumps([{'name': name, 'arguments': arguments}])
    receipt = h.send(key, ('Delegate capabilities: ' if worker else 'Capabilities: ') + plan)
    if recall_barrier:
        from verify_recall import drain_with_publication_barrier
        drain_with_publication_barrier(h, receipt['run_id'], prefix=key + '-')
    else:
        h.command('chat', 'work')
    pending = [a for a in h.command('approval', 'list') if a['status'] == 'pending']
    if pending:
        require(len(pending) == 1, 'unexpected pending approvals')
        a = pending[0]
        before = h.state()
        target = [t for t in before['tools'] if t['status'] == 'waiting' and t['tool_name'] == a['tool'] and t['arguments_json'] == a['arguments']]
        require(len(target) == 1 and target[0]['attempt_count'] == 0, 'dispatch before exact approval')
        h.command('approval', 'approve' if approve else 'deny', a['approval_id'], a['frozen_arguments_hash'])
        h.command('chat', 'work')
    return h.state()['tools'][-1]


def content(invocation):
    return json.loads(invocation['result_json'])



def select_managed_environment(h):
    repo = Path(__file__).resolve().parents[2]
    bindings = []
    policy = dict(id='environment-system-owner', allowed_contracts=[], allowed_requests=[], cases=[])
    for name in ('file-tools', 'shell-tools', 'coding-workspace'):
        installed = h.command('plugin', 'install', str(h.binary.parent / 'plugins' / name))
        bindings.append(dict(instance_id=name, package_digest=installed['digest'], config={}, state_namespace=name, state_version=1))
        contribution = json.loads((repo / ('tests/fixtures/plugins/' + name + '-policy.json')).read_text())
        policy['allowed_contracts'] += contribution['allowed_contracts']
        policy['allowed_requests'] += contribution.get('allowed_requests', [])
        policy['cases'] += contribution['cases']
    graph = h.command('plugin', 'prepare', text=json.dumps(dict(protocol='steward-rsi-plugins/0.1', bindings=bindings)))
    h.command('plugin', 'policy', text=json.dumps(policy))
    h.command('plugin', 'propose', text=json.dumps(dict(candidate_id='file-system', baseline='', target=graph['revision'], policy_id=policy['id'], goal='Independent file, shell and coding methods under original workspace and concrete approval')))
    decision = h.command('plugin', 'verify', 'file-system')
    h.command('plugin', 'activate', text=json.dumps(dict(operation_id='file-select', expected_graph_revision='', target_graph_revision=graph['revision'], decision_ref=decision['decision_ref'])))
    return graph['revision']


def check_managed_environment(h, revision):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db:
        db.execute('BEGIN')
        rows = db.execute("SELECT i.invocation_id,b.graph_revision,b.managed_services_json FROM tool_invocation i JOIN rsi_run b USING(run_id) WHERE i.tool_name IN ('builtin__read','builtin__edit','builtin__write','builtin__bash') AND i.attempt_count>0").fetchall()
        require(all(r[1] == revision and {'steward.file-window/1','steward.file-edit-span/1','steward.shell-limits/1','steward.coding-import-limits/1','steward.coding-report-recipe/1'} <= set(json.loads(r[2])) for r in rows), 'environment call lost its frozen managed composition')
        missing = db.execute("SELECT count(*) FROM tool_invocation i LEFT JOIN file_method_plan p USING(invocation_id) WHERE i.tool_name IN ('builtin__edit','builtin__write') AND i.status='succeeded' AND (p.invocation_id IS NULL OR p.graph_revision!=?)", (revision,)).fetchone()[0]
        require(missing == 0, 'successful mutation lacked a prior accepted method')
        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='file-tools'").fetchone()[0] == 0, 'pure file method created second unknown effect ledger')
        missing_shell = db.execute("SELECT count(*) FROM tool_invocation i LEFT JOIN shell_method_plan p USING(invocation_id) WHERE i.tool_name='builtin__bash' AND i.status='succeeded' AND (p.invocation_id IS NULL OR p.graph_revision!=? OR p.timeout_seconds>coalesce(json_extract(i.arguments_json,'$.timeout'),120) OR p.output_bytes>48000)", (revision,)).fetchone()[0]
        require(missing_shell == 0, 'successful command lacked its bounded accepted resource recipe')
        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='shell-tools'").fetchone()[0] == 0, 'pure shell recipe created second unknown effect ledger')
        imports = db.execute('SELECT e.status,e.scope_hash,b.managed,b.scope_hash,b.graph_revision,b.driver_hash,b.limits_json FROM task_code_environment e LEFT JOIN code_import_policy_binding b USING(task_id)').fetchall()
        require(all(managed == 1 and scope == saved_scope and (status != 'ready' or graph == revision and driver.startswith('steward-coding-driver/1:') and json.loads(limits) == dict(entries=4096,file_bytes=8388608,tree_bytes=67108864)) for status,scope,managed,saved_scope,graph,driver,limits in imports), 'Task import lost its prior accepted complete-snapshot method')
        missing_reports = db.execute("SELECT count(*) FROM artifact a LEFT JOIN code_report_method_binding b USING(artifact_ref) WHERE a.name='code-review.json' AND (b.artifact_ref IS NULL OR b.graph_revision!=?)", (revision,)).fetchone()[0]
        require(missing_reports == 0, 'code report Artifact lost its atomically accepted recipe')
        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='coding-workspace'").fetchone()[0] == 0, 'pure coding method created second unknown effect ledger')
        return dict(graph=revision, invocations=len(rows), accepted_file_methods=db.execute('SELECT count(*) FROM file_method_plan').fetchone()[0], accepted_shell_methods=db.execute('SELECT count(*) FROM shell_method_plan').fetchone()[0], accepted_import_methods=sum(r[4] is not None for r in imports), accepted_report_methods=db.execute('SELECT count(*) FROM code_report_method_binding').fetchone()[0])

def select_managed_skills(h):
    repo = Path(__file__).resolve().parents[2]
    installed = h.command('plugin', 'install', str(h.binary.parent / 'plugins/skill-library'))
    graph = h.command('plugin', 'prepare', text=json.dumps(dict(protocol='steward-rsi-plugins/0.1', bindings=[dict(instance_id='skill-library', package_digest=installed['digest'], config={}, state_namespace='skill-library', state_version=1)])))
    policy = json.loads((repo / 'tests/fixtures/plugins/skill-library-policy.json').read_text())
    h.command('plugin', 'policy', text=json.dumps(policy))
    h.command('plugin', 'propose', text=json.dumps(dict(candidate_id='skills-system', baseline='', target=graph['revision'], policy_id=policy['id'], goal='Independent frozen method discovery and reads')))
    decision = h.command('plugin', 'verify', 'skills-system')
    h.command('plugin', 'activate', text=json.dumps(dict(operation_id='skills-select', expected_graph_revision='', target_graph_revision=graph['revision'], decision_ref=decision['decision_ref'])))
    return graph['revision']


def check_managed_skill_receipts(h, revision):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
        count, managed = db.execute("SELECT count(*),sum(b.graph_revision=? AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value='steward.skill-discovery/1')) FROM tool_invocation i JOIN rsi_run b USING(run_id) WHERE i.tool_name IN ('builtin__skill_list','builtin__skill_view') AND i.status='succeeded'", (revision,)).fetchone()
        pure, = db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='skill-library'").fetchone()
    require(count >= 2 and count == managed and pure == 0, 'Skill calls bypassed the frozen package or wrote redundant pure-read operations')
    return dict(graph=revision, successful_reads=count, pure_read=True)


def run(binary, selected=None, fixture_context=None, scenario_jobs=1):
    report = {'version': 1, 'scope': 'MCP/Skill and native file/command contracts; approvals, isolated persistent execution, CAS and unknown effects',
              'checks': [], 'crashes': [], 'cleanup': False, 'scenario_jobs': scenario_jobs}
    with tempfile.TemporaryDirectory(prefix='steward-capabilities-') as temp:
        root = Path(temp)
        cases = []
        def scenario(name, fn, mode='edit', kind='checks'):
            if not selected or selected == name or selected == 'quotes' and name.startswith('quotes-'):
                cases.append((name, fn, mode, kind))

        def drive(item):
            name, fn, mode, kind = item
            folder = root / name
            folder.mkdir()
            metadata = None
            if name in ('task-code-environments', 'task-code-recovery', 'task-code-exact-report', 'tool-loading', 'main-business', 'discovery'):
                # Keep coding calls/approvals and repeated publication recovery
                # receipts inside the profile's protected-context budget.
                metadata = dict(name='Capability workload', provider_kind='openai_compatible',
                                endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions',
                                model_id='fixture-model', context_window=16384, max_output_tokens=1024,
                                compatibility={'tool_result_format': 'openai'}, capabilities={'text_input': True})
            h = prepare(binary, folder, mode, metadata)
            try:
                if fixture_context:
                    h.send('fixture-context', fixture_context)
                revision = select_managed_environment(h) if fn.__name__ in ('coding','dirty_index','audit','crash','task_code_environments','task_code_recovery') else None
                result = fn(h)
                if revision:
                    result['managed_environment'] = check_managed_environment(h, revision)
                return kind, result
            finally:
                h.cleanup()

        def coding(h):
            repo = h.root / 'repo'
            original = tree(repo)
            call(h, 'rules', 'builtin__read', {'path': 'AGENTS.md'}, worker=True)
            before = call(h, 'red', 'builtin__bash', {'command': 'go test -race ./...', 'timeout': 120}, worker=True)
            observed = json.loads(content(before)['text'])
            require(observed['exit_code'] != 0 and 'flag provided but not defined' in observed['output'], 'fixture did not reproduce missing --status: ' + str(observed))
            path = 'cmd/taskctl/main.go'
            source = json.loads(content(call(h, 'source', 'builtin__read', {'path': path}, worker=True))['text'])
            replacement = source['text'].replace('status := "" // CLI_STATUS_FLAG',
                                                'status := ""\n\tflags.StringVar(&status, "status", "", "Filter Task status")')
            edited = call(h, 'edit', 'builtin__edit',
                          {'path': path, 'before_hash': source['hash'], 'old_text': 'status := "" // CLI_STATUS_FLAG', 'new_text': 'status := ""\n\tflags.StringVar(&status, "status", "", "Filter Task status")'}, worker=True)
            require(not content(edited).get('error') and (repo / path).read_text() == replacement, 'approved Worker edit failed')
            test_path = 'cmd/taskctl/main_test.go'
            source_test = json.loads(content(call(h, 'test-source', 'builtin__read', {'path': test_path}, worker=True))['text'])
            edited_test = call(h, 'test-edit', 'builtin__edit', {'path': test_path, 'before_hash': source_test['hash'],
                               'old_text': 'package main', 'new_text': 'package main\n// verification retains existing tests'}, worker=True)
            require(not content(edited_test).get('error'), 'multi-file edit failed')
            tested = call(h, 'green', 'builtin__bash', {'command': 'go test -race ./...', 'timeout': 120}, worker=True)
            passed = json.loads(content(tested)['text'])
            require(passed['exit_code'] == 0 and not passed['timed_out'], 'real sandbox tests failed: ' + str(passed))
            # Git is a regular approved command; explicit no-refresh/index scope
            # avoids treating index maintenance as a source-file modification.
            diff = json.loads(content(call(h, 'diff', 'builtin__bash', {'command': 'git --no-optional-locks diff --no-ext-diff'}, worker=True))['text'])
            require(diff['exit_code'] == 0 and 'flags.StringVar' in diff['output'], 'Git diff omitted edit: ' + str(diff))
            published = content(call(h, 'deliver', 'builtin__read', {'path': path, 'as_artifact': True}, worker=True))
            artifact = fixture_run(h.args('artifact', 'open', published['artifact_refs'][0]), capture_output=True, env=h.env, cwd=h.root, timeout=20)
            require(artifact.returncode == 0 and artifact.stdout == replacement.encode(), 'native read did not retain file as Artifact')
            final = tree(repo)
            changed = [p for p in original if original[p] != final.get(p)]
            require(sorted(changed) == sorted([path, test_path]), 'unrelated source changes: ' + str(changed))
            require(not (repo / 'hook-ran').exists(), 'reading diff executed hook')
            return {'name': 'real_red_edit_green_with_Worker_approval', 'changed': changed, 'before': observed, 'after': passed, 'state': h.state()}
        scenario('coding', coding)

        def dirty_index(h):
            repo = h.root / 'repo'
            path = 'docs/notes.md'
            source = json.loads(content(call(h, 'read-dirty', 'builtin__read', {'path': path}, worker=True))['text'])
            first = call(h, 'first-edit', 'builtin__edit', {'path': path, 'before_hash': source['hash'],
                         'old_text': 'SENTINEL', 'new_text': 'SENTINEL + FIRST'}, worker=True)
            require(not content(first).get('error'), 'dirty-file edit rejected')
            current = json.loads(content(call(h, 'read-first', 'builtin__read', {'path': path}, worker=True))['text'])
            second = call(h, 'second-edit', 'builtin__edit', {'path': path, 'before_hash': current['hash'],
                          'old_text': 'FIRST', 'new_text': 'SECOND'}, worker=True)
            require(not content(second).get('error') and (repo / path).read_text() == 'USER_UNCOMMITTED_SENTINEL + SECOND\n', 'continuous edits lost user content')
            stale = call(h, 'stale-edit', 'builtin__edit', {'path': path, 'before_hash': source['hash'],
                         'old_text': 'SECOND', 'new_text': 'OVERWRITE'}, worker=True)
            require(content(stale)['error']['code'] == 'PERMISSION_DENIED', 'stale preimage overwrote changes')
            created = call(h, 'create', 'builtin__write', {'path': 'output/result.txt', 'content': 'created'}, worker=True)
            require(not content(created).get('error'), 'file creation failed')
            denied = call(h, 'overwrite', 'builtin__write', {'path': 'output/result.txt', 'content': 'blind'}, worker=True)
            require(content(denied)['error']['code'] == 'PERMISSION_DENIED', 'blind overwrite allowed')
            return {'name': 'continuous_dirty_edits_create_and_stale_hash_refusal', 'state': h.state()}
        scenario('dirty-index', dirty_index)

        def audit(h):
            repo = h.root / 'repo'
            before = tree(repo)
            read = content(call(h, 'read', 'builtin__read', {'path': 'internal/retry/executor.go'}, worker=True))
            text = json.loads(read['text'])['text']
            require('provider.Execute()' in text and 'ledger.Lookup()' in text, 'read lacks call-order evidence')
            denied = call(h, 'denied-command', 'builtin__bash', {'command': 'touch forbidden'}, approve=False, worker=True)
            require(content(denied)['error']['code'] == 'APPROVAL_DENIED' and tree(repo) == before, 'unapproved bash modified workspace')
            traversal = content(call(h, 'traversal', 'builtin__read', {'path': '../config.yaml'}, worker=True))
            require(traversal['error']['code'] == 'PERMISSION_DENIED', 'file traversal escaped workspace')
            return {'name': 'reads_automatic_mutations_require_exact_approval', 'state': h.state()}
        scenario('audit', audit)

        def discovery(h):
            revision = select_managed_skills(h)
            listed = content(call(h, 'skills', 'builtin__skill_list', {'query':'review'}, worker=True))
            summaries = json.loads(listed['text'])
            require(len(summaries) == 1 and 'PRIVATE_BODY_MARKER' not in listed['text'], 'Skill list exposed body')
            viewed = content(call(h, 'view', 'builtin__skill_view', {'name': 'review'}, worker=True))
            require('PRIVATE_BODY_MARKER' in viewed['text'], 'Skill view missing actual body')
            asset = content(call(h, 'asset', 'builtin__skill_view',
                                 {'name': 'review', 'path': 'assets/opaque.bin'}, worker=True))
            opened = fixture_run(h.args('artifact', 'open', asset['artifact_refs'][0]), capture_output=True,
                                    env=h.env, cwd=h.root, timeout=20)
            require(opened.returncode == 0 and opened.stdout == b'\x00\xffSKILL_ASSET',
                    'binary Skill asset was corrupted or bypassed ArtifactStore')
            denied = content(call(h, 'manage', 'builtin__skill_manage', {}, worker=True))
            require(denied['error']['code'] == 'NOT_FOUND', 'ordinary Worker got Skill management')
            denied = content(call(h, 'task-control', 'builtin__task_manage', {'operation': 'cancel', 'taskRef': 'forged'}, worker=True))
            require(denied['error']['code'] == 'NOT_FOUND', 'Worker gained Task control')
            read = content(call(h, 'mcp-read', 'mcp__fixture__read', {'text': 'structured fixture response'}, worker=True))
            require(read['text'] == 'structured fixture response', 'stdio response lost')
            rejected = content(call(h, 'forged', 'mcp__fixture__read', {'text': 'x', 'call_key': 'forged'}))
            require(rejected['error']['code'] == 'INVALID_ARGUMENT', 'malformed MCP parameters dispatched')
            send = call(h, 'deny', 'mcp__fixture__send', {'text': 'must not send'}, approve=False)
            require(content(send)['error']['code'] == 'APPROVAL_DENIED' and not (h.root / 'provider/mcp-effects.jsonl').exists(), 'denied MCP dispatched')
            unknown = call(h, 'unknown', 'mcp__fixture__send', {'text': 'one order', 'behavior': 'unknown'})
            require(unknown['status'] == 'unknown' and len((h.root / 'provider/mcp-effects.jsonl').read_text().splitlines()) == 1,
                    'ambiguous MCP lost uncertainty')
            h.command('chat', 'work')
            require(len((h.root / 'provider/mcp-effects.jsonl').read_text().splitlines()) == 1, 'unknown MCP automatically retried')
            value = h.state()
            empty_skills = hashlib.sha256(b'[]').hexdigest()
            require(all(c['skill_set_hash'] != empty_skills for c in value['capabilities']), 'Main or Worker Skill set not frozen')
            main_request = json.loads(value['generations'][0]['request_json'])
            require({'builtin__skill_list', 'builtin__skill_view'} <= {t['name'] for t in main_request['tools']} and
                    'Inspect code and preserve user files.' not in json.dumps(main_request) and
                    'PRIVATE_BODY_MARKER' not in json.dumps(main_request), 'Main Skill disclosure is not progressive')
            workers = {r['run_id'] for r in value['runs'] if r['kind'] == 'worker'}
            serialized = json.dumps(next(g for g in value['generations'] if g['run_id'] in workers))
            require('Inspect code and preserve user files.' not in serialized and 'PRIVATE_BODY_MARKER' not in serialized,
                    'startup Skill disclosure is not progressive')
            return {'name': 'MCP_transport_Skill_scope_role_denial_unknown', 'state': value, 'managed_skills': check_managed_skill_receipts(h, revision)}
        scenario('discovery', discovery)

        def main_business(h):
            revision = select_managed_skills(h)
            provider = h.root / 'provider'
            read = call(h, 'main-read', 'mcp__fixture__read', {'text': 'direct Main lookup'})
            require(content(read)['text'] == 'direct Main lookup', 'Main lookup lost its result')
            listed = content(call(h, 'main-skill-list', 'builtin__skill_list', {'query':'review'}))
            summary = json.loads(listed['text'])[0]
            viewed = content(call(h, 'main-skill-view', 'builtin__skill_view',
                                  {'name': summary['name']}))
            require('PRIVATE_BODY_MARKER' in viewed['text'], 'Main cannot read the selected Skill')
            denied = call(h, 'main-deny', 'mcp__fixture__send', {'text': 'denied'}, approve=False)
            require(denied['attempt_count'] == 0 and content(denied)['error']['code'] == 'APPROVAL_DENIED' and
                    not (provider / 'mcp-effects.jsonl').exists(), 'denied Main write dispatched')
            approved = call(h, 'main-approve', 'mcp__fixture__send', {'text': 'approved once'})
            require(approved['status'] == 'succeeded' and approved['attempt_count'] == 1 and
                    len((provider / 'mcp-effects.jsonl').read_text().splitlines()) == 1,
                    'approved Main write missing or repeated')
            (provider / 'hold-barrier').touch()
            receipt = h.send('main-slow', 'Capabilities: ' + json.dumps([
                {'name': 'mcp__fixture__read', 'arguments': {'text': 'slow Main result', 'behavior': 'hold'}}]))
            start = h.processes.start if h.processes is not None else subprocess.Popen
            child = start(h.args('chat', 'work'), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          text=True, env=h.env, cwd=h.root)
            h.children.append(child)
            try:
                h.wait_state(lambda _: (provider / 'hold-accepted').exists(), 'real MCP accepted slow call', timeout=5)
                started = time.monotonic()
                steering = h.send('main-ping', 'Responsive MCP ping')
                require(steering['run_id'] == receipt['run_id'], 'slow call split the active Main')
                before = h.wait_state(lambda state: any('RESPONSIVE_MCP_PONG' in json.dumps(message)
                                      for message in state['conversation']), 'Main reply while MCP held', timeout=5)
                latency = time.monotonic() - started
                held = [t for t in before['tools'] if json.loads(t['arguments_json']).get('behavior') == 'hold']
                require(len(held) == 1 and held[0]['status'] == 'running' and held[0]['attempt_count'] == 1 and
                        not (provider / 'hold-release').exists(), 'reply waited for or repeated slow MCP')
            finally:
                (provider / 'hold-release').touch()
                stdout, stderr = child.communicate(timeout=20)
            require(child.returncode == 0, 'Main Code Mode work failed: ' + stderr + stdout)
            h.command('chat', 'work')
            after = h.state()
            held = [t for t in after['tools'] if json.loads(t['arguments_json']).get('behavior') == 'hold']
            require(len(held) == 1 and held[0]['status'] == 'succeeded' and held[0]['attempt_count'] == 1 and
                    'slow Main result' in json.dumps(after['conversation']), 'slow MCP completion lost or repeated')
            require(all(r['kind'] == 'main' for r in after['runs']), 'Main-scoped Code Mode unexpectedly delegated simple business or Skill inspection')
            require(len((provider / 'mcp-effects.jsonl').read_text().splitlines()) == 1, 'Main write replayed')
            return dict(name='Main_business_Skills_exact_approval_responsive_MCP',
                        managed_skills=check_managed_skill_receipts(h, revision),
                        response_seconds=latency, reply_before_tool_release=True, state=after)
        scenario('main-business', main_business)

        def tool_loading(h):
            schema = dict(type='object', properties={'text': {'type': 'string'}}, required=['text'], additionalProperties=False)
            catalog = [dict(name='read', description='Read fixture data', inputSchema=schema),
                       dict(name='send', description='Send fixture data', inputSchema=schema)]
            permissions = dict(read='read_only', send='external_effect')
            for index in range(498):
                name = 'lookup_%03d' % index
                catalog.append(dict(name=name, description='Lookup a catalog record', inputSchema=dict(
                    type='object', properties={'text': {'type': 'string', 'description': 'SCHEMA500_BODY_MARKER'}},
                    required=['text'], additionalProperties=False)))
                permissions[name] = 'read_only'
            provider = h.root / 'provider'
            (provider / 'mcp-tools.json').write_text(json.dumps(dict(tools=catalog)))
            config = h.root / 'config.yaml'
            config.write_text(config.read_text().replace(
                '    tools: {read: read_only, send: external_effect}\n',
                '    description: Find catalog records namespacediscoverytoken\n    tools: ' + json.dumps(permissions) + '\n'))
            read = call(h, 'catalog-code', 'mcp__fixture__read', {'text': 'Code Mode receipt'})
            require(content(read)['text'] == 'Code Mode receipt', 'script tool unavailable')
            initial = json.loads(h.state()['generations'][0]['request_json'])
            require(not any(t['name'].startswith('mcp__') and t['name'] != 'mcp__fixture__echo' for t in initial['tools']) and
                    'SCHEMA500_BODY_MARKER' not in json.dumps(initial) and
                    'Find catalog records' not in json.dumps(initial) and
                    '<mcp_servers>' not in json.dumps(initial), 'MCP catalog entered model context or native tool declarations')
            searched = call(h, 'catalog-search', 'builtin__tool_search', {'query': 'lookup catalog record'})
            matches = json.loads(content(searched)['text'])
            require(len(matches['tools']) == 8 and matches['loaded'] == [], 'metadata discovery activated native tools')
            h.send('catalog-forged', 'Direct Capabilities: ' + json.dumps([
                {'name': 'mcp__fixture__lookup_003', 'arguments': {'text': 'must not run'}}]))
            h.command('chat', 'work')
            forged = h.state()['tools'][-1]
            require(content(forged)['error']['code'] == 'NOT_FOUND' and forged['attempt_count'] == 0,
                    'direct MCP bypassed Code Mode')
            source = ("const found = await searchTools('lookup catalog record'); "
                      "const namespace = await searchTools('namespacediscoverytoken'); "
                      "if (ALL_TOOLS.some(t => t.name === 'builtin__tool_search')) throw Error('hidden or model-only tool leaked'); "
                      "return {found: found.length, namespace: namespace.length, count: ALL_TOOLS.filter(t=>t.name.startsWith('mcp__fixture__') && t.name !== 'mcp__fixture__echo').length, "
                      "receipt: await tools.mcp__fixture__lookup_002({text:'Worker receipt'})};")
            call(h, 'catalog-worker', 'builtin__codemode_execute', {'source': source}, worker=True)
            value = h.state()
            wrapper = next(t for t in reversed(value['tools']) if t['tool_name'] == 'builtin__codemode_execute')
            require(wrapper['status'] == 'succeeded', 'Worker Code Mode failed: ' + str(wrapper))
            output = json.loads(content(wrapper)['text'])[-1]['value']
            require(output['found'] == 8 and output['namespace'] == 8 and output['count'] == 500 and output['receipt'] == 'Worker receipt',
                    'Code Mode discovery or invocation differed: ' + str(output))
            workers = {r['run_id'] for r in value['runs'] if r['kind'] == 'worker'}
            require(all('SCHEMA500_BODY_MARKER' not in g['request_json'] and
                        'Find catalog records' not in g['request_json'] and
                        '<mcp_servers>' not in g['request_json'] for g in value['generations'] if g['run_id'] in workers),
                    'Worker received MCP schemas or directory through the model request')
            return dict(name='500_tools_Code_Mode_only', initial_mcp_declarations=0,
                        matched_mcp_declarations=0, script_mcp_capabilities=output['count'], state=h.state())
        scenario('tool-loading', tool_loading)

        def malformed_mcp(h):
            provider = h.root / 'provider'
            observations = []
            # The real stdio Fake records an effect before emitting each
            # malformed reply. Every CLI command opens a new Host process.
            for name, reply in [('null', None), ('missing-text', {'content': [{'type': 'text'}]})]:
                (provider / 'mcp-result.json').write_text(json.dumps(reply))
                read = call(h, 'read-' + name, 'mcp__fixture__read', {'text': 'read'})
                require(read['status'] == 'failed' and content(read)['error']['code'] == 'PROVIDER_UNAVAILABLE',
                        'malformed read reply accepted')
                invocation = call(h, 'send-' + name, 'mcp__fixture__send', {'text': name}, worker=True)
                require(invocation['status'] == 'unknown' and invocation['attempt_count'] == 1 and
                        invocation['result_json'] is None, 'malformed effect reply claimed completion')
                require('UNKNOWN_SIDE_EFFECT' in json.dumps(h.command('chat', 'history')[-1]),
                        'side-effect uncertainty was not delivered')
                effects = (provider / 'mcp-effects.jsonl').read_bytes()
                require(len(effects.splitlines()) == len(observations) + 1, 'effect missing or repeated')
                h.command('approval', 'reconcile')
                h.command('chat', 'work')
                current = next(t for t in h.state()['tools'] if t['invocation_id'] == invocation['invocation_id'])
                require(current['status'] == 'unknown' and current['attempt_count'] == 1 and
                        (provider / 'mcp-effects.jsonl').read_bytes() == effects,
                        'malformed effect lost uncertainty or retried after restart/reconcile')
                observations.append({'reply': reply, 'invocation': current, 'effect_count': len(effects.splitlines())})
            return {'name': 'malformed_stdio_effect_replies_stay_unknown', 'cases': observations, 'state': h.state()}
        scenario('mcp-reply', malformed_mcp)

        def crash(h):
            repo = h.root / 'repo'
            path = 'cmd/taskctl/main.go'
            source = json.loads(content(call(h, 'source', 'builtin__read', {'path': path}, worker=True))['text'])
            replacement = source['text'].replace('package main', 'package main\n// accepted edit', 1)
            plan = [{'name': 'builtin__edit', 'arguments': {'path': path, 'before_hash': source['hash'],
                     'old_text': 'package main', 'new_text': 'package main\n// accepted edit'}}]
            h.send('crash-edit', 'Delegate capabilities: ' + json.dumps(plan))
            h.command('chat', 'work')
            a = [a for a in h.command('approval', 'list') if a['status'] == 'pending'][0]
            h.command('approval', 'approve', a['approval_id'], a['frozen_arguments_hash'])
            child = h.hold('before_tool_commit')
            child.send_signal(signal.SIGKILL)
            child.communicate(timeout=10)
            require((repo / path).read_text() == replacement, 'crash not after actual edit')
            h.command('chat', 'work')
            value = h.state()
            inv = [t for t in value['tools'] if t['tool_name'] == 'builtin__edit'][0]
            require(inv['status'] == 'unknown' and inv['attempt_count'] == 1 and (repo / path).read_text() == replacement,
                    'crashed edit retried or falsely completed')
            return {'name': 'edit_after_effect_before_result_commit', 'signal': 'SIGKILL', 'state': value}
        scenario('crash', crash, kind='crashes')

        def enable_task_code_source(h):
            source = (h.root / 'repo').resolve()
            workspaces = h.root.resolve() / 'task-workspaces'
            text = (h.root / 'config.yaml').read_text()
            old = 'execution:\n  root: ' + json.dumps(str((h.root / 'repo').resolve()))
            new = 'execution:\n  root: ' + json.dumps(str(workspaces)) + '\n  repository_source: ' + json.dumps(str(source))
            require(old in text, 'coding fixture execution configuration missing')
            (h.root / 'config.yaml').write_text(text.replace(old, new))
            return source, workspaces

        def task_code_environments(h):
            source, workspaces = enable_task_code_source(h)
            original = tree(source)
            before_hash = digest(source / 'docs/notes.md')
            for label in ('A', 'B'):
                plan = [
                    dict(name='builtin__read', arguments=dict(path='AGENTS.md')),
                    dict(name='builtin__edit', arguments=dict(path='docs/notes.md', before_hash=before_hash,
                         old_text='USER_UNCOMMITTED_SENTINEL', new_text='USER_UNCOMMITTED_SENTINEL\nTask '+label)),
                    dict(name='builtin__write', arguments=dict(path='task-result.txt', content=label+'\n')),
                    dict(name='builtin__bash', arguments=dict(command='exit 7')),
                    dict(name='builtin__bash', arguments=dict(command='test "$(cat task-result.txt)" = '+label))]
                h.send('task-code-'+label, 'Delegate capabilities: '+json.dumps(plan))
                h.command('chat', 'work')
            def environments():
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    db.row_factory=sqlite3.Row
                    return [dict(r) for r in db.execute('SELECT t.task_ref,e.directory,e.baseline_hash,e.status,e.error_code FROM task_code_environment e JOIN task t USING(task_id) ORDER BY t.created_at,t.task_id')]
            copies = environments()
            require(len(copies)==2 and all(w['status']=='ready' for w in copies) and copies[0]['directory']!=copies[1]['directory'], 'two pending Task copies were not isolated')
            # Every CLI call is another process: pending approvals and directory
            # identities must survive closing the original execution owner.
            for _ in range(12):
                approvals=[a for a in h.command('approval','list') if a['status']=='pending']
                if not approvals: break
                for a in approvals:
                    h.command('approval','approve',a['approval_id'],a['frozen_arguments_hash'])
                h.command('chat','work')
            reports=[]
            for label,w in zip(('A','B'),copies):
                directory=Path(w['directory'])
                if not (directory/'task-result.txt').is_file():
                    attempts = [(i['tool_name'], i['status'], i['attempt_count'], i['result_json']) for i in h.state()['tools']]
                    raise RuntimeError('scoped real tools did not produce the approved Task file: ' + str(attempts))
                require((directory/'task-result.txt').read_text()==label+'\n' and ('Task '+label) in (directory/'docs/notes.md').read_text(), 'Task edits contaminated another copy')
                require(not (directory/'.git').exists(), 'source Git metadata was imported')
                inspect=h.command('coding','inspect',w['task_ref'])
                require(inspect['directory']==w['directory'] and inspect['baseline_sha256']==w['baseline_hash'], 'restart rebound the Task working copy')
                commands=[json.loads(c['result']['text']) for c in inspect['commands']]
                require([c['exit_code'] for c in commands]==[7,0], 'actual failed/successful command evidence was lost or invented')
                artifact=h.command('coding','report',w['task_ref'])
                review=h.command('artifact','open',artifact['artifact_ref'])
                require('directory' not in review and review['diff']['changed'] and 'USER_UNCOMMITTED_SENTINEL' in review['diff']['patch'], 'review Artifact lost the imported dirty baseline or exposed host paths')
                require(str(workspaces) not in review['diff']['patch'] and 'b/task-result.txt' in review['diff']['patch'], 'patch headers were not repository relative')
                reports.append(dict(task_ref=w['task_ref'], artifact=artifact, command_exit_codes=[c['exit_code'] for c in commands]))
            # Use a normalized successful observer snapshot without configuring
            # any real token or reaching GitHub. Bind via the actual owner CLI.
            config = h.root / 'config.yaml'
            config.write_text(config.read_text() + '\ngithub:\n  repository: fixture/repo\n  pull_number: 7\n  credential_ref: env:STEWARD_SYNTHETIC_HANDOFF_TOKEN\n')
            head, observed = 'a'*40, time.time_ns() // 1000
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.execute("""INSERT INTO github_watch(scope,repository,pull_number,repository_id,enabled,intent_privacy_epoch,state,head_sha,last_success_at,version)
                            VALUES('fixture/repo#7','fixture/repo',7,99,1,0,'healthy',?,?,1)""", (head, observed))
            first = reports[0]['task_ref']
            binding = h.command('github', 'bind', text=json.dumps(dict(task_ref=first, task_epoch=1, version=0, mode='observe', event_kind='review')))
            watch = h.command('github', 'status')
            artifact = h.command('coding', 'report-pr', first, head)
            handoff = h.command('artifact', 'open', artifact['artifact_ref'])
            association = handoff.pop('pr_association')
            ordinary = h.command('artifact', 'open', reports[0]['artifact']['artifact_ref'])
            require('pr_association' not in ordinary and handoff['task_ref']==ordinary['task_ref'] and
                    handoff['baseline_sha256']==ordinary['baseline_sha256'] and handoff['commands']==ordinary['commands'] and
                    handoff['diff']['files']==ordinary['diff']['files'] and 'b/task-result.txt' in handoff['diff']['patch'],
                    'handoff changed local report evidence')
            observed_at = datetime.fromisoformat(association['observed_at'].replace('Z', '+00:00'))
            require(association['kind']=='owner_requested_association' and association['repository']=='fixture/repo' and
                    association['repository_id']==99 and association['pull_number']==7 and association['head_sha']==head and
                    round(observed_at.timestamp()*1_000_000)==observed,
                    'report mislabeled persisted PR identity or observation time')
            require(h.command('github', 'status')==watch and h.command('github', 'binding')==binding,
                    'report changed watcher or association')
            for ref, expected in ((first, 'b'*40), (reports[1]['task_ref'], head)):
                rejected = h.command('coding', 'report-pr', ref, expected, ok=False)
                require(rejected.returncode!=0 and not rejected.stdout, 'wrong Task or expected head accepted')
            h.command('github', 'unbind', text=json.dumps(dict(version=binding['version'])))
            rejected = h.command('coding', 'report-pr', first, head, ok=False)
            require(rejected.returncode!=0 and not rejected.stdout, 'revoked binding accepted')
            require(h.command('artifact', 'open', artifact['artifact_ref'])['pr_association']==association,
                    'existing historical report changed after unbind')
            require(tree(source)==original and not (source/'hook-ran').exists(), 'source dirty files or Git hooks changed')
            (source/'.git').rename(source/'.git-disabled')
            try:
                h.send('bad-source-task', 'Delegate capabilities: '+json.dumps([dict(name='builtin__read',arguments=dict(path='AGENTS.md'))]))
                h.command('chat','work')
            finally:
                (source/'.git-disabled').rename(source/'.git')
            value=environments()
            require(sum(w['status']=='failed' and w['error_code']=='CODE_PREPARATION_FAILED' for w in value)==1 and sum(w['status']=='ready' for w in value)==2, 'preparation failure affected another Task')
            return dict(name='task_code_environment_scope_diff_and_evidence', reports=reports, pr_handoff=artifact, preparation_failure='isolated', source_preserved=True)
        scenario('task-code-environments', task_code_environments)

        def task_code_exact_report(h):
            source, _ = enable_task_code_source(h)
            def owner(*args, text=None, ok=True):
                # Control operations use the production composition; only model
                # execution below uses the explicit protocol peer fixture.
                result = fixture_run([str(h.binary), '-config', str(h.root/'config.yaml'), *args],
                                     input=text, text=True, capture_output=True, env=h.env, cwd=h.root, timeout=320)
                require(not ok or result.returncode==0, f'{args}: {result.stderr}')
                return json.loads(result.stdout) if ok else result
            git_env = dict(PATH='/usr/bin:/bin', HOME='/nonexistent', GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null')
            def git(*args):
                return fixture_run(['git', '-C', str(source), '-c', 'core.hooksPath=/dev/null', '-c', 'maintenance.auto=false', '-c', 'gc.auto=0', *args],
                                   env=git_env, capture_output=True, text=True, check=True).stdout.strip()
            # Explicit fixture-owner commit precedes the source-preservation
            # boundary. No Task imports Git metadata or executes its hook.
            git('add', '.')
            git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.test', 'commit', '-qm', 'exact owner fixture')
            head, original = git('rev-parse', 'HEAD'), tree(source)
            owner('artifact', 'save', 'bootstrap.txt', 'text/plain', text='Activate the actual managed owner composition.')
            h.send('exact-task', 'Delegate capabilities: '+json.dumps([dict(name='builtin__read', arguments=dict(path='AGENTS.md'))]))
            h.command('chat', 'work')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                rows = db.execute("SELECT t.task_ref,e.directory,t.task_id FROM task t JOIN task_code_environment e USING(task_id) WHERE e.status='ready'").fetchall()
                require(len(rows)==1, 'exact owner Task was not actually prepared')
                ref, directory, task_id = rows[0]
                graph = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
                require(graph and graph.startswith('sha256:'), 'production owner composition was not prepared')
                require(db.execute('SELECT graph_revision FROM code_import_policy_binding WHERE task_id=?',(task_id,)).fetchone()==(graph,), 'exact Task used native import instead of its managed recipe')
            config = h.root/'config.yaml'
            config.write_text(config.read_text()+'\ngithub:\n  repository: fixture/repo\n  pull_number: 7\n  poll_seconds: 60\n  credential_ref: env:STEWARD_SYNTHETIC_HANDOFF_TOKEN\n')
            observed = time.time_ns()//1000
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.execute("""INSERT INTO github_watch(scope,repository,pull_number,repository_id,enabled,intent_privacy_epoch,state,head_sha,last_success_at,version)
                            VALUES('fixture/repo#7','fixture/repo',7,99,1,0,'healthy',?,?,1)""",(head,observed))
            binding = owner('github','bind',text=json.dumps(dict(task_ref=ref,task_epoch=1,version=0,mode='observe',event_kind='review')))
            watch = owner('github','status')
            artifact = owner('coding','report-pr-exact',ref,head)
            review = owner('artifact','open',artifact['artifact_ref'])
            association = review['pr_association']
            require(association['kind']=='owner_verified_exact_content' and association['head_sha']==head and
                    association['content']['head']==head and association['content']['tree_sha256']==review['candidate_sha256'] and
                    association['content']['git_version'] and len(association['content']['git_command_sha256'])==64 and
                    review['review']['original_task'] and review['review']['guide'], 'actual CLI did not retain exact content and managed review facts')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                require(db.execute('SELECT graph_revision FROM code_report_method_binding WHERE artifact_ref=?',(artifact['artifact_ref'],)).fetchone()==(graph,), 'exact CLI report bypassed its managed producer')
                before = db.execute('SELECT count(*) FROM artifact').fetchone()[0]
            wrong = ('b' if head[0]!='b' else 'c')*len(head)
            rejected = owner('coding','report-pr-exact',ref,wrong,ok=False)
            require(rejected.returncode!=0 and not rejected.stdout, 'exact CLI accepted another observed head')
            Path(directory,'unreviewed.txt').write_text('extra candidate bytes\n')
            changed = owner('coding','diff',ref)
            require(changed['candidate_sha256']!=review['candidate_sha256'], 'extra untracked bytes did not change the real candidate')
            rejected = owner('coding','report-pr-exact',ref,head,ok=False)
            require(rejected.returncode!=0 and not rejected.stdout and time.time_ns()//1000-observed<120_000_000,
                    'exact CLI did not reject a mismatching candidate under a fresh observation')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                require(db.execute('SELECT count(*) FROM artifact').fetchone()[0]==before, 'rejected exact handoff wrote an Artifact')
            require(owner('github','status')==watch and owner('github','binding')==binding, 'exact read changed observer or binding facts')
            owner('github','unbind',text=json.dumps(dict(version=binding['version'])))
            require(owner('artifact','open',artifact['artifact_ref'])==review, 'original exact Artifact changed after candidate drift or unbind')
            require(tree(source)==original and not (source/'hook-ran').exists(), 'exact read changed owner source, index, refs, or hook')
            return dict(name='task_code_exact_remote_content_report',artifact=artifact,head=head,candidate_sha256=review['candidate_sha256'],
                        git=association['content'],managed_graph=graph,source_preserved=True,
                        scope='real owner CLI and local raw objects under normalized observer fixture; no GitHub publication')
        scenario('task-code-exact-report',task_code_exact_report)

        def task_code_recovery(h):
            source, _ = enable_task_code_source(h)
            note = source / 'docs/notes.md'
            results = []
            def environment():
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    db.row_factory = sqlite3.Row
                    return dict(db.execute("""SELECT t.task_ref,e.directory,e.status,e.error_code,r.status AS root_status
                        FROM task_code_environment e JOIN task t USING(task_id) JOIN run r USING(task_id)
                        WHERE r.worker_position='root' ORDER BY t.created_at DESC,t.task_id DESC LIMIT 1""").fetchone())
            for index, (boundary, stop) in enumerate((('before_task_code_publish', signal.SIGTERM),
                     ('before_task_code_publish', signal.SIGKILL), ('after_task_code_publish', signal.SIGKILL))):
                original = f'owner dirty {index}\n'
                note.write_text(original)
                h.send(f'code-recovery-{index}', 'Delegate capabilities: []')
                child = h.hold(boundary)
                pending = environment()
                directory = Path(pending['directory'])
                published = boundary.startswith('after_')
                require(pending['status']=='pending' and directory.is_dir()==published, 'wrong publication boundary')
                child.send_signal(stop)
                child.communicate(timeout=10)
                expected_exit = -signal.SIGKILL if stop == signal.SIGKILL else 0
                require(child.returncode == expected_exit, f'{stop.name} returned {child.returncode}, expected {expected_exit}')
                stopped = environment()
                require(stopped['status']=='pending' and stopped['error_code']=='' and stopped['root_status']!='failed',
                        'interruption falsely failed Task preparation')
                newer = f'owner later edit {index}\n'
                note.write_text(newer)
                h.command('chat','work')
                resumed = environment()
                require(resumed['task_ref']==pending['task_ref'] and resumed['directory']==pending['directory'] and
                        resumed['status']=='ready' and resumed['root_status']=='completed', 'restart rebound or lost Task')
                require((directory/'docs/notes.md').read_text()==(original if published else newer) and note.read_text()==newer,
                        'restart recopied a published receipt or altered the source')
                require(not list(directory.parent.parent.glob('.prepare-*')), 'stale staging survived recovery')
                results.append(dict(boundary=boundary, signal=stop.name, returncode=child.returncode,
                                    reused_published_snapshot=published))
            return dict(name='task_code_publish_recovery', boundaries=results)
        scenario('task-code-recovery', task_code_recovery, kind='crashes')
        for kind, result in run_cases(cases, drive, scenario_jobs):
            report[kind].append(result)
        if not selected:
            expected = {'500_tools_Code_Mode_only',
                        'Main_business_Skills_exact_approval_responsive_MCP',
                        'real_red_edit_green_with_Worker_approval',
                        'continuous_dirty_edits_create_and_stale_hash_refusal',
                        'reads_automatic_mutations_require_exact_approval',
                        'MCP_transport_Skill_scope_role_denial_unknown',
                        'malformed_stdio_effect_replies_stay_unknown',
                        'task_code_environment_scope_diff_and_evidence',
                        'task_code_exact_remote_content_report'}
            require(len(report['checks']) == len(expected) and
                    {row['name'] for row in report['checks']} == expected and
                    {row['name'] for row in report['crashes']} == {'edit_after_effect_before_result_commit', 'task_code_publish_recovery'} and
                    len(report['crashes']) == 2,
                    'complete software capability scenario inventory required: checks=' + str(sorted(row['name'] for row in report['checks'])) + ', crashes=' + str(sorted(row['name'] for row in report['crashes'])))
    report['cleanup'] = not root.exists()
    require(report['cleanup'], 'disposable roots remain')
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--scenario', choices=['coding', 'dirty-index', 'audit', 'quotes', 'discovery', 'main-business', 'tool-loading', 'mcp-reply', 'crash', 'task-code-environments', 'task-code-recovery', 'task-code-exact-report'],
                        help='Run one maintained scenario; omit for the complete gate')
    parser.add_argument("--scenario-jobs",type=int,choices=[1,2,4],default=int(os.environ.get("STEWARD_VERIFY_CASE_JOBS","1")))
    args = parser.parse_args()
    report = run(args.binary.resolve(), args.scenario, scenario_jobs=args.scenario_jobs)
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'checks': len(report['checks']), 'crashes': len(report['crashes']), 'cleanup': report['cleanup']}))
