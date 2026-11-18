#!/usr/bin/env python3
"""Stock CLI -> fixed GitHub HTTPS -> actual policy/SQLite; optional old28.

The owned CONNECT peer terminates only api.github.com with a temporary trusted
test certificate. TLS verification stays enabled. No product endpoint override,
real PAT, external source, model quality score or recovery-side fixture repair.
"""
import argparse
from owner_inventory import check_default_owner
from contextlib import closing, contextmanager
import hashlib
import io
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import socketserver
import sqlite3
import subprocess
import ssl
import sys
import tempfile
import threading

from fixture_pool import current, run as owned_run, run_cases
from verify_capabilities import CapabilityHarness
from verify_main import require
from verify_tool_discovery_plugins import owner_state, install_complete_bundle, activate_owner
from provider_endpoint import Endpoint

ROOT = Path(__file__).resolve().parents[2]
TOKEN = 'synthetic-github-source-token'
CONTRACT = 'steward.github-pr-followup/1'
HEAD = 'a' * 40


class GitHubHarness(CapabilityHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'), *args]


def configure(root, bridge_binary, missing_skills=False):
    node = shutil.which('node')
    require(node, 'doctor: pinned Node is required')
    config = dict(version=1, github=dict(repository='owner/repo', pull_number=7,
        credential_ref='env:GITHUB_PLUGIN_TOKEN', poll_seconds=5),
        llm=dict(provider_bridge_command=str(bridge_binary), provider_bridge_script='-model-bridge',
                 provider_bridge_version='steward-go-v1'),
        javascript=dict(command=node, script=str(ROOT/'runtime/javascript/src/sidecar.mjs')))
    if missing_skills:
        config['skills'] = dict(root=str(root/'deliberately-missing-skills'), scope='agent')
    (root/'config.yaml').write_text(json.dumps(config))


def harness(binary, root, proxy, certificate, missing_skills=False):
    configure(root, binary, missing_skills)
    h = GitHubHarness(binary, root, fixture='')
    for key in ('HTTP_PROXY', 'http_proxy', 'HTTPS_PROXY', 'https_proxy', 'ALL_PROXY', 'all_proxy', 'NO_PROXY', 'no_proxy'):
        h.env.pop(key, None)
    h.env.update(HTTPS_PROXY=proxy, NO_PROXY='127.0.0.1,localhost', SSL_CERT_FILE=str(certificate),
                 GITHUB_PLUGIN_TOKEN=TOKEN)
    h.env.pop('COMPOSIO_API_KEY', None)
    return h


@contextmanager
def source_peer(root):
    # Trust is confined to the owned Host processes. Never alter system trust.
    key, cert, conf = root/'source-key.pem', root/'source-cert.pem', root/'source-cert.cnf'
    conf.write_text('[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=extensions\n'
                    '[dn]\nCN=api.github.com\n[extensions]\nsubjectAltName=DNS:api.github.com\n'
                    'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyEncipherment,keyCertSign\n'
                    'extendedKeyUsage=serverAuth\n')
    result = owned_run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-config', str(conf), '-keyout', str(key), '-out', str(cert)],
                       capture_output=True, text=True, timeout=20)
    require(result.returncode == 0, 'doctor: could not create owned source TLS certificate')
    key.chmod(0o600)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    tls.set_alpn_protocols(['http/1.1'])
    state = dict(calls=[], failures=[], enabled=True, source_id=1)
    lock = threading.Lock()

    class Source(BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.0'
        def log_message(self, *_):
            pass

        def do_GET(self):
            with lock:
                state['calls'].append(self.path)
                valid = (self.headers.get('Host') == 'api.github.com'
                         and self.headers.get('Authorization') == 'Bearer '+TOKEN
                         and self.headers.get('X-GitHub-Api-Version') == '2026-03-10')
                enabled, source_id = state['enabled'], state['source_id']
                if not valid:
                    state['failures'].append('source authority changed')
            status, body = 200, None
            if not valid:
                status, body = 403, {}
            elif self.path == '/user':
                body = dict(id=1, login='owner')
            elif self.path == '/repos/owner/repo/pulls/7':
                body = dict(number=7, head=dict(sha=HEAD), base=dict(repo=dict(id=99, full_name='owner/repo')))
            elif self.path == '/repos/owner/repo/pulls/7/comments?per_page=100&page=1':
                body = [dict(id=source_id, body='你'*6000, updated_at='2026-11-01T10:00:00Z',
                    html_url='https://github.com/owner/repo/pull/7#discussion_r1',
                    user=dict(id=2, login='reviewer'))] if enabled else []
            elif self.path == '/repos/owner/repo/pulls/7/reviews?per_page=100&page=1':
                body = []
            elif self.path == f'/repos/owner/repo/commits/{HEAD}/check-runs?filter=all&per_page=100&page=1':
                body = dict(check_runs=[])
            else:
                with lock:
                    state['failures'].append('unexpected source path')
                status, body = 404, {}
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.send_header('Connection', 'close')
            self.end_headers()
            self.wfile.write(raw)

    class Connect(socketserver.StreamRequestHandler):
        def handle(self):
            first = self.rfile.readline(8193)
            if first != b'CONNECT api.github.com:443 HTTP/1.1\r\n':
                with lock:
                    state['failures'].append('unexpected CONNECT target')
                return
            for _ in range(32):
                line = self.rfile.readline(8193)
                if line == b'\r\n':
                    break
                if not line or len(line) > 8192:
                    return
            else:
                return
            self.wfile.write(b'HTTP/1.1 200 Connection established\r\n\r\n')
            self.wfile.flush()
            with tls.wrap_socket(self.request, server_side=True) as secured:
                Source(secured, self.client_address, self.server)

    server = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Connect)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_address[1]}', cert, state
    finally:
        server.shutdown(); server.server_close(); thread.join(timeout=10)
        require(not thread.is_alive(), 'owned source TLS peer did not join')


def fresh_poll(binary, root, proxy, cert, source):
    h = harness(binary, root, proxy, cert, missing_skills=True)
    try:
        require(h.command('github', 'status')['state'] == 'never_checked', 'fresh source was implicitly read')
        require(h.command('github', 'binding')['state'] == 'unbound', 'fresh binding was fabricated')
        require(h.command('plugin', 'status')['epoch'] == 0 and not source['calls'],
                'read-only owner control initialized execution')
        h.command('github', 'poll', timeout=60)
        selected = h.command('plugin', 'status')
        require(selected['epoch'] == 1 and not selected['stopped'], 'first actual poll did not verify/select default40')
        with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db, db:
            db.execute('BEGIN')
            graph, policy = owner_state(h, selected)
            owner_cases = check_default_owner(graph, policy)
            row = db.execute('SELECT content_json,source_method_json FROM github_observation').fetchone()
            require(row and json.loads(row[1])['graph_revision'] == selected['graph_revision'],
                    'actual stock poll bypassed selected source method')
            require(db.execute('SELECT count(*) FROM run').fetchone()[0] == 0
                    and db.execute('SELECT count(*) FROM model_generation').fetchone()[0] == 0,
                    'source-only bootstrap fabricated Agent/model work')
            original = row
        require(h.command('status')['readiness'] == 'setup_required', 'source poll required or activated a model')
        h.command('github', 'poll')
        require(h.command('plugin', 'status') == selected and len(h.command('github', 'events')) == 1,
                'first cold dependent poll repaired selection or duplicated a source')
        with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db, db:
            require(db.execute('SELECT content_json,source_method_json FROM github_observation').fetchone() == original,
                    'cold polling rewrote original source/recipe')
        h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='source-default-disable',
            expected_graph_revision=selected['graph_revision'])))
        before = len(source['calls'])
        failed = h.command('github', 'poll', ok=False)
        require(failed.returncode != 0 and 'METHOD_UNAVAILABLE' in failed.stderr and len(source['calls']) == before,
                'deactivated cold source restored native IO')
        h.command('github', 'stop'); h.command('github', 'resume')
        failed = h.command('github', 'poll', ok=False)
        require(failed.returncode != 0 and len(source['calls']) == before,
                'explicit watch resume restored deactivated policy')
        return dict(complete_default40=True, independent_owner_cases=owner_cases, no_model_or_runtime_setup=True,
            readonly_control_no_selection=True, cold_original_source_and_recipe=True,
            disabled_no_native_http=True, cleanup=True), graph, policy
    finally:
        h.cleanup()


def portable_startup(binary, root):
    # macOS uses native certificate trust, not Linux's SSL_CERT_FILE. Prove the
    # stock control/bootstrap boundary with a missing credential, and require
    # the actual App TLS path separately. Successful stock HTTPS runs on Linux.
    h = harness(binary, root, '', root/'unused-certificate', missing_skills=True)
    h.env.pop('GITHUB_PLUGIN_TOKEN', None)
    try:
        h.command('github', 'status'); h.command('github', 'binding')
        require(h.command('plugin', 'status')['epoch'] == 0, 'read-only source control selected execution')
        failed = h.command('github', 'poll', ok=False, timeout=60)
        require(failed.returncode != 0 and 'CREDENTIAL_MISSING' in failed.stderr,
                'source-only startup required unrelated runtime or concealed credential failure')
        selected = h.command('plugin', 'status')
        graph, policy = owner_state(h, selected)
        require(selected['epoch'] == 1, 'stock source bootstrap did not select default graph')
        owner_cases = check_default_owner(graph, policy)
        require(h.command('status')['readiness'] == 'setup_required', 'source bootstrap activated a model')
        h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='source-startup-disable',
            expected_graph_revision=selected['graph_revision'])))
        failed = h.command('github', 'poll', ok=False)
        require(failed.returncode != 0 and 'METHOD_UNAVAILABLE' in failed.stderr,
                'deactivated source reached native credential path')
        h.command('github', 'stop'); h.command('github', 'resume')
        return dict(stock_control_bootstrap30_without_model=True, independent_owner_cases=owner_cases,
                    missing_credential_preserved=True, disabled_before_credential=True, cleanup=True)
    finally:
        h.cleanup()


def actual_app(binary, evidence):
    name = 'TestGitHubDefaultProductionControlPollAndColdLifecycle'
    environment = dict(os.environ, STEWARD_TEST_APP_DEFAULT_BUNDLE=str(binary))
    result = owned_run(['go', 'test', '-race', '-json', './internal/app', '-run', '^'+name+'$',
        '-count=1', '-timeout=10m'], cwd=ROOT, env=environment, capture_output=True, text=True, timeout=660)
    log = evidence.with_name(evidence.stem+'-app-go.jsonl'); log.write_text(result.stdout)
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    terminal = [row['Action'] for row in events if row.get('Test') == name
                and row['Action'] in ('pass', 'fail', 'skip')]
    require(result.returncode == 0 and terminal == ['pass'],
            'actual complete40 source App failed/skipped: '+result.stdout[-10000:]+result.stderr[-2000:])
    return dict(actual_app_verified_tls_source=True, exact30_bundle=True, terminal=terminal,
                cold_first_dependent_action=True, disabled_no_native=True, log=str(log))


@contextmanager
def model_peer():
    # Hold only the external model response. The archived stock CLI, its native
    # bridge and its already-durable generation admission remain unmodified.
    class Peer(Endpoint):
        def do_POST(self):
            raw = self.rfile.read(int(self.headers['Content-Length']))
            self.rfile = io.BytesIO(raw)
            body = json.loads(raw)
            source = any(str(m.get('content', '')).startswith('Runtime GitHub observation')
                         for m in body.get('messages', []))
            if self.server.hold_source.is_set() and source:
                self.server.source_entered.set()
                require(self.server.release_source.wait(30), 'owned model response was not released')
            try:
                super().do_POST()
            except (BrokenPipeError, ConnectionResetError):
                # Expected after SIGKILL of the original pending HTTP request.
                pass

    server = ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    server.mode, server.requests = 'github-plugins', []
    server.hold_source, server.source_entered, server.release_source = (threading.Event() for _ in range(3))
    thread = threading.Thread(target=server.serve_forever); thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}/v1', server
    finally:
        server.release_source.set()
        server.shutdown(); server.server_close(); thread.join(timeout=10)
        require(not thread.is_alive(), 'source model HTTP peer did not join')


def archived_source(binary, prior, root, proxy, cert, source, graph, policy):
    old_hash = hashlib.sha256(prior.read_bytes()).hexdigest()
    source['enabled'] = False
    h = harness(prior, root, proxy, cert)
    with model_peer() as (endpoint, model):
        try:
            h.setup(metadata=dict(name='Scoped PR work', provider_kind='openai_compatible', endpoint=endpoint,
                wire_api='openai_completions', model_id='fixture-model', context_window=65536,
                max_output_tokens=4096, capabilities=dict(text_input=True, tool_calls=True, streaming=True),
                compatibility=dict(tool_result_format='openai')))
            h.command('chat', 'work')
            original = h.command('plugin', 'status')
            old_graph, old_policy = owner_state(h, original)
            require(len(old_graph['bindings']) == 28, 'source archive did not start from actual28')
            h.send('github-original-task', 'GITHUB_TASK_CREATE work only on the exact PR')
            h.command('chat', 'work')
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db, db:
                task_ref, epoch = db.execute('SELECT task_ref,task_epoch FROM task').fetchone()
                require(db.execute('SELECT count(*) FROM task').fetchone()[0] == 1, 'model peer did not create one original Task')
            h.command('github', 'poll')
            h.command('github', 'bind', text=json.dumps(dict(task_ref=task_ref, task_epoch=epoch,
                version=0, mode='automatic', event_kind='review_comment')))
            source.update(enabled=True, source_id=1)
            h.command('github', 'poll')
            model.hold_source.set()
            held = current().start(h.args('chat', 'work'), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, env=h.env, cwd=h.root)
            h.children.append(held)
            require(model.source_entered.wait(30), 'original stock Main did not reach the held external model response')
            held.kill(); held.wait(timeout=10)
            require(held.returncode == -9, 'source Main did not crash after durable generation admission')
            current().retire(held); held.communicate(timeout=10)
            model.hold_source.clear(); model.release_source.set()
            dbpath = Path(h.env['STEWARD_DATA_ROOT'])/'state.db'
            def frozen():
                with closing(sqlite3.connect(dbpath)) as db, db:
                    db.execute('BEGIN')
                    row = db.execute('SELECT r.run_id,r.checkpoint_json,c.tools_json,b.graph_revision,b.managed_services_json,'
                        'g.request_hash,g.request_json FROM github_task_followup f JOIN run r USING(run_id) '
                        'JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id JOIN rsi_run b USING(run_id) '
                        'JOIN model_generation g USING(run_id)').fetchone()
                    obs = db.execute('SELECT sequence,event_key,content_json FROM github_observation').fetchone()
                    return row, obs
            before, observation = frozen()
            require(before and before[3] == original['graph_revision'] and observation,
                    'archived source Main/request was not actually frozen')
            ref = 'github:'+str(observation[0])
            page = h.command('evidence', 'read', text=json.dumps(dict(ref=ref, limit=4096)))
            require(page['kind'] == 'github_observation' and page['text'] and page.get('page_revision') == original['graph_revision'], 'old bound source or exact pagination reference unavailable')
            h.binary = binary
            require(h.command('plugin', 'status') == original and frozen() == (before, observation),
                    'forward migration changed old source/Main/request or selected another graph')
            target = install_complete_bundle(h, binary, graph)
            selected = activate_owner(h, target, policy, original['graph_revision'], 'source-complete40-owner')
            require(frozen() == (before, observation), 'new source activation rewrote original frozen request')
            h.command('chat', 'work')
            with closing(sqlite3.connect(dbpath)) as db, db:
                require(db.execute('SELECT count(*) FROM task').fetchone()[0] == 1
                        and db.execute('SELECT count(*) FROM task_instruction').fetchone()[0] == 1,
                        'old source did not append exactly once to the original Task')
                require(db.execute('SELECT source_method_json FROM github_observation').fetchone()[0] is None,
                        'migration retrofitted the old native source recipe')
                require(db.execute('SELECT request_hash,request_json FROM model_generation WHERE run_id=?',
                    (before[0],)).fetchone() == before[5:], 'old complete model request changed during recovery')
            old_window = dict(ref=ref, limit=4096, page_revision=page['page_revision'])
            require(h.command('evidence', 'read', text=json.dumps(old_window)) == page,
                    'old exact source/reader reference changed after owner upgrade and actual recovery')
            h.command('github', 'poll')
            require(len(h.command('github', 'events')) == 1, 'managed display created a duplicate of the old native source')
            source['source_id'] = 2
            h.command('github', 'poll'); h.command('chat', 'work')
            with closing(sqlite3.connect(dbpath)) as db, db:
                require(db.execute('SELECT count(*) FROM task').fetchone()[0] == 1
                        and db.execute('SELECT count(*) FROM task_instruction').fetchone()[0] == 2,
                        'new source expanded Task scope or lost original continuation')
                managed = db.execute('SELECT source_method_json FROM github_observation WHERE source_id=2').fetchone()[0]
                require(json.loads(managed)['graph_revision'] == selected['graph_revision'], 'new source bypassed actual29 method')
            require(hashlib.sha256(prior.read_bytes()).hexdigest() == old_hash, 'original28 artifact changed')
            return dict(actual_old28_sha256=old_hash, original_graph=original['graph_revision'],
                current_graph=selected['graph_revision'], crash_exit=-9, boundary='actual external model request before response;stock CLI/native bridge', frozen_original_request=True,
                same_task_two_exact_appends=True, old_source_and_page_reference=True,
                original_native_method_null=True, same_source_not_duplicated=True, new_source_managed=True,
                model='synthetic HTTP peer;native model bridge of original pinned28 remains configured', cleanup=True)
        except Exception as error:
            diagnostic = dict(error=str(error), external_model_request_reached=model.source_entered.is_set(), source_calls=source['calls'])
            dbpath = Path(h.env['STEWARD_DATA_ROOT'])/'state.db'
            if dbpath.exists():
                with closing(sqlite3.connect(dbpath)) as db, db:
                    db.execute('BEGIN')
                    for name, query in {
                        'task': 'SELECT task_ref,task_epoch,status,explicit_end_epoch FROM task',
                        'run': 'SELECT kind,worker_position,status,desired_state FROM run',
                        'watch': 'SELECT state,failure_code,enabled FROM github_watch',
                        'binding': 'SELECT rule_mode,rule_event_kind,enabled,last_sequence,generation FROM github_task_binding',
                        'observation': 'SELECT sequence,kind,binding_generation FROM github_observation',
                        'rule': 'SELECT event_kind,mode,matched FROM github_rule_decision',
                        'followup': 'SELECT state FROM github_task_followup',
                    }.items():
                        diagnostic[name] = db.execute(query).fetchall()
            raise RuntimeError('actual archived source evidence: '+json.dumps(diagnostic)) from error
        finally:
            h.cleanup()


def run(binary, evidence, prior28=None):
    original = hashlib.sha256(binary.read_bytes()).hexdigest()
    report = dict(checks=[], actual_prior28='not_run', live_service='not_run', cleanup=False)
    with tempfile.TemporaryDirectory(prefix='steward-github-default40-') as tmp:
        root = Path(tmp)
        if sys.platform != 'linux':
            require(prior28 is None, 'archived28 stock HTTPS proof must use the isolated Linux carrier')
            fresh = root/'fresh'; fresh.mkdir()
            report['checks'].append(portable_startup(binary, fresh))
            report['stock_https_source'] = 'not_run_on_macos_native_trust;separate Linux stock proof required'
        else:
            with source_peer(root) as (proxy, cert, source):
                fresh = root/'fresh'; fresh.mkdir()
                result, graph, policy = fresh_poll(binary, fresh, proxy, cert, source)
                report['checks'].append(result)
                report['stock_https_source'] = 'passed_with_verified_owned_certificate;no_product_endpoint_override'
                evidence.write_text(json.dumps(report, indent=2)+'\n')
                if prior28 is not None:
                    archive = root/'archive'; archive.mkdir()
                    report['actual_prior28'] = archived_source(binary, prior28, archive, proxy, cert, source, graph, policy)
                require(not source['failures'], 'owned fixed-origin TLS source protocol changed')
                report['source_requests'] = list(source['calls'])
                report['certificate_sha256'] = hashlib.sha256(cert.read_bytes()).hexdigest()
        if sys.platform != 'linux':
            report['checks'].append(actual_app(binary, evidence))
    require(hashlib.sha256(binary.read_bytes()).hexdigest() == original, 'actual binary changed during source proof')
    report.update(binary_sha256=original, cleanup=True)
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--prior28-binary', type=Path)
    args = parser.parse_args()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    report = run_cases([None], lambda _: run(args.binary.resolve(), args.evidence,
        args.prior28_binary.resolve() if args.prior28_binary else None), jobs=1)[0]
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps(dict(checks=len(report['checks']), actual_prior28=report['actual_prior28'] != 'not_run',
                          cleanup=report['cleanup'])))
