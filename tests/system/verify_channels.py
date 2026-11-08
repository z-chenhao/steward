#!/usr/bin/env python3
"""Launch/Doctor/Drive/Evidence/Cleanup for Web and Feishu through actual HTTP.

Uses the maintained MainHarness, production Actor, Tool/Approval/Delivery stores,
and independent Feishu HTTP Fake. All processes are foreground-owned and joined.
"""
import argparse
import base64
import socketserver
import shutil
import threading
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import selectors
from contextlib import closing
import sqlite3
import subprocess
import tempfile
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen

from feishu_sandbox import Sandbox
from event_broker import Broker
from fixture_pool import run_cases
from verify_main import MainHarness, require
from terminal_driver import Terminal

ROOT = Path(__file__).resolve().parents[2]
TOKEN = 'sandbox-web-owner-token-at-least-24-bytes'


class ChannelHarness(MainHarness):
    def __init__(self, binary, root, platform, fixture='main'):
        self.production_events = False
        self.platform = platform
        super().__init__(binary, root, fixture)
        # Deterministic channel fixtures must not inherit a deployment's live
        # app broker credential. Tests opt into their own explicit protocol peer.
        self.env.pop('COMPOSIO_API_KEY', None)
        self.config = root / 'config.yaml'
        self.config.write_text('version: 1\nweb:\n  listen: "127.0.0.1:0"\n  token_ref: env:STEWARD_WEB_TEST_TOKEN\nfeishu:\n  app_id: sandbox-app\n  tenant_key: sandbox-tenant\n  verification_token_ref: env:STEWARD_FEISHU_VERIFY\n  encrypt_key_ref: env:STEWARD_FEISHU_ENCRYPT\n  access_token_ref: env:STEWARD_FEISHU_ACCESS\n  api_base: ' + platform.url + '\n')
        if not fixture:
            from codemode_peer import runtime_yaml
            self.config.write_text(self.config.read_text() + runtime_yaml())
        self.env.update(STEWARD_WEB_TEST_TOKEN=TOKEN, STEWARD_FEISHU_VERIFY='sandbox-verify-token',
                        STEWARD_FEISHU_ENCRYPT='sandbox-encrypt-key', STEWARD_FEISHU_ACCESS='sandbox-access-token')
        self.bind = self.command('channel','manage',text=json.dumps(dict(op='bind',channel='feishu',subject='sandbox-tenant:sandbox-app:owner',destination='bound-chat',enabled=True,version=0)))
        self.process = None

    def args(self, *args):
        return [str(self.binary), '-config', str(self.config), '-test-bridge', self.fixture, *(['-test-events'] if getattr(self, 'production_events', False) else []), *(['-test-automation-now',self.now] if hasattr(self,'now') else []), *args]

    def start(self, boundary=None):
        args = ('web','serve') if boundary is None else ('-test-main-hold',boundary,'web','serve')
        start=self.processes.start if self.processes is not None else subprocess.Popen
        self.process = start(self.args(*args), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                        text=True, cwd=self.root, env=self.env)
        self.children.append(self.process)
        with selectors.DefaultSelector() as selector:
            selector.register(self.process.stdout,selectors.EVENT_READ)
            require(selector.select(15),'Web launch did not report listener')
        line=self.process.stdout.readline()
        require(line, 'Web launch exited: '+(self.process.stderr.read() if not line else ''))
        self.url=json.loads(line)['url']
        return self.process

    def stop(self, kill=False):
        if self.process and self.process.poll() is None:
            self.process.kill() if kill else self.process.terminate()
            _, err=self.process.communicate(timeout=15)
            require(kill or self.process.returncode==0, 'Web shutdown failed: '+err)
        self.process=None

    def http(self, path, data=None, method=None, auth=True, headers=None, expected=200):
        headers=dict(headers or {})
        if auth: headers['Authorization']='Bearer '+TOKEN
        if isinstance(data,dict):
            data=json.dumps(data).encode();headers['Content-Type']='application/json'
        req=Request(self.url+path,data=data,method=method,headers=headers)
        try:
            with urlopen(req,timeout=20) as response: status,raw=response.status,response.read()
        except HTTPError as error: status,raw=error.code,error.read()
        require(status==expected,f'{path}: expected {expected}, got {status}: {raw[:200]!r}')
        try: return json.loads(raw)
        except ValueError: return raw.decode()

    def callback(self, source, text, event=None, sender='owner', destination='bound-chat', expected=200, signature=True, kind='text'):
        self.platform.register_source(source, destination)
        content=dict(text=text) if kind=='text' else dict(file_key='file-key',file_name='received.txt')
        value=dict(schema='2.0',header=dict(event_id=event or source,event_type='im.message.receive_v1',token='sandbox-verify-token',app_id='sandbox-app',tenant_key='sandbox-tenant'),event=dict(sender=dict(sender_type='user',sender_id=dict(open_id=sender)),message=dict(message_id=source,chat_id=destination,chat_type='p2p',message_type=kind,content=json.dumps(content))))
        return self.signed_callback(value, expected, signature)

    def signed_callback(self, value, expected=200, signature=True):
        raw = json.dumps(value, separators=(',', ':')).encode()
        timestamp, nonce = str(int(time.time())), 'sandbox-nonce'
        digest = hashlib.sha256(timestamp.encode() + nonce.encode() + b'sandbox-encrypt-key' + raw).hexdigest()
        return self.http('/feishu/callback', raw, auth=False, headers={
            'Content-Type': 'application/json', 'X-Lark-Request-Timestamp': timestamp,
            'X-Lark-Request-Nonce': nonce, 'X-Lark-Signature': digest if signature else 'forged',
        }, expected=expected)

    def card_feedback(self, ref, card_id, answer, event, *, chat='bound-chat', owner='owner',
                      name='steward_task_question_submit', signature=True):
        return self.signed_callback(dict(
            schema='2.0',
            header=dict(event_id=event, event_type='card.action.trigger', token='sandbox-verify-token',
                        app_id='sandbox-app', tenant_key='sandbox-tenant'),
            event=dict(host='im_message', operator=dict(tenant_key='sandbox-tenant', open_id=owner),
                       context=dict(open_message_id=card_id, open_chat_id=chat),
                       action=dict(tag='button', name=name, value=dict(feedback_ref=ref),
                                   form_value=dict(answer=answer)))),
            signature=signature, expected=200 if signature else 401)

    def rows(self):
        with closing(sqlite3.connect(self.root/'data/state.db')) as db, db:
            db.row_factory=sqlite3.Row
            # Observe deliveries and Runs in one snapshot; a reply may be
            # enqueued between these reads while its Run becomes terminal.
            db.execute('BEGIN')
            return {name:[dict(r) for r in db.execute(query)] for name,query in dict(
                inputs='SELECT request_key,message_id,run_id FROM main_input ORDER BY sequence',
                callbacks='SELECT * FROM channel_callback ORDER BY event_id',
                deliveries='SELECT delivery_id,message_id,channel,destination_ref,status,attempt_count,version FROM delivery_request ORDER BY created_at,delivery_id',
                receipts='SELECT * FROM channel_receipt ORDER BY delivery_id,part',
                approvals='SELECT approval_id,frozen_arguments_hash,status FROM approval ORDER BY created_at',
                tools="SELECT tool_name,arguments_json,status,attempt_count,json_extract(error_json,'$.code') AS error_code FROM tool_invocation ORDER BY created_at",
                runs='SELECT run_id,kind,worker_position,status FROM run ORDER BY created_at',
                resources='SELECT * FROM channel_resource',
                projections='SELECT * FROM delivery_projection').items()}

    def wait(self,predicate,description):
        try:
            return self.wait_state(predicate,description,lambda _:self.rows(),timeout=20)
        except RuntimeError as error:
            # Bounded fixture state, without arguments, message bodies or credentials.
            state=self.rows()
            diagnostic={name:[{key:row[key] for key in keys} for row in state[name][-12:]]
                        for name,keys in dict(runs=['kind','status'], tools=['tool_name','status','attempt_count','error_code'],
                                              approvals=['status'], deliveries=['channel','status']).items()}
            raise RuntimeError(str(error)+'; fixture state: '+json.dumps(diagnostic)) from error

    def browser(self,evidence,phase):
        env=self.env.copy()
        executable=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
        if executable.exists():env['STEWARD_BROWSER_EXECUTABLE']=str(executable)
        run=self.processes.run if self.processes is not None else subprocess.run
        result=run(['node',str(ROOT/'tests/system/channels_browser.mjs'),self.url,str(evidence),phase],env=env,capture_output=True,text=True,timeout=45)
        require(result.returncode==0,'browser drive failed: '+result.stderr)


def preference_settings(binary, evidence, report):
    with tempfile.TemporaryDirectory(prefix='steward-preferences-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform)
        try:
            h.setup()
            h.start()
            output = evidence.parent / 'preferences-ui'
            h.browser(output, 'preferences')
            report['scenarios']['preferences'] = json.loads((output / 'preferences-browser.json').read_text())
            report['checks'].append('Preferences: bilingual grouped dropdown, keyboard/touch and help, single-flight autosave, rejection/retry, unknown-result and cookie readback, timeout, restoration, native fallbacks and neighboring settings')
        finally:
            h.cleanup()


def feishu_auth_lifecycle(binary, report):
    """Real CLI/Actor/Delivery against the HTTP peer; no browser or real account."""
    with tempfile.TemporaryDirectory(prefix='steward-feishu-auth-') as temp, Sandbox(managed_auth=True, token_ttl=2) as platform:
        h = ChannelHarness(binary, Path(temp), platform)
        try:
            h.config.write_text(h.config.read_text().replace('access_token_ref: env:STEWARD_FEISHU_ACCESS', 'app_secret_ref: env:STEWARD_FEISHU_APP_SECRET'))
            h.env['STEWARD_FEISHU_APP_SECRET'] = 'sandbox-app-secret'
            status = h.command('channel', 'status')
            require(status['mode'] == 'app_credentials' and status['state'] == 'unchecked' and platform.auth_requests == 0, 'status unexpectedly acquired credentials')
            checked = h.command('channel', 'check')
            require(checked['state'] == 'ready' and checked['expires_at'] and platform.auth_requests == 1, 'CLI check did not authenticate the bot')
            h.setup()
            h.start()
            h.callback('managed-first', 'First managed credential message.')
            h.wait(lambda s: len(s['deliveries']) == 1 and all(d['status'] == 'sent' for d in s['deliveries']), 'first managed delivery')
            with platform.lock:
                require(platform.auth_requests == 2, 'process cache did not acquire once')
                platform.current_token = None
            h.callback('managed-revoked', 'Continue after an explicitly rejected token.')
            h.wait(lambda s: len(s['deliveries']) == 2 and all(d['status'] == 'sent' for d in s['deliveries']), 'token rejection recovered through the durable retry')
            with platform.lock:
                require(platform.auth_requests == 3 and platform.auth_rejections == 1 and len(platform.send_attempts) == 2, 'rejection did not refresh safely')
                expiry = platform.token_expiry
            while time.monotonic() < expiry + .05:
                time.sleep(.025)
            h.callback('managed-expired', 'Continue after the cached credential expires.')
            h.wait(lambda s: len(s['deliveries']) == 3 and all(d['status'] == 'sent' for d in s['deliveries']), 'proactive refresh')
            with platform.lock:
                require(platform.auth_requests == 4 and platform.auth_rejections == 1 and len(platform.send_attempts) == 3, 'expired token reached the transport or message repeated')
            h.stop()
            h.env['STEWARD_FEISHU_APP_SECRET'] = 'wrong-private-app-secret'
            rejected = h.command('channel', 'check')
            require(rejected['state'] == 'credentials_rejected' and rejected['action'], 'bad app secret has no sanitized diagnosis')
            h.env['STEWARD_FEISHU_APP_SECRET'] = ''
            missing = h.command('channel', 'check')
            require(missing['state'] == 'credentials_unavailable' and missing['action'], 'missing credential has no recovery action')
            h.env['STEWARD_FEISHU_APP_SECRET'] = 'sandbox-app-secret'
            h.start()
            with platform.lock:
                platform.drop_response = True
            h.callback('managed-uncertain', 'An accepted send loses its response.')
            h.wait(lambda s: any(d['status'] == 'unknown' for d in s['deliveries']), 'uncertain managed delivery')
            h.stop()
            with platform.lock:
                attempts, issues = len(platform.send_attempts), platform.auth_requests
                require(attempts == 4, 'unexpected accepted transport count')
                platform.drop_response = False
            h.command('delivery', 'work')
            rows = h.rows()
            require(sum(d['status'] == 'unknown' for d in rows['deliveries']) == 1, 'restart cleared uncertainty without evidence')
            with platform.lock:
                require(len(platform.send_attempts) == attempts and platform.auth_requests == issues, 'recovery acquired credentials or resent an unknown message')
            h.env['STEWARD_FEISHU_APP_SECRET'] = 'wrong-private-app-secret'
            h.start()
            h.callback('managed-invalid-secret', 'This notification must fail before transport.')
            h.wait(lambda s: any(d['status'] == 'failed' for d in s['deliveries']), 'permanent credential failure has a durable diagnostic')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                require(db.execute("SELECT last_error FROM delivery_request WHERE status='failed'").fetchone() == ('credentials_rejected',), 'credential failure code was not preserved')
            h.stop()
            with platform.lock:
                require(len(platform.send_attempts) == attempts, 'invalid app secret reached the message endpoint')
            h.env['STEWARD_FEISHU_APP_SECRET'] = 'sandbox-app-secret'
            binding = next(b for b in h.command('channel', 'manage', text='{"op":"list"}') if b['channel'] == 'feishu')
            h.command('channel', 'manage', text=json.dumps(dict(op='bind', channel='feishu', subject=binding['subject'], destination=binding['destination'], enabled=False, version=binding['version'])))
            h.start()
            h.callback('managed-revoked-binding', 'This revoked identity cannot submit.', expected=403)
            h.stop()
            secrets = [b'sandbox-app-secret', b'wrong-private-app-secret', b'sandbox-managed-token-']
            for path in (h.root / 'data').rglob('*'):
                if path.is_file():
                    require(all(secret not in path.read_bytes() for secret in secrets), 'channel credentials leaked into durable state')
            require(all(secret.decode() not in json.dumps([checked, rejected, missing]) for secret in secrets), 'channel diagnostics leaked credentials')
            report['scenarios']['feishu-auth'] = dict(mode='app_credentials', initial_check='ready', accepted_messages=attempts, explicit_rejections_recovered=1, proactive_refresh=True, rejected_secret=rejected['state'], missing_secret=missing['state'], unknown_after_restart=1, unknown_not_resent=True, revoked_binding_rejected=True, invalid_credentials_delivery='credentials_rejected', credential_scan='clean')
            report['checks'].append('Feishu CLI/Actor/Delivery: managed tokens, rejection retry, expiry refresh, safe diagnostics, revoked binding and query-only unknown recovery; no real external account')
        finally:
            h.cleanup()


def attention_workspace(binary, evidence, report):
    """Actual Main/Task feedback -> authenticated Web -> persisted route/dedup."""
    with tempfile.TemporaryDirectory(prefix='steward-web-attention-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, fixture='tasks')
        try:
            # This channel workload retains six Tasks/feedback turns. Compaction
            # has separate gates; use the same explicit window as Feishu reviews.
            h.setup(metadata=dict(name='Web attention workload', provider_kind='openai_compatible',
                                  endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions',
                                  model_id='fixture-model', context_window=32768, max_output_tokens=1024,
                                  compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            h.send('question-task', 'Ongoing: Prepare the quarterly report'); h.command('chat', 'work')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                ref = db.execute('SELECT task_ref FROM task').fetchone()[0]
            h.send('question', f'Question: {ref} | Which quarter should the report cover?'); h.command('chat', 'work')
            h.send('review-task', 'Delegate: Review the product research'); h.command('chat', 'work')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                review_ref = db.execute('SELECT task_ref FROM task WHERE task_ref!=?', (ref,)).fetchone()[0]
            artifact = h.command('artifact', 'save', 'research-notes.txt', 'text/plain', text='Research revision A: sources and findings.')
            parts = [dict(type='text', text=f'Review: {review_ref} | {artifact["artifact_ref"]} | Is this research ready to use?'), dict(type='file', artifact_ref=artifact['artifact_ref'])]
            h.command('chat', 'send-parts', 'review', text=json.dumps(parts)); h.command('chat', 'work')
            h.send('failed-task', 'Delegate: Fail worker: Examine unavailable source'); h.command('chat', 'work')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                failed_ref=db.execute("SELECT task_ref FROM task WHERE anchor_task LIKE 'Fail worker:%'").fetchone()[0]
            h.send('failed-question',f'Question: {failed_ref} | Which source should this work use?');h.command('chat','work')
            initial_delivery_count=len(h.rows()['deliveries'])
            h.start(); h.browser(evidence.parent/'attention-ui', 'attention')
            # Each reply has its normal Main/follow-up delivery. The retried
            # failed Root also emits one Host-owned lifecycle notice.
            state = h.wait(lambda state: len([run for run in state['runs'] if run['kind']=='worker']) == 6 and all(run['status'] in ('completed','failed','cancelled') for run in state['runs']) and len(state['deliveries'])==initial_delivery_count+7 and all(delivery['status']=='sent' for delivery in state['deliveries']), 'three feedback continuations, Main follow-ups and the failed-attempt notice')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.row_factory=sqlite3.Row; db.execute('BEGIN')
                rows=[dict(row) for row in db.execute('SELECT f.feedback_ref,f.kind,f.status,f.response_message_id,t.task_ref FROM task_feedback f JOIN task t USING(task_id) ORDER BY f.rowid')]
                require(len(rows)==3 and all(row['status']=='answered' for row in rows), 'feedback was not answered once')
                require(db.execute("SELECT count(*) FROM task_instruction").fetchone()[0]==3, 'feedback/stale retry duplicated continuation')
                require(db.execute("SELECT count(*) FROM approval").fetchone()[0]==0, 'review became action approval')
                failures={"failure:"+run['run_id'] for run in state['runs'] if run['kind']=='worker' and run['status']=='failed'}
                notices={row[0] for row in db.execute("SELECT message_id FROM notification_intent WHERE intent='failure'")}
                require(notices==failures, 'failed Root is missing its unique Host notice')
                require(all(db.execute('SELECT count(*) FROM delivery_request WHERE message_id=?',(message,)).fetchone()[0]==1 for message in notices), 'failure notice duplicated its source-bound delivery')
                for row in rows:
                    message=db.execute("SELECT json_extract(message_json,'$.source') FROM main_input WHERE message_id=?",(row['response_message_id'],)).fetchone()[0]
                    require(message=='web' and db.execute('SELECT count(*) FROM message_route WHERE message_id=?',(row['response_message_id'],)).fetchone()[0]==1, 'Web feedback lost authenticated route')
            for row in rows:
                answer=('Use Q3.' if row['task_ref']==ref else 'External response.') if row['kind']=='input' else 'Clarify the introduction.'
                receipt=h.command('task','respond',row['feedback_ref'],text=answer)
                require(receipt['message_id']==row['response_message_id'], 'CLI retry did not retain Web receipt')
            require(h.rows()==state, 'cross-CLI retries changed work or delivery')
            report['scenarios']['attention']=dict(feedbacks=rows,state=state,artifact=artifact)
            report['checks'].append('Web attention: current question, completed-Task exact artifact review, failed Task, honest count; bound Web replies, keyboard/Chinese/mobile, lost acknowledgement and stale duplicate preserve drafts; cross-CLI retries retain route and one continuation')
        finally: h.cleanup()


def feishu_topic_delivery(binary, report):
    """Signed source -> real Actor -> frozen reply -> actual HTTP peer."""
    from urllib.parse import urlsplit
    with tempfile.TemporaryDirectory(prefix='steward-feishu-topics-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, 'tasks')
        try:
            h.config.write_text(h.config.read_text() + '  workspace_url: https://owner-workspace.example.test\n')
            h.setup(); h.start()
            h.callback('task-origin', 'Delegate: Review the bounded repository result.')
            state = h.wait(lambda s: len(s['deliveries']) == 2 and all(d['status'] == 'sent' for d in s['deliveries']), 'synchronous acceptance and asynchronous original-topic delivery')
            messages = platform.evidence()['messages']
            require(len(messages) == 2 and all(m.get('reply_to') == 'task-origin' for m in messages), 'background result lost its original message')
            for message in messages:
                card = json.loads(message['content'])
                link = card['elements'][1]['actions'][0]['url']
                parsed = urlsplit(link)
                require(parsed.netloc == 'owner-workspace.example.test' and not parsed.username and parsed.fragment.startswith('message-'), 'result link escaped its configured workspace')
                page = h.http(parsed.path + '?' + parsed.query)
                require('id="' + parsed.fragment + '"' in page, 'result link cannot locate the full semantic reply')
            h.stop(kill=True); h.start()
            h.callback('task-origin', 'Delegate: Review the bounded repository result.', event='task-origin-retry')
            require(len(h.rows()['deliveries']) == 2 and len(platform.evidence()['messages']) == 2, 'recovery created a duplicate notification intent')
            h.stop()
            # Explicit owner preference wins over origin; no automatic extra IM.
            preference = h.command('channel', 'manage', text='{"op":"preference"}')
            h.command('channel', 'manage', text=json.dumps(dict(op='prefer', channel='tui', version=preference['version'])))
            h.start()
            h.callback('task-explicit-tui', 'Delegate: Keep this asynchronous result in the terminal.')
            state = h.wait(lambda s: len(s['deliveries']) == 4 and all(d['status'] == 'sent' for d in s['deliveries']), 'explicit preference for background result')
            require(sum(d['channel'] == 'tui' for d in state['deliveries']) == 1, 'background result ignored explicit notification preference')
            require(len(platform.evidence()['messages']) == 3, 'one Task result created multiple channel notifications')
            h.stop()
            # A source reply accepted remotely but losing its response stays
            # query-only across process/credential/configuration changes.
            h.start()
            with platform.lock:
                platform.drop_response = True
            h.callback('topic-uncertain', 'A bounded result whose reply acknowledgement is lost.')
            h.wait(lambda s: any(d['status'] == 'unknown' for d in s['deliveries']), 'source reply uncertain outcome')
            h.stop(kill=True)
            attempts = len(platform.evidence()['send_attempts'])
            h.config.write_text(h.config.read_text().replace('https://owner-workspace.example.test', 'https://changed-workspace.example.test'))
            with platform.lock:
                platform.drop_response = False
            h.command('delivery', 'work')
            require(len(platform.evidence()['send_attempts']) == attempts, 'unknown source reply was resent')
            require(any(d['status'] == 'unknown' for d in h.rows()['deliveries']), 'unknown reply was erased without authoritative evidence')
            report['scenarios']['feishu-topic-delivery'] = dict(state=h.rows(), platform=platform.evidence(), original_source='task-origin', full_result_links=True, explicit_preference=True, query_only_unknown=True)
            report['checks'].append('Feishu original-message synchronous/Task follow-up replies, trusted full-result links, callback/restart notification dedup, explicit preference and query-only unknown recovery')
        finally:
            h.cleanup()
    with tempfile.TemporaryDirectory(prefix='steward-feishu-linked-file-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, 'artifacts')
        try:
            h.config.write_text(h.config.read_text() + '  workspace_url: http://127.0.0.1:8787\n')
            h.setup(); h.start()
            content = 'Complete source evidence with citations and the final checklist.\n'
            h.callback('linked-artifact', json.dumps(dict(name='evidence.txt', mediaType='text/plain', text=content)))
            h.wait(lambda s: len(s['deliveries']) == 1 and s['deliveries'][0]['status'] == 'sent', 'linked artifact notification')
            remote = platform.evidence()
            require(len(remote['messages']) == 1 and not remote['uploads'], 'linked details were mechanically copied to IM')
            conversation = h.http('/api/conversation')
            refs = [p['artifact_ref'] for m in conversation if m['role'] == 'assistant' for p in m['content'] if p.get('artifact_ref')]
            require(len(refs) == 1 and h.http('/artifact?' + urlencode(dict(ref=refs[0]))) == content, 'linked complete artifact was lost')
            report['scenarios']['feishu-linked-artifact'] = dict(platform=remote, artifact_ref=refs[0], full_content_sha256=hashlib.sha256(content.encode()).hexdigest())
            report['checks'].append('Linked IM result is one notice without repeated file upload; complete authenticated Artifact remains available in the workspace')
            h.stop()
        finally:
            h.cleanup()


def feishu_task_questions(binary, report):
    """Signed Card 2.0 input, real reply receipt, and the existing Main path."""
    with tempfile.TemporaryDirectory(prefix='steward-feishu-questions-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, 'tasks')
        state = h.root / 'data/state.db'

        def rows(query, args=()):
            with closing(sqlite3.connect(state)) as db, db:
                db.row_factory = sqlite3.Row
                return [dict(row) for row in db.execute(query, args)]

        def wait(read, predicate, description):
            return h.wait_state(predicate, description, lambda _: read(), timeout=20)

        def question_rows():
            return rows("""SELECT f.feedback_ref, f.prompt, f.status, f.response_message_id,
                q.binding_version, d.delivery_id, d.version AS delivery_version, d.status AS delivery_status, c.remote_id
                FROM task_feedback f LEFT JOIN feishu_task_question q USING(feedback_ref)
                LEFT JOIN delivery_request d ON d.message_id=q.message_id
                LEFT JOIN channel_receipt c ON c.delivery_id=d.delivery_id AND c.part=0 ORDER BY f.rowid""")

        def new_question(source, prompt, status='sent'):
            h.callback(source, f'Question: {ref} | {prompt}')
            def selected():
                return next((q for q in question_rows() if q['prompt'] == prompt), {})
            return wait(selected, lambda q: q.get('delivery_status') == status, f'{source} card {status}')

        def action(q, card_id, answer, event, **kwargs):
            return h.card_feedback(q['feedback_ref'], card_id, answer, event, **kwargs)

        def web_retry(q, answer):
            return h.http('/task/feedback', urlencode(dict(feedback_ref=q['feedback_ref'], answer=answer,
                          source_id='web-' + q['feedback_ref'], return_to='/tasks')).encode(),
                          headers={'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json'})

        try:
            h.setup(metadata=dict(name='Feishu question workload', provider_kind='openai_compatible',
                                  endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions',
                                  model_id='fixture-model', context_window=32768, max_output_tokens=1024,
                                  compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            h.start()
            h.callback('om_task_origin', 'Ongoing: Prepare the quarterly report')
            h.wait(lambda s: len(s['deliveries']) == 2 and all(d['status'] == 'sent' for d in s['deliveries']), 'original Task delivered')
            ref = rows('SELECT task_ref FROM task')[0]['task_ref']
            q = new_question('om_question_source', 'Which quarter should the report cover?')
            card_id = q['remote_id']
            remote = next(m for m in platform.evidence()['messages'] if 'remote-' + m['uuid'] == card_id)
            card = json.loads(remote['content'])
            text, form = card['body']['elements']
            submit = form['elements'][1]
            require(card['schema'] == '2.0' and remote['reply_to'] == 'om_question_source' and
                    text['text']['tag'] == 'plain_text' and q['prompt'] in text['text']['content'] and
                    form['tag'] == 'form' and submit['form_action_type'] == 'submit' and
                    submit['behaviors'][0]['value'] == dict(feedback_ref=q['feedback_ref']),
                    'card does not bind the exact question and original reply route')

            answer = 'Cover Q3.'
            action(q, card_id, answer, 'unsigned', signature=False)
            invalid = [('wrong-owner', dict(owner='different-owner')), ('wrong-chat', dict(chat='other-chat')),
                       ('approval-action', dict(name='approve'))]
            for key, kwargs in invalid:
                require(action(q, card_id, answer, key, **kwargs)['toast']['type'] == 'error', key + ' accepted')
            require(action(q, 'om_forwarded_copy', answer, 'forwarded')['toast']['type'] == 'error', 'copied card accepted')
            require(rows("SELECT count(*) AS n FROM main_input")[0]['n'] == 2, 'invalid callbacks admitted Main input')
            require(action(q, card_id, answer, 'valid-answer')['toast']['type'] == 'success', 'valid answer rejected')
            accepted = rows('SELECT response_message_id FROM task_feedback WHERE feedback_ref=?', (q['feedback_ref'],))[0]['response_message_id']
            require(accepted, 'success toast preceded durable acceptance')
            h.wait(lambda s: len(s['runs']) >= 6 and all(r['status'] == 'completed' for r in s['runs']) and
                   len(s['deliveries']) >= 6 and all(d['status'] == 'sent' for d in s['deliveries']), 'Main continuation and delivery')
            require(rows('SELECT count(*) AS n FROM task_instruction')[0]['n'] == 1 and
                    rows('SELECT task_ref FROM task') == [dict(task_ref=ref)] and not h.rows()['approvals'],
                    'answer did not continue one existing Task independently of approvals')
            require(rows("""SELECT json_extract(i.message_json,'$.source') AS source, m.reply_source_id
                         FROM main_input i JOIN message_route m USING(message_id) WHERE message_id=?""", (accepted,)) ==
                    [dict(source='feishu', reply_source_id=card_id)], 'answer did not retain the real card route')
            outbound = platform.evidence()['messages']
            require(sum(m.get('reply_to') == card_id for m in outbound) == 1 and
                    sum(m.get('reply_to') == 'om_task_origin' for m in outbound) == 3,
                    'answer reply or asynchronous continuation lost its Feishu topic')
            for key in ('valid-answer', 'answer-new-event'):
                require(action(q, card_id, answer, key)['toast']['type'] == 'success', 'same card answer did not replay')
            require(h.command('task', 'respond', q['feedback_ref'], text=answer)['message_id'] == accepted and
                    web_retry(q, answer)['message_id'] == accepted, 'cross-channel retry changed the original receipt')
            require(action(q, card_id, 'Different answer', 'conflict')['toast'] ==
                    dict(type='error', content='This request changed or already has a different response. Check Tasks.'),
                    'different answer was accepted or reported as uncertain')
            require(rows('SELECT count(*) AS n FROM main_input')[0]['n'] == 3 and
                    len(platform.evidence()['messages']) == len(outbound), 'retry created extra input or delivery')

            # A fresh question cannot inherit another card's receipt.
            newer = new_question('om_new_question', 'Which region should the report cover?')
            require(action(newer, card_id, 'EU', 'substitute')['toast']['type'] == 'error', 'another card authorized the question')
            require(action(q, card_id, answer, 'old-question')['toast']['type'] == 'error', 'superseded card accepted')
            def bind(enabled, version):
                return h.command('channel', 'manage', text=json.dumps(dict(op='bind', channel='feishu',
                                 subject='sandbox-tenant:sandbox-app:owner', destination='bound-chat', enabled=enabled, version=version)))
            bind(False, 1)
            bind(True, 2)
            require(action(newer, newer['remote_id'], 'EU', 'old-binding')['toast']['type'] == 'error', 're-enabled binding revived old card')

            preference = h.command('channel', 'manage', text='{"op":"preference"}')
            h.command('channel', 'manage', text=json.dumps(dict(op='prefer', channel='tui', version=preference['version'])))
            h.callback('om_tui_preference', f'Question: {ref} | Prefer the terminal.')
            local = wait(question_rows, lambda qs: qs[-1]['prompt'] == 'Prefer the terminal.', 'local question')[-1]
            require(local['binding_version'] is None, 'explicit preference created a Feishu card')
            preference = h.command('channel', 'manage', text='{"op":"preference"}')
            h.command('channel', 'manage', text=json.dumps(dict(op='prefer', channel='auto', version=preference['version'])))
            h.stop()
            h.send('cli-question', f'Question: {ref} | This is a local question.')
            h.command('chat', 'work')
            require(question_rows()[-1]['binding_version'] is None, 'CLI question fell back to Feishu')
            h.start()

            with platform.lock:
                platform.drop_response = True
            uncertain = new_question('om_uncertain_question', 'Which notes should I use?', 'unknown')
            require(uncertain['remote_id'] is None, 'ambiguous send acquired a receipt')
            unknown_card = next(m for m in platform.evidence()['messages'] if m.get('reply_to') == 'om_uncertain_question'
                                and json.loads(m['content']).get('schema') == '2.0')
            require(action(uncertain, 'remote-' + unknown_card['uuid'], 'Use these notes', 'unconfirmed-card')['toast']['type'] == 'error',
                    'card without authoritative receipt accepted')
            h.stop(kill=True)
            attempts = sum(m['uuid'] == unknown_card['uuid'] for m in platform.evidence()['send_attempts'])
            with platform.lock:
                platform.drop_response = False
            bind(False, 3)
            wake_at = rows('SELECT next_wake_at FROM delivery_outbox WHERE delivery_id=?', (uncertain['delivery_id'],))[0]['next_wake_at']
            time.sleep(max(0, (wake_at - time.time_ns() // 1000) / 1_000_000) + .02)
            h.command('delivery', 'work')
            recovered = next(q for q in question_rows() if q['feedback_ref'] == uncertain['feedback_ref'])
            require(recovered['delivery_version'] > uncertain['delivery_version'] and recovered['delivery_status'] == 'unknown' and
                    sum(m['uuid'] == unknown_card['uuid'] for m in platform.evidence()['send_attempts']) == attempts == 1,
                    'recovery did not query, repeated the send or erased uncertainty after revocation')
            bind(True, 4)

            h.start()
            stopped = new_question('om_stop_question', 'Should the report include a summary?')
            h.callback('om_pause', 'Pause: Prepare the quarterly report')
            wait(lambda: rows('SELECT status FROM task'), lambda ts: ts[0]['status'] == 'paused', 'Task paused')
            require(action(stopped, stopped['remote_id'], 'Yes', 'stopped-card')['toast']['type'] == 'error', 'paused card accepted')
            require(rows('SELECT count(*) AS n FROM task_instruction')[0]['n'] == 1 and
                    rows("SELECT count(*) AS n FROM task_feedback WHERE status='answered'")[0]['n'] == 1,
                    'stale or ambiguous callback created work')
            report['scenarios']['feishu-task-questions'] = dict(
                feedback_ref=q['feedback_ref'], card_id=card_id, accepted_message_id=accepted,
                original_question_topic=remote['reply_to'], unknown_card_attempts=attempts,
                answered=1, continuations=1, final_task_status='paused')
            report['checks'].append('Feishu Task question Card 2.0, signed owner/context/receipt checks, three-channel dedup, original reply routing, stale/reenrolled/ambiguous rejection and query-only restart')
        finally:
            h.cleanup()


def feishu_artifact_reviews(binary, report):
    """Authenticated Workspace link and the exact review revision round trip."""
    from urllib.parse import urlsplit
    with tempfile.TemporaryDirectory(prefix='steward-feishu-reviews-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, 'tasks')

        def rows(query, args=()):
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.row_factory = sqlite3.Row
                return [dict(row) for row in db.execute(query, args)]

        def review(prompt):
            found = rows("""SELECT f.feedback_ref, f.artifact_refs, f.response_message_id, q.review_url, c.remote_id
                FROM task_feedback f LEFT JOIN feishu_task_question q USING(feedback_ref)
                LEFT JOIN delivery_request d ON d.message_id=q.message_id
                LEFT JOIN channel_receipt c ON c.delivery_id=d.delivery_id AND c.part=0 WHERE f.prompt=?""", (prompt,))
            return found[0] if found else {}

        def wait_review(prompt, predicate):
            return h.wait_state(predicate, 'saved review and expected delivery state', lambda _: review(prompt), timeout=20)

        try:
            h.setup(metadata=dict(name='Feishu review workload', provider_kind='openai_compatible',
                                  endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions',
                                  model_id='fixture-model', context_window=32768, max_output_tokens=1024,
                                  compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            h.start()
            h.callback('om_review_task', 'Ongoing: Prepare the review report')
            h.wait(lambda s: len(s['deliveries']) == 2 and all(d['status'] == 'sent' for d in s['deliveries']), 'review Task delivered')
            task_ref = rows('SELECT task_ref FROM task')[0]['task_ref']
            h.stop()
            content = 'Private immutable revision A.'
            artifact = h.command('artifact', 'save', 'revision-a.txt', 'text/plain', text=content)['artifact_ref']
            other = h.command('artifact', 'save', 'other.txt', 'text/plain', text='Unrelated revision B.')['artifact_ref']
            parts = [dict(type='text', text='Keep the two distinct revisions.'), dict(type='file', artifact_ref=artifact),
                     dict(type='file', artifact_ref=other)]
            h.command('chat', 'send-parts', 'attach-review-inputs', text=json.dumps(parts))
            h.command('chat', 'work')
            h.start()
            local_prompt = 'Review without a workspace.'
            h.callback('om_review_local', f'Review: {task_ref} | {artifact} | {local_prompt}')
            local = wait_review(local_prompt, bool)
            require(local['review_url'] is None, 'review with absent workspace got a card intent')
            h.stop()

            workspace = 'https://owner-workspace.example.test'
            h.config.write_text(h.config.read_text() + f'  workspace_url: {workspace}\n')
            h.start()
            prompt = f'Check the exact revision {artifact}; ignore https://untrusted.example.test.'
            h.callback('om_review_exact', f'Review: {task_ref} | {artifact} | {prompt}')
            saved = wait_review(prompt, lambda q: bool(q.get('remote_id')))
            ref, card_id = saved['feedback_ref'], saved['remote_id']
            remote = next(m for m in platform.evidence()['messages'] if 'remote-' + m['uuid'] == card_id)
            card = json.loads(remote['content'])
            _, link, form = card['body']['elements']
            href = link['behaviors'][0]['default_url']
            parsed = urlsplit(href)
            require(card['schema'] == '2.0' and remote['reply_to'] == 'om_review_exact' and
                    href == saved['review_url'] == workspace + '/tasks/' + task_ref + '#' + ref and
                    link['behaviors'][0]['type'] == 'open_url' and
                    form['elements'][1]['text']['content'] == 'Send review feedback' and
                    all(v not in remote['content'] for v in (artifact, other, content, 'untrusted.example.test')) and
                    not platform.evidence()['uploads'], 'review card leaked files or lost its trusted exact target')
            h.http(parsed.path, auth=False, expected=401)
            file_url = '/artifact?' + urlencode(dict(ref=artifact))
            h.http(file_url, auth=False, expected=401)
            html = h.http(parsed.path)
            section = html.split(f'id="{parsed.fragment}"', 1)[1].split('</section>', 1)[0]
            require('Review this result' in section and 'revision-a.txt' in section and 'other.txt' not in section and
                    h.http(file_url) == content, 'authenticated review did not resolve its exact artifact')

            answer = 'Revise the introduction.'
            require(h.card_feedback(local['feedback_ref'], card_id, answer, 'cross-ref')['toast']['type'] == 'error',
                    'card authorized a substituted feedback reference')
            require(h.card_feedback(ref, 'om_transferred', answer, 'transferred')['toast']['type'] == 'error',
                    'transferred card authorized a review')
            require(h.card_feedback(ref, card_id, answer, 'review-response')['toast']['type'] == 'success', 'review was rejected')
            accepted = review(prompt)['response_message_id']
            h.wait_state(lambda rs: len(rs) == 1, 'review continuation',
                         lambda _: rows('SELECT instruction,artifact_refs FROM task_instruction'))
            instructions = rows('SELECT instruction,artifact_refs FROM task_instruction')
            require(accepted and instructions == [dict(instruction=answer, artifact_refs=json.dumps([artifact], separators=(',', ':')))] and
                    rows('SELECT task_ref FROM task') == [dict(task_ref=task_ref)] and not h.rows()['approvals'],
                    'review did not continue exactly the original Task and artifact')
            require(h.card_feedback(ref, card_id, answer, 'review-retry')['toast']['type'] == 'success' and
                    h.command('task', 'respond', ref, text=answer)['message_id'] == accepted,
                    'Feishu/CLI replay changed the first receipt')
            web = h.http('/task/feedback', urlencode(dict(feedback_ref=ref, answer=answer, source_id='web-review',
                         return_to=parsed.path)).encode(), headers={'Content-Type': 'application/x-www-form-urlencoded',
                         'Accept': 'application/json'})
            require(web['message_id'] == accepted and
                    rows('SELECT reply_source_id FROM message_route WHERE message_id=?', (accepted,)) == [dict(reply_source_id=card_id)] and
                    rows('SELECT count(*) AS n FROM task_instruction')[0]['n'] == 1,
                    'cross-client replay changed the Feishu route or continued work twice')

            h.wait(lambda s: all(r['status'] == 'completed' for r in s['runs']), 'continued Task settled')
            replacement = 'Review the new revision.'
            h.callback('om_review_replacement', f'Review: {task_ref} | {other} | {replacement}')
            latest = wait_review(replacement, lambda q: bool(q.get('remote_id')))
            require(h.card_feedback(ref, card_id, answer, 'old-revision')['toast']['type'] == 'error' and
                    h.card_feedback(latest['feedback_ref'], card_id, answer, 'substituted-revision')['toast']['type'] == 'error' and
                    rows('SELECT count(*) AS n FROM task_instruction')[0]['n'] == 1,
                    'superseded card authorized feedback for a replaced revision')
            report['scenarios']['feishu-artifact-reviews'] = dict(
                feedback_ref=ref, card_id=card_id, source=remote['reply_to'], accepted_message_id=accepted,
                reviewed_artifact=artifact, authorized_link=href, continuations=1, no_uploads=True, no_workspace_card=False, superseded_card_rejected=True)
            report['checks'].append('Feishu exact artifact review opens the authenticated Task revision, preserves the original reply route, and accepts one cross-channel response')
        finally:
            h.cleanup()


def sending_feedback(binary,evidence,report):
    with tempfile.TemporaryDirectory(prefix='steward-send-feedback-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform)
        try:
            h.setup();h.start();h.browser(evidence.parent/'sending-ui','send-feedback')
            h.wait(lambda state:len(state['inputs'])==2 and len(state['deliveries'])==2 and all(row['status']=='sent' for row in state['deliveries']),'two optimistic sends admitted once')
            report['scenarios']['sending-feedback']=json.loads((evidence.parent/'sending-ui/sending.json').read_text())
            report['checks'].append('Composer: immediate safe outgoing bubble, white-on-orange contrast, next draft/file retained, authoritative lost-ack reconciliation, exact retry identity and animation without visible loading text')
        finally:h.cleanup()


def live_updates(binary,evidence,report):
    """Drive the existing long-text UI scenario without summarizing its markers."""
    with tempfile.TemporaryDirectory(prefix='steward-channel-live-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform,'capabilities')
        try:
            # This scrolling/UI scenario needs literal tool-result text. The
            # scripted summarizer preserves receipts, not semantic source text.
            h.setup(metadata=dict(name='Live UI workload',provider_kind='openai_compatible',
                                  endpoint='http://127.0.0.1:1/v1',wire_api='openai_completions',
                                  model_id='fixture-model',context_window=32768,max_output_tokens=1024,
                                  compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
            h.start()
            h.browser(evidence.parent/'channels-live-ui','live')
            state=h.rows()
            require(any(a['status']=='denied' for a in state['approvals']), 'live UI did not persist exact approval rejection')
            require(not (h.root/'data.computer/unapproved.txt').exists(), 'rejected live action executed')
            report['scenarios']['live-updates']=state
            report['checks'].append('Live Web: committed replies/approvals/progress update while draft, file, focus, selection, scroll and request identity survive; network/session recovery without reload')
        finally:h.cleanup()


def streaming_replies(binary, evidence, report):
    """A real model HTTP stream gated by observations in Chromium."""
    from http.server import ThreadingHTTPServer
    from provider_endpoint import Endpoint
    with tempfile.TemporaryDirectory(prefix='steward-streaming-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        h = ChannelHarness(binary, Path(temp), platform, '')
        server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
        server.requests, server.mode, server.stream_gates = [], 'success', h.root
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(binary)) +
                                '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            h.setup(metadata=dict(name='Streaming HTTP fixture', provider_kind='openai_compatible',
                endpoint=f'http://127.0.0.1:{server.server_port}/v1', wire_api='openai_completions', model_id='fixture-model',
                context_window=8192, max_output_tokens=1024, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            server.mode = 'web-stream'
            h.env['STEWARD_STREAM_GATES'] = str(h.root)
            h.start()
            h.wait_state(lambda status: status['readiness']=='ready', 'stream runtime readiness', lambda _:h.http('/api/status'))
            h.http('/api/conversation/stream', auth=False, expected=401)
            h.browser(evidence.parent/'channels-streaming', 'streaming')
            h.wait(lambda state: sorted(r['status'] for r in state['runs']) == ['completed', 'failed'], 'committed and failed stream outcomes')
            messages = h.http('/api/conversation')
            replies = [m for m in messages if m['message_id'].startswith('reply:')]
            expected = 'First chunk <img src=x onerror=unsafe()>\n\n**增长趋势：**A在增长。\n\n**正在解释，继续说明**\n\nSecond chunk 中文.\nFinal tail.'
            require(len(replies)==1 and replies[0]['content'][0]['text'].rstrip()==expected and
                    'Interrupted draft only.' not in json.dumps(messages), 'preview changed durable reply history')
            report['scenarios']['streaming-replies'] = dict(durable_replies=1, failure_discarded=True, late_subscriber=True,
                                                           reconnected=True, commit_tail=True)
            report['checks'].append('Native Go model HTTP chunks reach Web before completion, reconnect restores exact text, the unread commit tail survives until one final projection, and an interrupted stream leaves no reply')
        finally:
            for gate in ('second', 'finish', 'fail'):
                (h.root/gate).touch()
            h.cleanup()
            server.shutdown();server.server_close();thread.join()


def transport_channels(binary, evidence, report):
    feishu_auth_lifecycle(binary,report)
    feishu_topic_delivery(binary,report)
    feishu_task_questions(binary, report)
    feishu_artifact_reviews(binary, report)
    with tempfile.TemporaryDirectory(prefix='steward-channels-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform)
        try:
            h.start()
            require('Open your workspace' in h.http('/',auth=False),'login page missing')
            h.http('/api/conversation',auth=False,expected=401)
            h.http('/api/message',dict(source_id='x',content=[dict(type='text',text='x')]),headers={'Origin':'http://evil.example'},expected=403)
            h.http('/api/progress',data=b'x',method='POST',expected=404)
            h.callback('forged','bad',signature=False,expected=401)
            h.callback('unbound','bad',sender='other',expected=403)
            h.callback('recipient','bad',destination='other',expected=403)
            h.browser(evidence.parent/'channels-ui','setup')
            h.browser(evidence.parent/'preferences-ui','preferences')
            report['checks'].append('Preferences: grouped bilingual language list, accessible hover/keyboard/touch help, single-flight autosave, browser persistence, no-JS fallback and neighboring Resources/Notifications/Capabilities')
            h.wait(lambda s:any(d['status']=='sent' and d['channel']=='web' for d in s['deliveries']),'browser setup and first reply')
            report['checks'].append('Launch/Doctor: browser language switch persists into Chat, followed by real loopback Web setup and file attachment; explicit model Fake; auth/CSRF/signature/identity/recipient rejection')
            # Duplicate callback IDs and new callback IDs carrying the same source
            # message are distinct retry patterns, both deduplicated durably.
            with ThreadPoolExecutor(max_workers=8) as pool:
                list(pool.map(lambda i:h.callback('feishu-source','Continue the same plan in Feishu.',event='feishu-event-'+str(i%3)),range(12)))
            settled=h.wait(lambda s:len(s['inputs'])==2 and len(s['deliveries'])==2 and all(d['status']=='sent' for d in s['deliveries']),'one Feishu input and notice')
            h.callback('feishu-source','parameter drift',event='different-retry',expected=409)
            history=h.http('/api/conversation')
            require(len(history)==4 and [m['source'] for m in history if m['role']=='user']==['web','feishu'],'shared Conversation order')
            require([m['sequence'] for m in history]==list(range(1,5)),'nonmonotonic Conversation')
            require(len(platform.evidence()['messages'])==1 and len(platform.evidence()['send_attempts'])==1,'duplicate remote send attempt or notification')
            h.stop();h.start()
            h.callback('feishu-source','Continue the same plan in Feishu.',event='after-restart')
            require(len(h.rows()['inputs'])==2 and len(platform.evidence()['messages'])==1,'restart duplicated accepted callback/delivery')
            h.browser(evidence.parent/'channels-ui','conversation')
            with Terminal(h.args('tui'),h.env,h.root) as terminal:
                terminal.expect('Steward: idle');terminal.send('/history')
                terminal.expect('Processed: Continue the same plan in Feishu.')
                require(terminal.stop()==0,'shared-history TUI did not stop')
                report['scenarios']['tui-history']=terminal.transcript()
            report['scenarios']['shared-conversation']=dict(state=h.rows(),conversation=history,platform=platform.evidence())
            report['checks'].append('Drive/Evidence: Web and signed Feishu callbacks share ordered Conversation; concurrent event/source retries, drift rejection and restart produce one remote reply')
            # Incoming attachment import is stable across independent callback IDs.
            h.callback('media-message','',kind='file')
            h.wait(lambda s:len(s['inputs'])==3 and len(s['deliveries'])==3 and all(d['status']=='sent' for d in s['deliveries']),'attachment reply')
            h.callback('media-message','',kind='file',event='media-retry')
            state=h.rows();require(len(state['resources'])==2 and len(state['inputs'])==3,'attachment duplicate changed reference')
            attached=[p['artifact_ref'] for m in h.http('/api/conversation') if m['source']=='feishu' for p in m['content'] if p.get('artifact_ref')]
            require(len(attached)==1,'missing incoming Artifact reference')
            downloaded=h.http('/artifact?'+urlencode(dict(ref=attached[0])))
            require(downloaded=='Inbound attachment from the authenticated Feishu owner.\n','wrong attachment bytes')
            h.http('/artifact?ref=artifact:other',expected=404)
            report['scenarios']['attachment']=dict(state=state,artifact=attached[0],sha256=hashlib.sha256(downloaded.encode()).hexdigest())
            report['checks'].append('Verified Feishu attachment imports once, opens through authenticated Web boundary, and rejects unknown references')
            h.stop()
        finally:h.cleanup()

    # Approval recovery uses the same external-effect Fake and persistent Security
    # lifecycle as TUI. Callback races cross two authenticated transports.
    with tempfile.TemporaryDirectory(prefix='steward-channel-approval-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform,'approval')
        try:
            h.setup();h.start()
            h.callback('effect-source',json.dumps(dict(target='alex.chen@example.test',text='Steward review: 2026-10-19 15:00–15:30 +08:00')))
            state=h.wait(lambda s:len(s['approvals'])==1 and s['approvals'][0]['status']=='pending','approval notice')
            old=state['approvals'][0]
            h.browser(evidence.parent/'channels-ui','approval')
            h.http('/api/message',dict(source_id='steering',content=[dict(type='text',text=json.dumps(dict(target='alex.chen@example.test',text='Steward review: 2026-10-20 11:00–11:30 +08:00; do not use Monday.')))]))
            state=h.wait(lambda s:len(s['approvals'])==2 and s['approvals'][-1]['status']=='pending','steering invalidates old approval')
            require(state['approvals'][0]['status']=='invalidated','old Monday approval remained valid')
            approval=state['approvals'][-1]
            h.callback('old-approve','/approve '+old['approval_id']+' '+old['frozen_arguments_hash'],expected=409)
            # Crash while awaiting approval, not while committing its notice.
            # An interrupted external send must legitimately remain unknown;
            # delivery_leases separately exercises that ambiguity boundary.
            h.wait(lambda s:len(s['deliveries'])==4 and all(d['status']=='sent' for d in s['deliveries']),
                   'approval notices durable before restart')
            h.stop(kill=True);h.start()
            h.http('/approval',data=urlencode(dict(approval_id=approval['approval_id'],hash='drift',decision='approved')).encode(),headers={'Content-Type':'application/x-www-form-urlencoded'},expected=409)
            command='/approve '+approval['approval_id']+' '+approval['frozen_arguments_hash']
            with ThreadPoolExecutor(max_workers=6) as pool:list(pool.map(lambda i:h.callback('approve-'+str(i),command),range(6)))
            # Two Task receipts, two exact approval notices and the final Task
            # result each have one delivery under the delegated execution flow.
            state=h.wait(lambda s:any(t['tool_name']=='mcp__fixture__send' and t['status']=='succeeded' for t in s['tools']) and len(s['deliveries'])==5 and all(d['status']=='sent' for d in s['deliveries']),'approved effect once')
            effects=[t for t in state['tools'] if t['tool_name']=='mcp__fixture__send' and t['status']=='succeeded']
            require(len(effects)==1 and effects[0]['attempt_count']==1,'duplicate approved effect')
            frozen=json.loads(effects[0]['arguments_json'])
            require(frozen['target']=='alex.chen@example.test' and '2026-10-20 11:00' in frozen['text'],'wrong final recipient or date')
            with closing(sqlite3.connect(h.root/'data/effect-ledger.db')) as db, db:
                ledger=[dict(zip(('key','args','result'),r)) for r in db.execute('SELECT * FROM effects')]
            require(len(ledger)==1 and json.loads(ledger[0]['args'])==frozen,'external ledger disagrees with approved action')
            report['scenarios']['approval-restart']=dict(state=state,platform=platform.evidence(),effect_ledger=ledger)
            report['checks'].append('GC-12 channel/Security fragment; Approval: exact frozen-hash rejection, SIGKILL/restart, concurrent duplicate callbacks; one approved Tool dispatch')
            h.stop()
        finally:h.cleanup()
    extra_scenarios(binary,report)
    delivery_leases(binary, report)


def web_channels(binary, evidence, report):
    preference_settings(binary,evidence,report)
    memory_inspection(binary,evidence,report)
    attention_workspace(binary,evidence,report)
    sending_feedback(binary,evidence,report)
    live_updates(binary,evidence,report)
    streaming_replies(binary,evidence,report)

    inspection_relationships(binary,evidence,report)

    capability_settings(binary,evidence,report)
    dependency_recovery(binary,report)
    native_components(binary, evidence, report)
    rich_replies(binary, evidence, report)
    account_presentation(binary, evidence.parent/'account-ui', report)
    native_trace_inspection(binary,evidence,report)
    page_transitions(binary,evidence,report)
    model_connections(binary,evidence,report)
    task_control_components(binary,evidence,report)


CHANNEL_GROUPS = dict(transport=transport_channels, web=web_channels)


def run(binary, evidence, group='all'):
    report = dict(checks=[], scenarios={})
    selected = CHANNEL_GROUPS if group == 'all' else {group: CHANNEL_GROUPS[group]}
    for check in selected.values():
        check(binary, evidence, report)
    report['checks'].append('Cleanup: Web/Bridge/browser children joined, sandbox HTTP servers stopped, disposable roots removed')
    return report


def model_connections(binary,evidence,report):
    """Real Web key entry, owned HTTP model peer, file credentials and composer."""
    from http.server import ThreadingHTTPServer
    from provider_endpoint import Endpoint
    with tempfile.TemporaryDirectory(prefix='steward-model-connections-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
        server=ThreadingHTTPServer(('127.0.0.1',0),Endpoint);server.daemon_threads=True;server.requests=[];server.mode='success'
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        h=ChannelHarness(binary,Path(temp),platform,'')
        try:
            h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
            h.env['STEWARD_MODEL_CONNECTION_ENDPOINT']=f'http://127.0.0.1:{server.server_port}/v1'
            h.start();output=evidence.parent/'model-connections-ui';h.browser(output,'model-connections')
            key=b'SYNTHETIC_WEB_CONNECTION_KEY_NEVER_ECHO'
            require(any(r.get('auth')=='Bearer '+key.decode() for r in server.requests),'API key did not reach the owned provider boundary')
            for path in h.root.rglob('*'):
                if not path.is_file() or path.parent.name in ('secrets','account-secrets'):continue
                require(key not in path.read_bytes(),'API key leaked outside private credential files')
            keys=[p for p in (h.root/'data/secrets').glob('*') if p.is_file() and p.read_bytes()==key]
            require(len(keys)==1 and keys[0].stat().st_mode&0o777==0o600,'disconnect did not remove its credential or key permissions are wrong')
            report['scenarios']['model-connections']=json.loads((output/'models-connections.json').read_text())
            report['checks'].append('Models: native API-key entry through owned HTTP provider, atomic file/metadata handling, automatic verification, composer selection, versioned disconnect, retained history, no credential echo, responsive screenshots')
        finally:
            h.cleanup();server.shutdown();server.server_close();thread.join()


def page_transitions(binary, evidence, report):
    with tempfile.TemporaryDirectory(prefix='steward-page-transitions-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform)
        try:
            h.setup()
            h.command('model', 'add', 'http://127.0.0.1:1/v1', 'candidate-model')
            h.start()
            output = evidence.parent / 'page-transitions-ui'
            h.browser(output, 'page-transitions')
            report['scenarios']['page-transitions'] = json.loads((output / 'page-transitions.json').read_text())
            consistency=evidence.parent/'settings-consistency-ui'
            h.browser(consistency,'settings-consistency')
            report['scenarios']['settings-consistency']=json.loads((consistency/'settings-consistency.json').read_text())
            report['checks'].append('Page transitions: cached critical fonts without fallback frames, bilingual light/dark Resources sizing, persisted collapse, stable pre-bundle layout, native fallback, notification autosave and pointer/keyboard composer focus without an outer frame')
        finally:
            h.cleanup()


def task_control_components(binary, evidence, report):
    # The CI channels gate owns this scenario; the browser driver is not an
    # uncalled example. All schedule writes use a disposable data root.
    with tempfile.TemporaryDirectory(prefix='steward-task-controls-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform)
        h.now = '2026-10-15T12:00:00+08:00'
        try:
            h.setup()
            h.start()
            output = evidence.parent / 'task-controls-ui'
            h.browser(output, 'schedule')
            require(not h.rows()['runs'], 'schedule control checks unexpectedly started a Run')
            report['scenarios']['task-controls'] = json.loads((output / 'schedule-browser.json').read_text())
            report['checks'].append('Task shared Select/Switch: native short-list selection, keyboard confirmation/cancellation, dependent fields, single popup, manual save, persistence, stale drafts, touch, native fallback and no premature dispatch')
        finally:
            h.cleanup()


def memory_inspection(binary, evidence, report):
    """Real encrypted automatic/tool/local memories, scoped owner reads and provenance."""
    def plan(calls):
        return 'Capabilities: ' + json.dumps(calls)
    with tempfile.TemporaryDirectory(prefix='steward-memory-ui-') as temp, Sandbox() as platform:
        h = ChannelHarness(binary, Path(temp), platform, 'capabilities')
        try:
            h.env['STEWARD_MEMORY_UI_KEY'] = 'M' * 32
            h.config.write_text(h.config.read_text() + 'memory:\n  encryption_key_ref: env:STEWARD_MEMORY_UI_KEY\n')
            h.setup(metadata=dict(name='Memory inspection workload', provider_kind='openai_compatible',
                endpoint='http://127.0.0.1:1/v1', wire_api='openai_completions', model_id='fixture-model',
                context_window=32768, max_output_tokens=1024, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            explicit = 'I prefer jasmine tea. <img src=x onerror="unsafe()"> 真实记忆正文'
            h.send('memory-tool', plan([dict(name='builtin__memory_manage', arguments=dict(operation='remember', content=explicit))]))
            h.command('chat', 'work')
            h.send('memory-automatic', 'Capabilities: []\nI prefer walking by the river every morning')
            h.command('chat', 'work')
            for index in range(23):
                h.command('memory', 'manage', 'memory-list-' + str(index), text=json.dumps(dict(operation='remember', content='I prefer local memory item %02d' % index)))
            h.command('memory', 'work')
            note = h.command('memory', 'manage', 'layer-note', text=json.dumps(dict(operation='write', layer='notes', title='Research plan', content='Preserve the constraints and validate the research findings.')))
            wiki = h.command('memory', 'manage', 'layer-wiki', text=json.dumps(dict(operation='write', layer='representations', title='Research wiki', content='Synthesized research guidance with a link to the detailed plan.', links=[note['ref']])))
            h.command('memory', 'manage', 'layer-injected', text=json.dumps(dict(operation='write', layer='injected', title='Session memory', content='Use the research wiki for detailed guidance.', links=[wiki['ref']])))
            h.start()
            h.wait(lambda state: all(row['status'] in ('completed','failed','cancelled') for row in state['runs']), 'completed memory fixture')
            def fingerprint():
                result = {}
                for name, tables in {'state.db': ('run','tool_invocation','memory_mutation','memory_privacy'),
                                     'conversation.db': ('conversation_message',), 'memory.db': ('memory_record','memory_receipt','memory_fact','memory_evidence','memory_document','memory_document_dependency','memory_document_receipt')}.items():
                    with closing(sqlite3.connect(h.root/'data'/name)) as db:
                        db.execute('BEGIN')
                        for table in tables:
                            result[name+':'+table] = hashlib.sha256(json.dumps(db.execute('SELECT * FROM '+table+' ORDER BY 1').fetchall(), default=str).encode()).hexdigest()
                return result
            for layer in ('transcripts','notes','representations','injected'):
                layer_page = h.http('/api/developer/memory?' + urlencode(dict(layer=layer)))
                require(layer_page['items'] and all(item['layer']==layer for item in layer_page['items']), 'layer projection missing or mixed: '+layer)
                detail = h.http('/api/developer/memory?' + urlencode(dict(layer=layer,ref=layer_page['items'][0]['ref'])))
                require(len(detail['items'])==1 and detail['items'][0].get('content'), 'layer detail missing: '+layer)
            items, cursor = [], ''
            for _ in range(100):
                page = h.http('/api/developer/memory?' + urlencode(dict(limit=2, cursor=cursor)))
                require(len(page['items'])<=2, 'unbounded Memory page')
                items += page['items']; cursor = page.get('next','')
                if not cursor:
                    break
            else:
                raise RuntimeError('Memory pagination did not terminate')
            require(len(items)>=25 and len(items)==len({item['ref'] for item in items}), 'Memory pagination repeated or lost records')
            require(any(item['origin']=='automatic' for item in items), 'automatic memory missing')
            target = next(item for item in items if item.get('content')==explicit)
            require(target['origin']=='explicit' and any(source.get('run_id') for source in target['sources']), 'tool memory lost its actual invocation source')
            automatic = next(item for item in items if item['origin']=='automatic')
            require(automatic['content'] != 'I prefer walking by the river every morning'
                    and 'Capabilities:' not in automatic['content'],
                    'automatic Memory copied the original statement or Tool plan instead of a derived fact')
            source = next(source for source in automatic['sources'] if source['kind']=='conversation')
            source_page = h.http('/api/developer/memory?' + urlencode(dict(ref=automatic['ref'], source=source['ref'])))
            require(any('I prefer walking' in value.get('text','') for value in source_page['items'][0]['sources']), 'original Memory source content missing')
            h.http('/api/developer/memory?' + urlencode(dict(ref=target['ref'], source=source['ref'])), expected=404)
            search = h.http('/api/developer/memory?' + urlencode(dict(search='真实记忆')))
            require(len(search['items'])==1 and search['items'][0]['ref']==target['ref'], 'decrypted literal Unicode search failed')
            replacement = 'I prefer oolong tea after the correction'
            h.command('memory', 'manage', 'memory-correction', text=json.dumps(dict(operation='remember', content=replacement, supersedes=target['ref'])))
            h.command('memory', 'work')
            retired = h.http('/api/developer/memory?' + urlencode(dict(ref=target['ref'])))['items'][0]
            require(retired['status']=='superseded' and not retired.get('content') and retired.get('replaced_by'), 'retired memory leaked body or lost lineage')
            for suffix, code in [('',401), ('?agent=other',400), ('?sql=SELECT+1',400), ('?tier=unknown',400), ('?limit=0',400), ('?limit=51',400), ('?search=a&search=b',400), ('?ref=memory:missing',404)]:
                h.http('/api/developer/memory'+suffix, auth=code!=401, expected=code)
            h.http('/api/developer/database', expected=404)
            h.http('/developer/database', expected=404)
            h.http('/api/developer/memory', data=b'x', method='POST', expected=404)
            first = h.http('/api/developer/memory?limit=2')
            h.http('/api/developer/memory?' + urlencode(dict(tier='core', cursor=first['next'])), expected=400)
            before = fingerprint()
            h.browser(evidence.parent/'memory-ui', 'memory')
            require(fingerprint()==before, 'Memory browsing changed archived domain records')
            h.stop(); h.start()
            require(h.http('/api/developer/memory')['total']==len(items), 'Memory restart lost current entries')
            h.command('memory', 'manage', 'memory-forget', text=json.dumps(dict(operation='forget', memory_ref=automatic['ref'])))
            h.http('/api/developer/memory?' + urlencode(dict(ref=automatic['ref'])), expected=404)
            h.command('memory', 'work')
            require('walking by the river' not in json.dumps(h.http('/api/developer/memory?status=all')), 'Forget resurrected old memory content')
            report['scenarios']['memory'] = dict(records=len(items), encrypted=True, read_only=True, source_content=True, restart=True, forget=True)
            report['checks'].append('Memory: real encrypted automatic/tool/local records, Unicode content search, source evidence and Trace links, keyset pagination, retired metadata, owner auth, native/mobile/theme browser flows, read-only restart and Forget')
            h.stop()
        finally:
            h.cleanup()


def default_capability_settings(binary,evidence,report):
    """Real startup defaults with an explicitly injected, owned binary SecretStore."""
    class MemorySecrets(socketserver.StreamRequestHandler):
        def handle(self):
            value=json.loads(self.rfile.readline(131072))
            with self.server.lock:
                op,ref=value['op'],value['ref']
                if op=='put':self.server.values[ref]=value['value']
                elif op=='delete':self.server.values.pop(ref,None)
                result=self.server.values.get(ref)
            self.wfile.write((json.dumps(dict(ok=op!='get' or result is not None,value=result))+'\n').encode())
    with tempfile.TemporaryDirectory(prefix='steward-default-settings-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        root=Path(temp).resolve();h=ChannelHarness(binary,root,platform,'terminal-input')
        h.production_events=True
        h.env.update(STEWARD_NATS_URL=broker.url,STEWARD_NATS_STREAM_PREFIX='default_memory_ui')
        socket=root/'secrets.sock'
        server=socketserver.ThreadingUnixStreamServer(str(socket),MemorySecrets)
        server.values={'env:'+name:base64.b64encode(h.env[name].encode()).decode() for name in ('STEWARD_WEB_TEST_TOKEN','STEWARD_FEISHU_VERIFY','STEWARD_FEISHU_ENCRYPT','STEWARD_FEISHU_ACCESS')}
        server.lock=threading.Lock();server.daemon_threads=True;os.chmod(socket,0o600)
        thread=threading.Thread(target=server.serve_forever);thread.start()
        original=h.args;h.args=lambda *args:original('-test-secret-store',str(socket),*args)
        try:
            with h.config.open('a') as out:out.write('defaults: {memory: true, search: true, skills: true, computer: true}\n')
            h.setup();h.start()
            h.browser(evidence.parent/'default-settings-ui','capability-defaults')
            key_ref=json.loads((root/'data/default-memory.json').read_text())['ref']
            require(len(base64.b64decode(server.values[key_ref]))==32,'default Memory did not create its key')
            h.stop();h.start()
            require(json.loads((root/'data/default-memory.json').read_text())['ref']==key_ref,'restart replaced the default key')
            require('Enabled' in h.http('/settings/capabilities/memory'),'default Memory lost its enabled status')
            require(not (root/'data.desktop').exists(),'browsing settings prepared a desktop')
            report['checks'].append('Default Memory and local search start together; one enabled Memory entry, concise Preferences layout, Chinese/mobile, stable SecretStore key and no desktop download')
            h.stop()
        finally:
            h.cleanup();server.shutdown();server.server_close();thread.join(10)


def capability_settings(binary,evidence,report):
    """Owner UI -> real config/probes -> restart, retaining frozen Run records."""
    default_capability_settings(binary,evidence,report)
    from verify_recall import Embeddings
    with tempfile.TemporaryDirectory(prefix='steward-capability-settings-') as temp, Sandbox() as platform:
        root=Path(temp).resolve();h=ChannelHarness(binary,root,platform,'terminal-input')
        peer=Embeddings()
        try:
            skills=root/'skills';skills.mkdir(mode=0o700)
            item=skills/'research';item.mkdir();(item/'SKILL.md').write_text('---\nname: research\ndescription: Inspect sources before making claims.\n---\nRead the evidence.\n')
            provider=root/'provider';provider.mkdir(mode=0o700)
            vm=root/'computer';vm.mkdir(mode=0o700)
            source=root/'base-source';source.mkdir(mode=0o700)
            for name in ('disk.img','nvram.bin'):(source/name).write_text('owned-'+name)
            (source/'config.json').write_text(json.dumps(dict(macAddress='02:00:00:00:00:01',machineIdentifier='fixture-base',cpuCount=4)))
            h.env.update(STEWARD_SETUP_MEMORY_KEY='k'*32,STEWARD_COMPUTER_FIXTURE_PASSWORD='fixture-only')
            start=h.processes.start if h.processes is not None else subprocess.Popen
            native=start([str(binary),'-test-computer-server',str(vm)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=root,env=h.env)
            h.children.append(native)
            with selectors.DefaultSelector() as selector:
                selector.register(native.stdout,selectors.EVENT_READ);require(selector.select(10),'Computer diagnostic peer unavailable')
            computer=json.loads(native.stdout.readline())
            javascript=dict(command=shutil.which('node'),script=str(ROOT/'runtime/javascript/src/sidecar.mjs'))
            preparing=root/'computer-prepare.yaml';preparing.write_text(json.dumps(dict(version=1,javascript=javascript,computer=computer)))
            run=h.processes.run if h.processes is not None else subprocess.run
            prepared=run([str(binary),'-config',str(preparing),'computer','prepare-base',str(source)],env=h.env,cwd=root,capture_output=True,text=True,timeout=20)
            require(prepared.returncode==0,'Computer base preparation failed: '+prepared.stderr)
            h.setup();h.start()
            h.http('/api/message',dict(source_id='capability-baseline',content=[dict(type='text',text='Owner configuration baseline.')]))
            h.wait(lambda state:state['deliveries'] and all(d['status']=='sent' for d in state['deliveries']),'baseline before configuration')
            def snapshots():
                with closing(sqlite3.connect(root/'data/state.db')) as db, db:return dict((r[0],r[1:]) for r in db.execute('SELECT snapshot_id,tool_specs_hash,tools_json FROM capability_snapshot'))
            frozen=snapshots();original=h.config.read_bytes()
            h.http('/settings/capabilities',auth=False,expected=401)
            h.http('/settings/capabilities/unknown',expected=404)
            form={'Content-Type':'application/x-www-form-urlencoded'}
            # The owner-only mutations retain host/origin authentication guards.
            h.http('/capability/configure',urlencode(dict(capability='memory',action='test')).encode(),headers={**form,'Origin':'http://evil.example'},expected=403)
            revision=hashlib.sha256(original).hexdigest()
            rejected=h.http('/capability/configure',urlencode(dict(capability='memory',action='save',revision=revision,configuration='encryption_key_ref: env:STEWARD_SETUP_MEMORY_KEY')).encode(),headers=form,expected=409)
            require('test this exact configuration successfully before saving' in rejected,'unverified proof was not enforced')
            h.http('/capability/configure',urlencode(dict(capability='memory',action='test',revision=revision,configuration='x'*65537)).encode(),headers=form,expected=413)
            require(h.config.read_bytes()==original,'unverified save modified configuration')
            h.env['STEWARD_CAPABILITY_SETUP_FACTS']=json.dumps(dict(embedding=f'http://127.0.0.1:{peer.server.server_port}/v1',skills=str(skills),provider=str(provider),binary=str(binary),workspace=str(root/'workspace'),computer=dict(computer=computer,javascript=javascript)))
            h.browser(evidence.parent/'capability-setup-ui','capability-setup')
            require(snapshots()==frozen,'settings changed existing frozen capabilities')
            require(not (provider/'mcp-effects.jsonl').exists(),'MCP diagnostic invoked a business tool')
            require(not list((vm/'vms').iterdir()),'Computer diagnostic booted or cloned a VM')
            require(all(p['input']=='Steward embedding connection test' for p in peer.requests),'embedding diagnostic sent private conversation')
            require('k'*32 not in h.config.read_text() and 'fixture-only' not in h.config.read_text(),'plaintext credential in settings')
            require(h.config.stat().st_mode&0o777==0o600,'configuration is not private')
            h.stop();h.start()
            for kind in ('memory','embedding','mcp','skills','computer'):
                view=h.http('/settings/capabilities/'+kind)
                require('data-status="configured">Enabled' in view and 'Restart required' not in view,'saved capability was not loaded after restart: '+kind)
            h.http('/api/message',dict(source_id='capability-after-restart',content=[dict(type='text',text='Confirm the new setup is available.')]))
            try:
                h.wait(lambda state:len(state['deliveries'])==2 and all(d['status']=='sent' for d in state['deliveries']),'new request after restart')
            except RuntimeError as error:
                with closing(sqlite3.connect(root/'data/state.db')) as db, db:
                    diagnostic=db.execute("SELECT status,json_extract(checkpoint_json,'$.phase'),json_extract(checkpoint_json,'$.error') FROM run ORDER BY created_at").fetchall()
                raise RuntimeError(str(error)+'; '+json.dumps(diagnostic)) from error
            after=snapshots();require(all(after.get(k)==v for k,v in frozen.items()),'restart rewrote old snapshots')
            new_main = {key:data for key,data in after.items() if key not in frozen}
            require(new_main and all('mcp__owner_catalog__read' in data[1] for data in new_main.values()),'new Main missed the configured business tool')
            h.stop();h.fixture='capabilities'
            h.setup(metadata=dict(name='Configured Worker capacity',provider_kind='openai_compatible',
                                 endpoint='http://127.0.0.1:1/v1',wire_api='openai_completions',
                                 model_id='fixture-model',context_window=65536,max_output_tokens=4096,
                                 compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
            receipt=h.send('capability-worker','Delegate capabilities: []')
            from verify_recall import drain_with_publication_barrier
            drain_with_publication_barrier(h,receipt['run_id'],prefix='configured-worker-')
            worker_snapshots=snapshots()
            require(any('mcp__owner_catalog__read' in data[1] for key,data in worker_snapshots.items() if key not in after),'new Worker missed the newly configured provider')
            require(all(worker_snapshots.get(k)==v for k,v in frozen.items()),'Worker admission rewrote old snapshots')
            report['scenarios']['capability-settings']=dict(kinds=['memory','embedding','mcp','skills','execution','computer'],old_snapshots_retained=True,new_worker_uses_new_tools=True,main_uses_configured_business_tools=True,probe_does_not_boot_or_invoke=True,embedding_requests=len(peer.requests))
            report['checks'].append('F-030: real owner UI config/test/save/restart for six capabilities; failed probes repaired, no-JS/mobile, exact candidate/revision checks, preserved frozen snapshots and newly loaded tools')
            h.stop()
        finally:
            h.cleanup();peer.close()


def native_trace_inspection(binary,evidence,report):
    """Actual Go provider HTTP retries, durable result reuse, and the tracing UI."""
    from http.server import ThreadingHTTPServer
    from provider_endpoint import Endpoint
    class TraceEndpoint(Endpoint):
        def do_POST(self):
            time.sleep(.04)
            if getattr(self.server,'reject_next',False):
                self.server.reject_next=False
                body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                self.server.requests.append(dict(method='POST',auth=self.headers.get('Authorization'),session=self.headers.get('session_id'),body=body))
                self.send_response(503);self.send_header('Content-Length','0');self.end_headers();return
            return super().do_POST()
    with tempfile.TemporaryDirectory(prefix='steward-native-traces-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        server=ThreadingHTTPServer(('127.0.0.1',0),TraceEndpoint);server.daemon_threads=True
        server.requests=[];server.mode='success'
        server.task_title='筛选杭州有产品设计感的前沿 AI 团队'
        server.task_description=('Browser report fixture. 用户希望在杭州寻找以用户体验与产品原则为核心的 AI 团队。'
                                 '核对实际职责、研发地点、招聘来源与团队工作方式；明确区分已验证事实、推测与未知信息。'
                                 '不要将团队定位或设计偏好写成客观事实；没有证据时如实说明。'
                                 '输出可验证的候选、技术匹配理由、未知项和下一步访谈问题。'*3)
        server.task_root_delay=6
        server.task_root_tool='builtin__artifact_manage'
        server.task_root_arguments=dict(operation='save',name='trace-fixture.txt',mediaType='text/plain',text='Synthetic browser regression evidence.')
        server.task_followup=('## 本轮研究结论\n\n这是一份用于界面回归的合成报告。示例团队与证据状态不代表真实招聘信息。\n\n'
                              '优先核实团队是否持续交付面向真实用户的 AI 产品，以及工程师能否参与产品取舍。当前证据不足以确认全部岗位，因此将候选与待验证问题分开呈现。\n\n'
                              '### 候选方向\n\n'
                              '| 候选 | 技术匹配 | 仍需核实 |\n| --- | --- | --- |\n'
                              '| 示例团队 A | Agent 后端、业务系统集成 | 杭州研发地点与岗位状态 |\n'
                              '| 示例团队 B | AI CRM、客户研究工作流 | 产品决策权限与协作方式 |\n'
                              '| 示例团队 C | 面向用户的 AI 应用 | 实际职责及团队工作时间 |\n\n'
                              '### 建议的核验顺序\n\n'
                              '1. 先查看官方招聘页和近期产品发布，记录来源与日期。\n'
                              '2. 向团队确认后端、模型应用与产品工作的实际分工。\n'
                              '3. 用具体产品讨论验证设计理念，而不是仅凭品牌印象判断。\n\n'
                              '### 面试时值得追问\n\n'
                              '- 最近一次因为用户反馈而改变产品方向是什么？\n'
                              '- 工程师如何参与交互和体验决策？\n'
                              '- 跨团队需求冲突时，由谁决定优先级，依据是什么？\n\n'
                              '目前仍不能确认实际岗位、杭州办公安排及工作时间。这些问题需要官方来源或团队直接答复，不能由模型补全。\n\n'
                              'Main Task follow-up through Pi.')
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        h=ChannelHarness(binary,Path(temp),platform,'')
        try:
            h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
            h.env['STEWARD_TRACE_KEY']='SYNTHETIC_TRACE_CREDENTIAL_NEVER_STORE'
            auth=h.command('model','login','STEWARD_TRACE_KEY')
            h.setup(metadata=dict(name='Trace HTTP fixture',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{server.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=65536,max_output_tokens=4096,generation_defaults={},compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True),auth_connection_id=auth['connection_id']))
            server.mode='tasks';server.reject_next=True
            h.start()
            h.wait_state(lambda status:status['readiness']=='ready','native trace runtime readiness',lambda _:h.http('/api/status'))
            h.browser(evidence.parent/'execution-empty-ui','execution-empty')
            # The Root terminal commit can precede its causal Main follow-up. Wait
            # for both Main runs and their deliveries before measuring read-only UI.
            h.wait(lambda state:any(r['worker_position']=='root' for r in state['runs']) and sum(r['kind']=='main' for r in state['runs'])>=2 and all(r['status'] in ('completed','failed','cancelled') for r in state['runs']) and len(state['deliveries'])>=2 and all(d['status']=='sent' for d in state['deliveries']),'native trace completion and Main follow-up')
            # A second real execution exercises previous/next inspection without
            # replacing the first trace's frozen request or accepted delivery.
            h.http('/api/message',dict(source_id='native-trace-second',content=[dict(type='text',text='Prepare a second independent report from the available evidence.')]))
            h.wait(lambda state:sum(r['worker_position']=='root' for r in state['runs'])==2 and sum(r['kind']=='main' for r in state['runs'])>=4 and all(r['status'] in ('completed','failed','cancelled') for r in state['runs']) and all(d['status']=='sent' for d in state['deliveries']),'second native execution and delivery')
            capture_state={}
            def captured(_):
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    db.execute('BEGIN')
                    runs={r[0] for r in db.execute('SELECT run_id FROM run')}
                    physical=[r for r in server.requests if r.get('session') in runs and r['method']=='POST']
                    # agent_end is intentionally emitted after the terminal commit.
                    ended=db.execute("SELECT count(DISTINCT run_id) FROM run_trace WHERE json_extract(event_json,'$.kind')='agent_end'").fetchone()[0]
                    capture_state.update(physical=len(physical),ended=ended,runs=len(runs),phases=db.execute('SELECT phase,count(*) FROM dispatch_observation GROUP BY phase').fetchall(),generations=db.execute('SELECT status,count(*) FROM model_generation GROUP BY status').fetchall())
                    return ended==len(runs) and db.execute("SELECT count(*) FROM dispatch_observation WHERE phase='headers'").fetchone()[0]==len(physical)
            try:
                h.wait_state(captured,'all terminal HTTP observations persisted',timeout=10)
            except RuntimeError as error:
                raise RuntimeError(str(error)+'; '+json.dumps(capture_state)) from error
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.execute('BEGIN')
                generation,main=db.execute('SELECT generation_id,run_id FROM model_generation ORDER BY created_at LIMIT 1').fetchone()
                before=[r['body'] for r in server.requests if r.get('session')==main and r['method']=='POST'][:2]
                require(len(before)==2 and before[0]==before[1],'actual HTTP retry was not exercised')
                root=db.execute("SELECT run_id FROM run WHERE worker_position='root' ORDER BY created_at,run_id LIMIT 1").fetchone()[0]
                require(db.execute("SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='builtin__artifact_manage' AND status='succeeded'",(root,)).fetchone()[0]==1,'Root fixture did not execute a valid tool successfully')
                attempts=db.execute("SELECT body_json FROM dispatch_observation WHERE generation_id=? AND phase='started' ORDER BY occurred_at",(generation,)).fetchall()
                require(len(attempts)==2,'result reuse fabricated a new HTTP attempt')
                require([json.loads(a[0]) for a in attempts]==before,'observed final request differs from actual HTTP body')
                raw=''.join(r[0] or '' for r in db.execute('SELECT body_json FROM dispatch_observation'))
                require(h.env['STEWARD_TRACE_KEY'] not in raw and 'Bearer ' not in raw,'wire capture persisted credentials')
                facts=dict(main=main,root=root,generation=generation,counts={table:db.execute('SELECT count(*) FROM '+table).fetchone()[0] for table in ('model_generation','provider_usage','tool_invocation','dispatch_observation','run_trace')})
            tree=h.http('/api/developer/observations?'+urlencode(dict(run=main)))
            require(sum(o['name']=='Provider HTTP' for o in tree['items'])>=2,'actual sends missing from observation tree')
            round_tree=h.http('/api/developer/observations?'+urlencode(dict(run=main,obs='generation:'+generation)))
            generation_details=round_tree['generation']
            require(generation_details['model']=='fixture-model' and generation_details['metrics']['known_usage']==1
                    and generation_details['metrics']['input_tokens']==7 and generation_details['metrics']['output_tokens']==8,
                    'selected generation metrics do not match the actual provider usage')
            root_input=h.http('/api/developer/observation-body?'+urlencode(dict(run=root,obs='agent:'+root,field='input')))
            require(root_input['available'] and server.task_description in root_input['content'], 'Worker input lost its original frozen task brief')
            timings=round_tree['provider_timings']
            require(round_tree['trace']['metrics'].get('ttft_ms',-1)>=0,'tool-only first generation lost trace TTFT')
            phases=[x['phase'] for x in timings]
            require(phases[:5]==['started','headers','started','headers','first_body']
                    and sorted(phases[5:])==['first_tool_call','stream_terminal'],
                    'retry and stream milestones are missing from generation details: '+repr(phases))
            require([x['http_status'] for x in timings if x['phase']=='headers']==[503,200],
                    'stream diagnostics lost the HTTP outcomes')
            h.stop(kill=True);h.start()
            require(h.http('/api/developer/observations?'+urlencode(dict(run=main)))==tree,'completed trace changed across restart')
            h.env['STEWARD_NATIVE_TRACE_FACTS']=json.dumps(facts)
            h.browser(evidence.parent/'trace-ui','native-trace')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.execute('BEGIN')
                after={table:db.execute('SELECT count(*) FROM '+table).fetchone()[0] for table in facts['counts']}
            require(after==facts['counts'],'browsing tracing mutated execution or observations: '+json.dumps(dict(before=facts['counts'],after=after)))
            report['scenarios']['native-traces']=dict(read_only=True,actual_attempts=2,restart=True)
            report['checks'].append('Langfuse-style trace tree/timeline/details; actual Go provider final HTTP bodies and retries, terminal restart without re-execution, missing usage/pricing, authenticated browser, responsive/no-JS and unchanged ledgers')
        finally:
            h.cleanup();server.shutdown();server.server_close();thread.join()


def inspection_relationships(binary, evidence, report):
    """Real Main -> Task -> Root -> recursive Child, read through Web only."""
    def plan(calls): return 'Capabilities: '+json.dumps(calls)
    leaf=plan([dict(name='mcp__fixture__echo',arguments=dict(text='Inspection leaf completed.'))])
    child=plan([dict(name='builtin__agent_run',arguments=dict(task=leaf))])
    root='Inspection Task <img src=x onerror="unsafe()">\n'+plan([dict(name='builtin__agent_run',arguments=dict(task=child))])
    with tempfile.TemporaryDirectory(prefix='steward-inspection-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform,'capabilities')
        try:
            h.setup(metadata=dict(name='inspection fixture',provider_kind='openai_compatible',
                endpoint='http://127.0.0.1:1/v1',wire_api='openai_completions',model_id='fixture-model',
                context_window=65536,max_output_tokens=4096,generation_defaults={},
                compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
            h.start('after_generation_commit')
            h.wait_state(lambda status:status['readiness']=='ready','inspection runtime readiness',lambda _:h.http('/api/status'))
            h.http('/api/message',dict(source_id='inspect-tree',content=[dict(type='text',text=plan([
                dict(name='builtin__task_manage',arguments=dict(operation='create',task=root))]))]))
            def first_accepted(_):
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    return db.execute("SELECT count(*) FROM model_generation WHERE status='completed'").fetchone()[0]==1
            h.wait_state(first_accepted,'accepted model result before checkpoint',timeout=10)
            h.stop(kill=True);h.start()
            h.wait(lambda s:sum(r['worker_position']=='child' for r in s['runs'])==2
                   and all(r['status'] in ('completed','failed','cancelled') for r in s['runs'])
                   and all(d['status']=='sent' for d in s['deliveries']), 'completed inspection tree')
            # A real long user turn proves continuation through the actual Web
            # surface; no synthetic internal acceptance or checkpoint rewrite.
            h.http('/api/message',dict(source_id='inspect-long-context',content=[dict(type='text',text='Capabilities: []\n'+'完整上下文🦊 '*1000+'<img src=x onerror="unsafe()">LONG_CONTEXT_END')]))
            h.wait(lambda s:all(r['status'] in ('completed','failed','cancelled') for r in s['runs'])
                   and all(d['status']=='sent' for d in s['deliveries']), 'completed long context turn')
            def facts():
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    db.execute('BEGIN')
                    return dict(runs=[dict(zip(('id','role','parent','root'),row)) for row in db.execute(
                        "SELECT run_id,CASE WHEN kind='main' THEN 'main' ELSE worker_position END,coalesce(parent_run_id,''),coalesce(root_run_id,'') FROM run ORDER BY created_at,run_id")],
                        task=db.execute('SELECT task_ref FROM task').fetchone()[0],
                        long_context=dict(zip(('run','step','item'),db.execute("SELECT g.run_id,g.step_index,cast(j.key AS integer) FROM model_generation g,json_each(g.request_json,'$.messages') j WHERE json_extract(j.value,'$.text') LIKE '%LONG_CONTEXT_END%' ORDER BY g.created_at DESC LIMIT 1").fetchone())),
                        frozen_hash=hashlib.sha256(json.dumps(db.execute('SELECT generation_id,request_hash,request_json,result_json FROM model_generation ORDER BY generation_id').fetchall()).encode()).hexdigest(),
                        main=db.execute("SELECT i.run_id FROM task_request q JOIN tool_invocation i USING(invocation_id) WHERE json_extract(i.arguments_json,'$.operation')='create'").fetchone()[0],
                        counts={table:db.execute('SELECT count(*) FROM '+table).fetchone()[0] for table in
                                ('run','task','tool_invocation','task_request','task_invocation','model_generation','run_trace','event_outbox')})
            original=facts()
            h.http('/developer/observability',auth=False,expected=401)
            h.http('/api/developer/executions',auth=False,expected=401)
            h.http('/api/developer/executions?limit=101',expected=400)
            h.http('/api/developer/relationships?run=missing',expected=404)
            h.http('/api/developer/relationships?task=task:missing',expected=404)
            ids=[];cursor=''
            for _ in range(len(original['runs'])+1):
                page=h.http('/api/developer/executions?'+urlencode(dict(limit=1,cursor=cursor)))
                ids.extend(r['run_id'] for r in page['items']);cursor=page.get('next','')
                if not cursor:break
            require(len(ids)==len(set(ids))==len(original['runs']) and set(ids)=={r['id'] for r in original['runs']},'inspection pagination omitted or duplicated an execution')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                first=db.execute('SELECT generation_id FROM model_generation WHERE run_id=? ORDER BY step_index LIMIT 1',(original['main'],)).fetchone()[0]
            audited=[json.loads(line) for line in (h.root/'data/fake-provider-calls.jsonl').read_text().splitlines()]
            require(sum(row['generation_id']==first for row in audited)==1,'accepted model result dispatched twice after recovery')
            trace=h.http('/api/developer/observations?'+urlencode(dict(run=original['main'])))
            require(any(node['status']=='reused' for node in trace['items']),'accepted result reuse missing from trace')
            require(not any(node['name']=='Provider HTTP' for node in trace['items']),'scripted provider fabricated HTTP attempts')
            source=h.http('/api/developer/relationships?'+urlencode(dict(run=original['main'])))
            require(len(source['tasks'])==1 and source['tasks'][0]['task_ref']==original['task'] and source['tasks'][0]['operation']=='create','Main creation receipt missing')
            tree=h.http('/api/developer/relationships?'+urlencode(dict(task=original['task'])))
            actual={r['run_id']:(r['role'],r.get('parent_run_id',''),r.get('root_run_id','')) for r in tree['executions']}
            require(actual=={r['id']:(r['role'],r['parent'],r['root']) for r in original['runs'] if r['role']!='main'},'inspection tree differs from authoritative Run links')
            require(tree['creator']['run_id']==original['main'],'Task origin lost')
            require(all(parent!=original['main'] for _,parent,_ in actual.values()),'Root mislabeled as Main Child')
            inspect_frozen_context(h)
            h.env['STEWARD_INSPECTION_FACTS']=json.dumps(original)
            h.browser(evidence.parent/'inspection-ui','inspection')
            require(facts()==original,'opening inspection changed execution or domain records')
            h.stop(kill=True);h.start()
            recovered=h.http('/api/developer/relationships?'+urlencode(dict(task=original['task'])))
            require(recovered==tree and facts()==original,'inspection restart rewrote or reran completed work')
            inspect_frozen_context(h)
            report['scenarios']['inspection']=dict(source=source,tree=tree,paginated_ids=ids,read_only=True,restart=True)
            report['checks'].append('Developer Observability: actual Main/Task/Root/recursive Child, paged metadata, original receipt causality, browser filters/navigation/escaping/mobile, read-only restart')
            report['checks'].append('Frozen context: ordered stored messages/tools/responses, recorded provenance, adjacent input diff, complete UTF-8 content, authenticated browser and restart without re-execution')
        finally:h.cleanup()


def inspect_frozen_context(h):
    """Compare the HTTP projection with the actual frozen Host records."""
    def content(run,step,section,index):
        parts=[];offset=0
        for _ in range(1000):
            view=h.http('/api/developer/context?'+urlencode(dict(run=run,step=step,section=section,item=index,offset=offset,format='json')))
            require(len(view['content'].encode())<=8192,'unbounded context part')
            parts.append(view['content'])
            if 'next_offset' not in view:return json.loads(''.join(parts))
            require(view['next_offset']>offset,'context continuation did not advance')
            offset=view['next_offset']
        raise AssertionError('context continuation never completed')
    h.http('/api/developer/rounds?run=missing',auth=False,expected=401)
    h.http('/api/developer/context?run=missing&step=0',expected=404)
    h.http('/api/developer/context?run=missing&step=-1',expected=400)
    with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
        frozen=db.execute('SELECT run_id,step_index,request_json,result_json,status FROM model_generation ORDER BY run_id,step_index').fetchall()
        for run in {row[0] for row in frozen}:
            rounds=h.http('/api/developer/rounds?'+urlencode(dict(run=run)))
            require([r['step'] for r in rounds['items']]==[row[1] for row in frozen if row[0]==run],'round order differs from frozen history')
        for run,step,raw,result,status in frozen:
            request=json.loads(raw)
            for section in ('messages','tools'):
                items=request.get(section,[])
                if not items:continue
                view=h.http('/api/developer/context?'+urlencode(dict(run=run,step=step,section=section)))
                require([item['index'] for item in view['items']]==list(range(len(items))),'context order differs from snapshot')
                for index,expected in enumerate(items):
                    require(content(run,step,section,index)==expected,'HTTP inspection changed frozen '+section)
                diff=h.http('/api/developer/context-diff?'+urlencode(dict(run=run,step=step,section=section)))
                require(all(change['kind'] in ('added','removed','changed','moved') for change in diff['items']),'unknown diff semantics')
            if status=='completed':require(content(run,step,'response',0)==json.loads(result),'response or tool calls differ from accepted result')
            provenance=db.execute('SELECT provenance_json FROM context_compaction WHERE run_id=? AND step_index=?',(run,step)).fetchone()
            if provenance:require(content(run,step,'provenance',0)==json.loads(provenance[0]),'compaction provenance rewritten')


def dependency_recovery(binary, report):
    with tempfile.TemporaryDirectory(prefix='steward-channel-repair-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        h = ChannelHarness(binary, Path(temp), platform)
        try:
            h.setup()
            h.production_events = True
            h.env['STEWARD_NATS_URL'] = broker.url
            catalog = h.root / 'repaired-skills'
            with h.config.open('a') as config:
                config.write('skills:\n  root: ' + str(catalog) + '\n  scope: agent\n')
            h.start()
            require('href="/settings/models/new"' in h.http('/settings/models'), 'dependency failure hid model settings in Resources')
            require('<h1>Execution records</h1>' in h.http('/tasks'), 'dependency failure hid Progress')
            require(h.http('/api/status')['readiness'] != 'ready', 'broken runtime admitted work')
            h.http('/api/message', dict(source_id='blocked', content=[dict(type='text', text='blocked')]), expected=409)
            catalog.mkdir(mode=0o700)
            h.wait_state(lambda status: status['readiness'] == 'ready', 'dependency repair reopened ingress', reader=lambda _: h.http('/api/status'))
            h.http('/api/message', dict(source_id='repaired', content=[dict(type='text', text='Continue after repair.')]))
            h.wait(lambda state: any(row['status'] == 'sent' for row in state['deliveries']), 'repaired runtime reply')
            # Repeated UI reads must not create traces, projections or intents.
            h.wait(lambda state: all(row['status'] in ('completed', 'failed') for row in state['runs']), 'settled reads')
            before = h.rows()
            for path in ('/settings', '/progress', '/', '/api/conversation'):
                h.http(path)
            after = h.rows()
            require(before == after, 'GET mutated authoritative state')
            report['scenarios']['dependency-repair'] = dict(status='passed', state=after)
            report['checks'].append('Unavailable optional dependency keeps control pages usable; in-process repair opens ingress; GET leaves settled state unchanged')
            h.stop()
        finally:
            h.cleanup()


def rich_replies(binary, evidence, report):
    """Generate real replies through Main/Bridge/Delivery, then answer in Web.

    No projection or Conversation rows are inserted by this scenario.
    """
    from http.server import ThreadingHTTPServer
    from provider_endpoint import Endpoint
    with tempfile.TemporaryDirectory(prefix='steward-rich-replies-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        server = ThreadingHTTPServer(('127.0.0.1',0),Endpoint)
        server.requests,server.mode=[], 'success'
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        h = ChannelHarness(binary, Path(temp), platform, '')
        try:
            h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
            h.setup(metadata=dict(name='Native UI tool fixture', provider_kind='openai_compatible',
                                  endpoint=f'http://127.0.0.1:{server.server_port}/v1', wire_api='openai_completions',
                                  model_id='fixture-model', context_window=32768, max_output_tokens=4096,
                                  generation_defaults={}, compatibility=dict(tool_result_format='openai'),
                                  capabilities=dict(text_input=True)))
            result = dict(title="Trip comparison", fallback='Compare A and B. A contains literal markup; B has value 42. Choose a review time.', components=[
                dict(name='Table',props=dict(title='Trip comparison',columns=['Option','Result'],rows=[['A','<img src=x onerror=alert(1)>'],['B','42']],source='User-provided fixture values')),
                dict(name='barChart',props=dict(title='Values',description='User-provided fixture values',data=[dict(label='A',value=10),dict(label='B',value=42)])),
                dict(name='pieChart',props=dict(title='Shares',description='User-provided fixture values',data=[dict(label='A',value=10),dict(label='B',value=42)])),
                dict(name='plan_visualization',props=dict(approach='Compare the supplied values.',technology='Native components',key_elements=['Table','Charts'])),
                dict(name='scheduleTime',props=dict(reasonForScheduling='Review the comparison',meetingDuration=30,timeSlots=[dict(date='2026-12-01',time='10:00'),dict(date='2026-12-02',time='14:00')])),
                dict(name='scheduleTime',props=dict(reasonForScheduling='Follow-up review',meetingDuration=15,timeSlots=[dict(date='2026-12-03',time='10:00'),dict(date='2026-12-04',time='14:00')])),
            ])
            result['components'].append(dict(name='generateSandboxedUi',props=dict(initialHeight=160,placeholderMessages=[],css='',html='<label>Scenario level <input id="level" type="range" min="0" max="100" value="20"></label><output id="level-value">20</output>',jsFunctions='function initialize(){document.querySelector("#level").addEventListener("input",e=>document.querySelector("#level-value").textContent=e.target.value)}',jsExpressions=['initialize()'])))
            server.widgets=[result];server.mode='intelligent-ui'
            h.start()
            h.wait_state(lambda status:status['readiness']=='ready','native UI runtime readiness',lambda _:h.http('/api/status'))
            source = 'SHOW_INTELLIGENT_UI: Compare these options and ask for the trip details.'
            h.http('/api/message', dict(source_id='rich-reply-request', content=[dict(type='text', text=source)]))
            initial = h.wait(lambda s: len(s['deliveries']) == 1 and s['deliveries'][0]['status'] == 'sent', 'normal Main rich reply delivered')
            history = h.http('/api/conversation')
            message = next(m for m in history if m['role'] == 'assistant')
            projection = json.loads(initial['projections'][0]['spec_json'])
            (evidence.parent / 'rich-generation.json').write_text(json.dumps(dict(history=history, projection=projection, failure_state=h.failure_state()), ensure_ascii=False, indent=2) + '\n')
            kinds = [c['type'] for c in projection['components']]
            require([kind for kind in kinds if kind!='markdown'] == ['openintelligentui']*6+['widget'], f'ordinary reply did not generate controlled components: {kinds!r}')
            forms = [c for c in projection['components'] if c.get('name') == 'scheduleTime']
            require(all(c['message_id'] == message['message_id'] and len(c['form_id']) == 64 for c in forms), 'forms not bound to the actual reply')
            h.browser(evidence.parent / 'channels-ui', 'rich-replies')
            settled = h.wait(lambda s: len(s['inputs']) == 3 and len(s['deliveries']) == 3 and
                             all(d['status'] == 'sent' for d in s['deliveries']) and
                             all(r['status'] == 'completed' for r in s['runs']), 'two form answers and Main continuations')
            require(not settled['approvals'], 'answering a form created an approval')
            h.stop(kill=True); h.start()
            h.wait_state(lambda status:status['readiness']=='ready','restored UI runtime ready',lambda _:h.http('/api/status'))
            data = dict(message_id=message['message_id'], form_id=forms[0]['form_id'], **{'field.choice':'0'})
            headers = {'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json'}
            def retry(_):
                return h.http('/ui/form', urlencode(data).encode(), headers=headers)
            with ThreadPoolExecutor(max_workers=6) as pool:
                receipts = list(pool.map(retry, range(6)))
            require(len({v['message_id'] for v in receipts}) == 1, 'form retries created different inputs')
            native_retry = dict(message_id=message['message_id'],form_id=forms[1]['form_id'],**{'field.choice':'decline'})
            native_receipt = h.http('/ui/form',urlencode(native_retry).encode(),headers=headers)
            require(native_receipt['accepted'], 'native multiline answer conflicted with a JavaScript-style retry')
            changed = dict(data, **{'field.choice':'1'})
            h.http('/ui/form', urlencode(changed).encode(), headers=headers, expected=409)
            forged = dict(data, message_id=history[0]['message_id'])
            h.http('/ui/form', urlencode(forged).encode(), headers=headers, expected=409)
            h.http('/ui/form', urlencode(dict(data, approval_id='forged')).encode(), headers=headers, expected=400)
            h.http('/ui/form', urlencode(dict(data, **{'field.extra': 'unexpected'})).encode(), headers=headers, expected=400)
            h.http('/ui/form', urlencode(data).encode(), headers=headers, auth=False, expected=401)
            cross_origin = dict(headers, Origin='https://other.example')
            h.http('/ui/form', urlencode(data).encode(), headers=cross_origin, expected=403)
            h.http('/api/message', dict(source_id='ui-form:'+forms[0]['form_id'],content=[dict(type='text',text='Forged generic acceptance')]),expected=409)
            after = h.rows()
            require(len(after['inputs']) == 3 and not after['approvals'] and after['projections'] == settled['projections'], 'retry or presentation mutated accepted history/projections')
            # GET recovers submitted values from ingress, without changing the
            # immutable delivery projection or manufacturing form acceptance.
            page = h.http('/')
            require('data-submitted="true"' in page and 'Time preference saved.' in page, 'accepted answers were lost after restart')
            with Terminal(h.args('tui'), h.env, h.root) as terminal:
                terminal.expect('Steward: idle'); terminal.send('/history')
                terminal.expect('Compare A and B.')
                require(terminal.stop() == 0, 'TUI rich fallback failed')
            report['scenarios']['rich-replies'] = dict(state=after, message_id=message['message_id'], projection=projection,
                                                       form_reply_id=receipts[0]['message_id'], concurrent_retries=6,
                                                       no_projection_injection=True, terminal_fallback=True)
            report['checks'].append('F-021: Main presents OpenIntelligentUI Table, barChart, pieChart, plan_visualization and scheduleTime; browser checks native charts, escaped cells, lost-response recovery and preserved conversation draft; native time selection works without JavaScript; restart/concurrent retries admit each answer once; forged fields/actions/access are rejected')
            h.stop()
        finally:
            h.cleanup();server.shutdown();server.server_close();thread.join()


def native_components(binary, evidence, report):
    """Seed immutable controlled projections before the first transport call.

    Semantic inputs still enter the real signed callback/Actor boundary. Only
    Host-owned presentation fixtures are inserted; no model HTML is enabled.
    """
    with tempfile.TemporaryDirectory(prefix='steward-channel-components-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform)
        try:
            h.setup();h.start('before_delivery_send')
            h.callback('native-table','Compare these two choices.')
            with selectors.DefaultSelector() as selector:
                selector.register(h.process.stdout,selectors.EVENT_READ)
                require(selector.select(15),'native component fixture did not reach transport boundary')
            require(json.loads(h.process.stdout.readline())['boundary']=='before_delivery_send','wrong component fixture boundary')
            h.callback('unknown-component','Retain this semantic answer.')
            h.wait(lambda s:len(s['deliveries'])==2,'unknown component semantic reply')
            h.callback('invalid-table','Retain this answer when table rendering fails.')
            h.wait(lambda s:len(s['deliveries'])==3,'component fixture semantic replies')
            h.callback('empty-projection','Keep the original text for an empty projection.')
            h.wait(lambda s:len(s['deliveries'])==4,'empty projection semantic reply')
            h.callback('unknown-version','Keep the original text for an unknown version.')
            h.wait(lambda s:len(s['deliveries'])==5,'unknown version semantic reply')
            messages=[m for m in h.http('/api/conversation') if m['role']=='assistant']
            artifact=h.command('artifact','save','markdown-test.html','text/html',text='<h1>Downloaded artifact</h1>')
            markdown='**Markdown rendered**\n\n[Download game]('+artifact['artifact_ref']+')\n\n[Unsafe](javascript:alert(1))\n\n<img src=x onerror=alert(1)>\n\n```mermaid\nflowchart LR\n  A[Request] --> B[Worker]\n```'
            specs=[
                dict(schema_version=1,fallback='Native table semantic fallback',components=[dict(type='openintelligentui',name='Table',props=dict(title='Native comparison',columns=['Option','Result'],rows=[['A','<img src=x onerror=alert(1)>'],['B','Exact value: 42']],source='User-provided fixture')),dict(type='markdown',text=markdown)]),
                dict(schema_version=1,fallback='Unknown component semantic fallback',components=[dict(type='react',text='<script>alert(1)</script>')]),
                dict(schema_version=1,fallback='Invalid table semantic fallback',components=[dict(type='table',columns=['One'],rows=[['too','many']])]),
                dict(schema_version=1,fallback='Untrusted projection fallback',components=[]),
                dict(schema_version=99,fallback='Untrusted projection fallback',components=[]),
            ]
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                for message,spec in zip(messages,specs):
                    canonical=json.dumps(message,sort_keys=True,separators=(',',':'),ensure_ascii=False).replace('&','\\u0026').replace('<','\\u003c').replace('>','\\u003e')
                    identity=hashlib.sha256(canonical.encode()).hexdigest()
                    delivery_id=db.execute('SELECT delivery_id FROM delivery_request WHERE message_id=?',(message['message_id'],)).fetchone()[0]
                    db.execute('INSERT INTO delivery_projection VALUES(?,?,?,?)',(delivery_id,identity,1,json.dumps(spec)))
            h.browser(evidence.parent/'channels-ui','components')
            require(h.http('/api/conversation')[-1]==messages[-1],'presentation changed semantic Conversation')
            report['scenarios']['native-components']=dict(state=h.rows(),specs=specs,conversation_unchanged=True)
            report['checks'].append('UI: bundled ad-agent components, OpenIntelligentUI native table, Steward wordmark/avatar, desktop/mobile layouts; escaped cells, unknown/invalid component fallback; no external frontend requests')
            h.stop()
        finally:h.cleanup()


def early_composer(binary, evidence, report):
    with tempfile.TemporaryDirectory(prefix='steward-channel-early-composer-') as temp, Sandbox() as platform:
        h=ChannelHarness(binary,Path(temp),platform)
        try:
            h.start()
            h.browser(evidence.parent/'channels-ui','setup')
            state=h.wait(lambda s:len(s['inputs'])==1 and len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','early draft accepted once with attachment')
            history=h.http('/api/conversation')
            require(any(p.get('artifact_ref') for m in history if m['role']=='user' for p in m['content']),'early attachment did not reach Conversation')
            report['scenarios']['early-composer']=dict(state=state,conversation=history)
            report['checks'].append('Composer: delayed local bundle preserves early server-rendered draft and attachment, then submits through the real form once')
            h.stop()
        finally:h.cleanup()


def delivery_leases(binary, report):
    """Exercise the same foreground delivery path beyond the default 5s lease."""
    for name, fixture, send_delay, upload_delay in (
            ('slow-send', 'main', 7, 0),
            ('slow-upload', 'artifacts', 0, 6),
            ('cumulative-multipart', 'artifacts', 2, 2)):
        with tempfile.TemporaryDirectory(prefix='steward-channel-lease-') as temp, Sandbox(send_delay, upload_delay) as platform:
            h = ChannelHarness(binary, Path(temp), platform, fixture)
            try:
                h.setup(); h.start()
                content = 'Slow delivery must retain its original fenced owner.'
                text = content if fixture == 'main' else json.dumps(dict(name='slow.txt', mediaType='text/plain', text=content))
                started = time.monotonic()
                h.callback(name, text)
                require(platform.send_started.wait(10), 'slow delivery did not reach platform')
                competitor = None
                if name == 'slow-send':
                    with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                        # Renewal leaves updated_at/version unchanged. Derive
                        # the original expiry even if a heartbeat already ran.
                        initial_expiry, token = db.execute('SELECT d.updated_at+5000000,o.claim_token FROM delivery_outbox o JOIN delivery_request d USING(delivery_id)').fetchone()
                    h.wait(lambda _: time.time() > initial_expiry/1_000_000 + .1, 'past original claim expiry')
                    require(platform.evidence()['operations'][0]['finished_at'] is None, 'slow send finished before competing scan')
                    h.command('delivery', 'work')
                    with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                        expiry, current = db.execute('SELECT claim_expires_at,claim_token FROM delivery_outbox').fetchone()
                    require(current == token and expiry > initial_expiry, 'competing consumer took a live renewed claim')
                    competitor = dict(same_owner=True, initial_expiry=initial_expiry, renewed_expiry=expiry)
                state = h.wait(lambda s: len(s['deliveries']) == 1 and s['deliveries'][0]['status'] == 'sent', name+' settled')
                elapsed = time.monotonic() - started
                ledger = platform.evidence()
                expected = 1 if fixture == 'main' else 2
                require(elapsed > 5 and len(state['receipts']) == expected and len(ledger['send_attempts']) == expected, 'slow delivery lost or repeated a part')
                require(state['deliveries'][0]['attempt_count'] == 1, 'slow send became another attempt')
                require(h.process.poll() is None and h.http('/api/status')['readiness'] == 'ready', 'slow delivery stopped ingress')
                if fixture == 'artifacts':
                    require(len(ledger['uploads']) == 1 and ledger['uploads'][0]['name'] == 'slow.txt' and ledger['uploads'][0]['sha256'] == hashlib.sha256(content.encode()).hexdigest(), 'slow multipart changed attachment')
                report['scenarios'][name] = dict(state=state, platform=ledger, elapsed_seconds=elapsed, competing_scan=competitor)
                h.stop()
            finally:
                h.cleanup()

    # Expire a live sender after the platform recorded the effect, then use the
    # real CLI as a competing recovery owner. The old HTTP call must cancel and
    # join without taking down Web ingress; uncertainty is still query-only.
    with tempfile.TemporaryDirectory(prefix='steward-channel-lost-claim-') as temp, Sandbox(send_delay=6) as platform:
        h = ChannelHarness(binary, Path(temp), platform)
        try:
            h.setup(); h.start()
            h.callback('lost-claim', 'Keep ingress alive if this transport loses its claim.')
            require(platform.send_started.wait(10), 'lost-claim send did not reach platform')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                db.execute('UPDATE delivery_outbox SET claim_expires_at=0')
            h.command('delivery', 'work')
            h.wait(lambda s: s['deliveries'][0]['status'] == 'unknown', 'competing recovery query')
            h.http('/api/message', dict(source_id='after-lost-claim', content=[dict(type='text', text='Web still accepts unrelated conversation input.')]))
            state = h.wait(lambda s: len(s['deliveries']) == 2 and any(d['channel'] == 'web' and d['status'] == 'sent' for d in s['deliveries']), 'ingress and delivery survive ownership loss')
            h.wait(lambda _: platform.evidence()['operations'][0]['finished_at'] is not None, 'late platform response finished')
            h.stop(kill=True)
            prior = next(d for d in h.rows()['deliveries'] if d['channel'] == 'feishu')['version']
            h.start()
            state = h.wait(lambda s: any(d['channel'] == 'feishu' and d['status'] == 'unknown' and d['version'] >= prior+2 for d in s['deliveries']), 'unknown query settled after restart')
            ledger = platform.evidence()
            require(len(ledger['send_attempts']) == 1 and len(ledger['messages']) == 1, 'competing recovery resent unknown side effect')
            feishu_id = next(d for d in state['deliveries'] if d['channel'] == 'feishu')['delivery_id']
            require(not any(r['delivery_id'] == feishu_id for r in state['receipts']), 'lost sender committed a late channel receipt')
            require(h.process.poll() is None and h.http('/api/status')['readiness'] == 'ready', 'late sender or recovery stopped ingress')
            report['scenarios']['lost-claim-competing-recovery'] = dict(state=state, platform=ledger)
            h.stop()
        finally:
            h.cleanup()
    report['checks'].append('Delivery lease: slow send, slow upload and cumulative multipart settle under one renewed claim; competing live scan excluded; lost owner cancels without stopping ingress, restart keeps unknown query-only with one remote send')


def extra_scenarios(binary,report):
    # Outbound attachment: semantic Artifact -> same DeliveryRequest -> real
    # multipart upload + bound-recipient platform message -> durable receipts.
    for lost_ack in (False, True):
        with tempfile.TemporaryDirectory(prefix='steward-channel-output-') as temp, Sandbox() as platform:
            h=ChannelHarness(binary,Path(temp),platform,'artifacts')
            try:
                h.setup();platform.drop_response=lost_ack;h.start()
                content='Final wedding checklist: 128 guests; confirm seats per table.'
                h.callback('output',json.dumps(dict(name='checklist.txt',mediaType='text/plain',text=content)))
                status='unknown' if lost_ack else 'sent'
                state=h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']==status,'outbound attachment receipt')
                if not lost_ack:
                    uploads=platform.evidence()['uploads']
                    require(len(uploads)==1 and uploads[0]['name']=='checklist.txt' and uploads[0]['sha256']==hashlib.sha256(content.encode()).hexdigest(),'wrong outbound attachment')
                    require(len(platform.evidence()['messages'])==2 and len(state['receipts'])==2,'attachment parts lost or duplicated')
                before=platform.evidence()
                require(len(before['send_attempts'])==(1 if lost_ack else 2),'unexpected remote send attempts before restart')
                h.stop(kill=True)
                prior_version=h.rows()['deliveries'][0]['version']
                h.start()
                h.callback('output',json.dumps(dict(name='checklist.txt',mediaType='text/plain',text=content)),event='output-retry')
                if lost_ack:
                    h.wait(lambda s:s['deliveries'][0]['status']=='unknown' and s['deliveries'][0]['version']>=prior_version+2,'unknown delivery was queried after restart')
                require(h.rows()['deliveries'][0]['status']==status and platform.evidence()==before,'uncertain effect or sent attachment retried')
                report['scenarios']['lost-remote-ack' if lost_ack else 'outbound-attachment']=dict(state=h.rows(),platform=platform.evidence())
                h.stop()
            finally:h.cleanup()
    report['checks'].append('Outbound Artifact multipart bytes and recipient verified; sent multipart receipts survive restart; ambiguous remote send stays unknown without resend')

    # SIGKILL immediately before and after the input transaction, and after a
    # channel accepted delivery but before outbox acknowledgement.
    for boundary in ('before_channel_input_commit','after_channel_input_commit','after_delivery_send'):
        with tempfile.TemporaryDirectory(prefix='steward-channel-kill-') as temp, Sandbox() as platform:
            h=ChannelHarness(binary,Path(temp),platform)
            try:
                h.setup();h.start(boundary)
                def invoke():
                    try:h.callback('crash-input','Recover the same channel message.')
                    except (OSError,HTTPError):pass
                with ThreadPoolExecutor(max_workers=1) as pool:
                    future=pool.submit(invoke)
                    with selectors.DefaultSelector() as selector:
                        selector.register(h.process.stdout,selectors.EVENT_READ)
                        require(selector.select(15),'missing channel crash boundary '+boundary)
                    require(json.loads(h.process.stdout.readline())['boundary']==boundary,'wrong crash boundary')
                    h.stop(kill=True);future.result(timeout=20)
                h.start();h.callback('crash-input','Recover the same channel message.',event='retry-after-kill')
                state=h.wait(lambda s:len(s['inputs'])==1 and len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','channel crash recovery')
                require(len(platform.evidence()['messages'])==1 and len(platform.evidence()['send_attempts'])==1,'channel crash duplicated send attempt or notification')
                report['scenarios'][boundary]=dict(state=state,platform=platform.evidence())
                h.stop()
            finally:h.cleanup()
    report['checks'].append('Real SIGKILL before/after inbound commit and after remote delivery receipt: one accepted message, one execution and one notice after restart')

    # Exercise Task continuity and notification deduplication through the transports.
    for case,fixture in (('task-continuation','tasks'),('scheduled-delivery','automation')):
        with tempfile.TemporaryDirectory(prefix='steward-channel-'+case+'-') as temp, Sandbox() as platform:
            h=ChannelHarness(binary,Path(temp),platform,fixture)
            try:
                h.setup()
                if case=='scheduled-delivery':h.now='2026-10-15T12:00:00+08:00'
                h.start()
                if case=='task-continuation':
                    h.callback(case,'Delegate: Prepare the wedding venue checklist with material dependencies; contact nobody.')
                    h.wait(lambda s:len(s['runs'])>=3 and all(r['status']=='completed' for r in s['runs']),'Task completion')
                    h.http('/api/message',dict(source_id='wedding-update',content=[dict(type='text',text='Continue: wedding | Guests confirmed: 128. Preserve the checklist and ask before contacting the hotel.')]))
                    h.wait(lambda s:len([r for r in s['runs'] if r['kind']=='worker'])==2 and all(r['status']=='completed' for r in s['runs']) and len(s['deliveries'])==4 and all(d['status']=='sent' for d in s['deliveries']),'Task continuation')
                    with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                        tasks=list(db.execute('SELECT task_id,anchor_task,task_epoch FROM task'))
                        instructions=list(db.execute('SELECT instruction FROM task_instruction'))
                    require(len(platform.evidence()['messages'])==3,'wedding notices did not use bound/default Feishu')
                    require(len(tasks)==1 and tasks[0][2]==2 and len(instructions)==1 and '128' in instructions[0][0],'wedding continuity broken')
                    history=h.http('/api/conversation')
                else:
                    q=dict(operation='create',instruction='Check HKG-CDG 2026-12-01 daily price; notify only a new entry below threshold.',triggerType='cron',triggerSpec='0 8 * * *',timezone='Asia/Shanghai',below=1800)
                    h.callback(case,'Automation: '+json.dumps(q))
                    h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','Automation accepted over channel')
                    h.stop()
                    for i,expected in enumerate((1,1,2,2,2,3),16):
                        h.now=f'2026-10-{i:02d}T08:00:00+08:00';h.start()
                        h.wait(lambda s:len([r for r in s['runs'] if r['kind']=='worker' and r['status']=='completed'])==i-15 and len(s['deliveries'])==expected and all(d['status']=='sent' for d in s['deliveries']),'quiet threshold day')
                        require(len(platform.evidence()['messages'])==expected,'threshold notification storm')
                        h.stop()
                    with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                        require(db.execute("SELECT count(*) FROM task WHERE status='active'").fetchone()[0]==1,'ongoing monitor completed')
                    h.start();history=h.http('/api/conversation')
                report['scenarios'][case]=dict(status='passed',state=h.rows(),conversation=history,platform=platform.evidence())
                h.stop()
            finally:h.cleanup()
    report['checks'].append('Task continuation across Feishu/Web; real zoned scheduling to default Feishu with only two threshold notices')


def account_presentation(binary, browser_evidence, report):
    # Reuse the account harness' owned OAuth network substitution and SecretStore.
    # The production Bridge/Manager is real; no login token or auth URL is evidence.
    from verify_account import account_config, Secrets
    from fixture_pool import current
    from event_broker import Broker
    scope=current()
    fixture=scope.start(['node',str(ROOT/'provider-bridge/test/account-fixture.mjs')],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        with selectors.DefaultSelector() as selector:
            selector.register(fixture.stdout,selectors.EVENT_READ)
            require(selector.select(10),'OAuth fixture not ready')
        endpoint=fixture.stdout.readline().strip()
        with tempfile.TemporaryDirectory(prefix='ec-auth-',dir='/tmp') as temp, Broker(Path(temp)) as broker:
            root=Path(temp);socket=root/'secrets.sock'
            server=socketserver.ThreadingUnixStreamServer(str(socket),Secrets)
            server.values={'credential:web-owner':base64.b64encode(TOKEN.encode()).decode()}
            server.lock=threading.Lock();server.sensitive=set();os.chmod(socket,0o600)
            server.credential_reads = {}
            thread=threading.Thread(target=server.serve_forever);thread.start()
            h=object.__new__(ChannelHarness);MainHarness.__init__(h,binary,root,fixture='')
            h.env.update(STEWARD_NATS_URL=broker.url,STEWARD_NATS_STREAM_PREFIX='web_account')
            try:
                h.config=account_config(root,ROOT/'provider-bridge/bridge.mjs',endpoint)
                with h.config.open('a') as out:out.write('web:\n  listen: 127.0.0.1:0\n  token_ref: credential:web-owner\n')
                original=h.args
                h.args=lambda *args:original('-test-secret-store',str(socket),*args)
                h.env['STEWARD_WEB_TEST_TOKEN']=TOKEN;h.process=None;h.start()
                form={'Content-Type':'application/x-www-form-urlencoded'}
                def pending_login():
                    pending = h.wait_state(
                        lambda s: s['status'] == 'pending' and any(e['kind'] == 'open_url' for e in s['events']),
                        'UI-neutral account authorization event', lambda _: h.http('/api/auth'),
                    )
                    return pending, next(e['url'] for e in pending['events'] if e['kind'] == 'open_url')

                # The external browser can fail before it sends a callback.
                h.browser(root/'browser', 'account-signin')
                _, abandoned_url = pending_login()
                require('Cancel sign-in' in h.http('/settings/models/new?provider=chatgpt'), 'pending login has no way to cancel')
                h.http('/auth/cancel', b'', headers=form)
                cancelled = h.wait_state(lambda s: s['status'] == 'reauth_required',
                                         'cancelled Web login', lambda _: h.http('/api/auth'))
                require(not cancelled.get('account_id') and not h.command('model', 'accounts'),
                        'abandoned authorization created an account')
                require(h.http('/api/status')['readiness'] == 'setup_required', 'abandoned authorization changed readiness')
                retry_page = h.http('/settings/models/new?provider=chatgpt')
                require('Sign-in was not completed.' in retry_page and 'href="/settings/models/new?method=account"' in retry_page,
                        'cancelled authorization did not offer another login')
                require(abandoned_url not in retry_page and 'Continue sign-in in your browser' not in retry_page,
                        'cancelled authorization still offers the abandoned URL')
                h.http('/auth/login', b'', headers=form)
                pending, private_url = pending_login()
                require(private_url != abandoned_url, 'retry reused the abandoned authorization')
                request=Request(endpoint+'/fixture/authorize',data=json.dumps(dict(url=private_url)).encode(),headers={'Content-Type':'application/json'})
                with urlopen(request,timeout=10) as response:callback=json.load(response)['callback']
                with urlopen(callback,timeout=10) as response:require(response.status==200,'OAuth callback rejected')
                connected=h.wait_state(lambda s:s['status']=='connected','Web account login completes',lambda _:h.http('/api/auth'))
                aid=connected['account_id'];require(aid,'account metadata absent')
                h.http('/model/add',urlencode(dict(account=aid,model='hidden-model')).encode(),headers=form,expected=409)
                profile=h.command('model','list')[0]
                require(h.http('/api/status')['readiness']=='ready','Web account profile unavailable')
                with urlopen(endpoint+'/fixture/stats',timeout=10) as response:
                    require(json.load(response)['generations']==0,'account connection sent unnecessary test generations')
                detail = '/settings/models/' + profile['profile_id']
                add_page = '/settings/models/new?provider=chatgpt'
                require('Choose an account model' not in h.http(detail), 'add-model sign-in result appeared on existing model')
                request=Request(endpoint+'/fixture/control',data=json.dumps(dict(models=['fixture-account-model','fixture-account-backup']+[f'fixture-extra-{index:02}' for index in range(12)])).encode(),headers={'Content-Type':'application/json'})
                with urlopen(request,timeout=10) as response:require(response.status==200,'catalog fixture update failed')
                h.browser(browser_evidence, 'account-reconnect')
                pending_login()
                resources = '/settings/models'
                require('Waiting for sign-in' in h.http(resources),
                        'reconnect status was displayed outside the Resources account section')
                for view, visible in ((resources, True), (detail, False), ('/settings/models/new', False)):
                    updates = h.http('/api/updates?' + urlencode(dict(view=view)))
                    require(('Waiting for sign-in' in updates['regions']['signin-status']) == visible,
                            'live updates put reconnect status on the wrong page')
                h.http('/auth/cancel', urlencode(dict(return_to=resources)).encode(), headers=form)
                h.wait_state(lambda s:s['status']=='reauth_required', 'Resources reconnect cancels', lambda _:h.http('/api/auth'))
                def account_credential_reads():
                    with server.lock:
                        # The owner token and default Composio project probe are
                        # independent of the saved model account credential.
                        return sum(count for ref, count in server.credential_reads.items()
                                   if ref not in ('credential:web-owner', 'env:COMPOSIO_API_KEY'))

                reads_before = account_credential_reads()
                h.stop();h.start()
                require('Add another account' in h.http(add_page), 'account management has no explicit additional-account action')
                for _ in range(2):
                    h.http(resources)
                    h.http('/api/status')
                    for view in ('/', resources, '/settings/models/new'):
                        h.http('/api/updates?' + urlencode(dict(view=view)))
                require(account_credential_reads() == reads_before,
                        'startup, page load or polling read the account credential')
                catalog = h.http('/api/models')
                require(any(item['label']=='fixture-account-model' for item in catalog['choices'])
                        and account_credential_reads() > reads_before,
                        'composer catalog did not load authenticated models')
                require(len(h.command('model','list'))==2,'catalog reads created extra profiles')
                status=h.http('/api/status')
                choice='account:'+aid+':fixture-account-model'
                h.http('/model/choose',urlencode(dict(consent='yes',choice='account:'+aid+':hidden-model',config_version=status['config_version'])).encode(),headers={**form,'Accept':'application/json'},expected=422)
                h.http('/model/choose',urlencode(dict(consent='yes',choice=choice,config_version=status['config_version']-1)).encode(),headers={**form,'Accept':'application/json'},expected=409)
                require(h.http('/api/status')['active_model_profile_id']==profile['profile_id'],'rejected model selection changed active profile')
                h.http('/auth/logout',urlencode(dict(account=aid)).encode(),headers=form)
                require(h.command('model','accounts')[0]['status']=='logged_out','Web logout missed existing lifecycle')
                require('logged_out' in h.http(resources), 'signed-out account has no blocked status')
                h.http('/auth/login', urlencode(dict(account=aid, return_to=resources)).encode(), headers=form)
                _, reconnect_url = pending_login()
                request = Request(endpoint+'/fixture/authorize', data=json.dumps(dict(url=reconnect_url)).encode(),
                                  headers={'Content-Type':'application/json'})
                with urlopen(request, timeout=10) as response:
                    callback = json.load(response)['callback']
                with urlopen(callback, timeout=10) as response:
                    require(response.status == 200, 'OAuth reconnect callback rejected')
                h.wait_state(lambda s: s.get('account_id') == aid and s['status'] == 'connected',
                             'Web account reconnect completes', lambda _: h.http('/api/auth'))
                require('Test connection' not in h.http(resources) and 'Add account model' not in h.http(resources),
                        'account reconnect requires an unnecessary connection test')
                restored = h.http('/api/status')
                require(restored['readiness'] == 'ready' and restored['active_model_profile_id'] == profile['profile_id'],
                        'account reconnect did not automatically restore the existing model')
                restored_profile = next(item for item in h.command('model','list') if item['profile_id']==profile['profile_id'])
                require(restored_profile['status']=='ready','reconnected current profile retained stale auth state')
                with closing(sqlite3.connect(h.root/'data/state.db')) as db, db:
                    require(db.execute('SELECT count(*) FROM model_generation').fetchone()[0]==0 and
                            db.execute('SELECT count(*) FROM model_probe').fetchone()[0]==0,
                            'account reconnect generated inference or fabricated a probe')
                restored_page = h.http(resources)
                require('fixture-account-model' in h.http('/') and 'Test connection' not in restored_page and 'Use model' not in restored_page,
                        'restored model still exposes replaced actions')
                h.browser(browser_evidence/'restored', 'account-restored')
                # Price a real accepted response using the bundled catalog. Only
                # the owned OAuth/Responses peer is substituted; no ledger or
                # rendered result is injected by the browser fixture.
                request = Request(endpoint+'/fixture/control', data=json.dumps(dict(models=['fixture-account-model','gpt-6-luna'])).encode(), headers={'Content-Type':'application/json'})
                with urlopen(request, timeout=10) as response:
                    require(response.status == 200, 'pricing model catalog setup failed')
                h.command('model','account-catalog',aid)
                priced = h.command('model','account-add',aid,'gpt-6-luna')
                h.http('/model/select', urlencode(dict(profile=priced['profile_id'], profile_version=priced['version'], config_version=h.http('/api/status')['config_version'], consent='yes')).encode(), headers=form)
                h.http('/api/message',dict(source_id='automatic-cost',content=[dict(type='text',text='Summarize the available evidence.')]))
                h.wait(lambda state:len(state['runs'])==1 and state['runs'][0]['status']=='completed' and all(d['status']=='sent' for d in state['deliveries']), 'automatically priced account response')
                traces=h.http('/api/developer/traces')['items']
                require(len(traces)==1 and traces[0]['metrics']['unpriced']==0, 'known account usage remained unpriced')
                costs=traces[0]['metrics']['costs']
                require(len(costs)==1 and costs[0]['currency']=='USD' and costs[0]['estimated'] and costs[0]['micros']>0 and 'openai-codex' in costs[0]['source'], 'automatic price amount or provenance is missing')
                with closing(sqlite3.connect(root/'data/state.db')) as db, db:
                    db.execute('BEGIN')
                    require(db.execute('SELECT count(*) FROM generation_cost_estimate').fetchone()[0]==db.execute('SELECT count(*) FROM model_generation').fetchone()[0], 'accepted usage estimate count diverged')
                    require(db.execute('SELECT count(*) FROM spend_reservation').fetchone()[0]==0 and db.execute('SELECT count(*) FROM spend_approval').fetchone()[0]==0, 'subscription reference costs entered monetary consent')
                h.browser(browser_evidence/'estimated-cost','estimated-cost')
                h.stop(kill=True);h.start()
                require(h.http('/api/developer/traces')['items'][0]['metrics']['costs']==costs, 'restart repriced accepted usage')
                report['checks'].append('Automatic catalog price: actual accepted account usage, immutable estimates across restart, no monetary consent mutation, visible amount/source and compact cost-cell geometry in desktop/mobile light/dark browser views')
                h.browser(browser_evidence/'signout','settings-account')
                with server.lock:
                    sensitive=list(server.sensitive)
                for path in (root/'data').rglob('*'):
                    if path.is_file():require(all(value not in path.read_bytes() for value in sensitive),'credential persisted outside SecretStore')
                report['scenarios']['account-auth']=dict(status='connected_then_logged_out',abandoned_login_recovered=True,event_kinds=[e['kind'] for e in pending['events']],profile_id=profile['profile_id'],credential_scan='clean')
                report['checks'].append('Web account login uses real Manager/Bridge against owned OAuth Fake; direct popup and manual fallback, cancel without callback, fresh authorization on retry, hidden-model rejection, first connection activation without test inference, reconnect page and live update scoping, restart picker, sign-out confirmation and credential scan verified')
                h.stop()
            finally:
                h.cleanup();server.shutdown();server.server_close();thread.join(timeout=10)
                require(not thread.is_alive(),'SecretStore server did not stop')
    finally:
        fixture.terminate();fixture.communicate(timeout=10);scope.retire(fixture)


if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--binary',type=Path,required=True);parser.add_argument('--evidence',type=Path,required=True)
    action=parser.add_mutually_exclusive_group()
    action.add_argument('--group', choices=tuple(CHANNEL_GROUPS), help='Run one complete independent channel group')
    action.add_argument('--delivery-lease-only',action='store_true')
    action.add_argument('--ui-components-only',action='store_true')
    action.add_argument('--rich-replies-only',action='store_true')
    action.add_argument('--memory-only',action='store_true')
    action.add_argument('--attention-only',action='store_true')
    action.add_argument('--feishu-questions-only',action='store_true')
    action.add_argument('--feishu-reviews-only',action='store_true')
    action.add_argument('--preferences-only',action='store_true')
    args=parser.parse_args()
    args.evidence.parent.mkdir(parents=True,exist_ok=True)
    def verify(_):
        if args.group:
            return run(args.binary.resolve(), args.evidence.resolve(), args.group)
        if not args.delivery_lease_only and not args.ui_components_only and not args.rich_replies_only and not args.memory_only and not args.attention_only and not args.feishu_questions_only and not args.feishu_reviews_only and not args.preferences_only:
            return run(args.binary.resolve(),args.evidence.resolve())
        report = dict(checks=[], scenarios={})
        if args.preferences_only:
            preference_settings(args.binary.resolve(),args.evidence.resolve(),report)
        elif args.memory_only:
            memory_inspection(args.binary.resolve(),args.evidence.resolve(),report)
        elif args.rich_replies_only:
            rich_replies(args.binary.resolve(),args.evidence.resolve(),report)
        elif args.feishu_reviews_only:
            feishu_artifact_reviews(args.binary.resolve(), report)
        elif args.feishu_questions_only:
            feishu_task_questions(args.binary.resolve(), report)
        elif args.attention_only:
            attention_workspace(args.binary.resolve(),args.evidence.resolve(),report)
        elif args.ui_components_only:
            sending_feedback(args.binary.resolve(),args.evidence.resolve(),report)
            early_composer(args.binary.resolve(),args.evidence.resolve(),report)
            native_components(args.binary.resolve(),args.evidence.resolve(),report)
            rich_replies(args.binary.resolve(),args.evidence.resolve(),report)
        else:
            delivery_leases(args.binary.resolve(), report)
        report['checks'].append('Cleanup: Web/Bridge children joined, sandbox HTTP servers stopped, disposable roots removed')
        return report
    report=run_cases([0],verify,1)[0]
    report['binary_sha256']=hashlib.sha256(args.binary.read_bytes()).hexdigest()
    args.evidence.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(dict(evidence=str(args.evidence),checks=report['checks'])))
