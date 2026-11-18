#!/usr/bin/env python3
"""Real default App meeting contribution, original answers, versions and recovery."""
import argparse
from owner_inventory import check_default_owner_policy
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import io
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading
import time
from fixture_pool import run as process, run_cases
from event_broker import Broker
from feishu_sandbox import Sandbox
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness, require


ROOT = Path(__file__).resolve().parents[2]

class MeetingEndpoint(Endpoint):
 def do_POST(self):
  raw = self.rfile.read(int(self.headers['Content-Length'])); body = json.loads(raw)
  if not any('MEETING_GATE' in str(m.get('content', '')) for m in body['messages'] if m['role'] == 'user'):
   self.rfile = io.BytesIO(raw); return super().do_POST()
  self.server.requests.append(dict(method='POST', body=body, session=self.headers.get('session_id')))
  self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.send_header('Connection', 'close'); self.end_headers()
  def chunk(delta, finish=None):
   value = dict(id='meeting-fixture', object='chat.completion.chunk', model=body['model'], choices=[dict(index=0, delta=delta, finish_reason=finish)])
   self.wfile.write(('data: ' + json.dumps(value) + '\n\n').encode()); self.wfile.flush()
  if body['messages'][-1]['role'] != 'tool' and 'MEETING_GATE' in str(body['messages'][-1].get('content', '')):
   args = json.dumps(self.server.meeting); cut = len(args) - 2
   chunk(dict(tool_calls=[dict(index=0, id='meeting-call', type='function', function=dict(name='builtin__intelligent_ui_show', arguments=args[:cut]))]))
   self.server.prefix_sent = time.time(); self.server.ready.set()
   require(self.server.release.wait(60), 'meeting preview not released')
   chunk(dict(tool_calls=[dict(index=0, function=dict(arguments=args[cut:]))])); chunk({}, 'tool_calls')
  else: chunk(dict(content='Meeting preference ready.')); chunk({}, 'stop')
  self.wfile.write(b'data: [DONE]\n\n'); self.wfile.flush()

def prepare(h, binary, folder):
 folder.mkdir(parents=True); package = folder / 'ui-package'; shutil.copytree(binary.parent / 'plugins/intelligent-ui', package)
 replacements = {}
 for name in ['plugin.go', 'meeting_view.go']:
  original = ROOT / 'internal/plugin/intelligentui' / name; code = original.read_text()
  if name == 'plugin.go': changed = code.replace('Version: "1.27.0"', 'Version: "1.28.0"', 1)
  else: changed = code.replace('Title: props.Reason', 'Title: "Meeting v126: " + props.Reason', 1).replace('DeclineLabel: "None of these work"', 'DeclineLabel: "Try other times"', 1)
  require(changed != code, 'independent meeting implementation unchanged')
  replacement = folder / name; replacement.write_text(changed); replacements[str(original)] = str(replacement)
 overlay = folder / 'overlay.json'; overlay.write_text(json.dumps(dict(Replace=replacements)))
 process(['go', 'build', '-trimpath', '-overlay', str(overlay), '-o', str(package / 'plugin'), './cmd/plugins/intelligent-ui'], cwd=ROOT, check=True, capture_output=True, text=True, timeout=90)
 manifest = json.loads((package / 'steward.plugin.json').read_text()); manifest['version'] = '1.28.0'; (package / 'steward.plugin.json').write_text(json.dumps(manifest))
 h.command('plugin', 'seal', str(package)); installed = h.command('plugin', 'install', str(package))
 with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
  db.execute('BEGIN'); baseline = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
  graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?', (baseline,)).fetchone()[0]); policy = json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
 next(b for b in graph['bindings'] if b['instance_id'] == 'intelligent-ui')['package_digest'] = installed['digest']
 target = h.command('plugin', 'prepare', text=json.dumps(graph))['revision']; policy['id'] = 'meeting-full-app-owner-126'; changed = 0
 for case in policy['cases']:
  expected = case['expected']
  if case.get('contract') == 'steward.ui-document/1':
   for view in expected.get('meeting_views', []): view['title'] = 'Meeting v126: ' + view['title']; view['decline_label'] = 'Try other times'; changed += 1
  if case.get('contract') == 'steward.ui-preview/1' and '"meeting_views"' in expected.get('preview_json', ''):
   expected['preview_json'] = expected['preview_json'].replace('"title":"Review"', '"title":"Meeting v126: Review"', 1).replace('"decline_label":"None of these work"', '"decline_label":"Try other times"', 1); changed += 1
 require(changed == 2, 'independent meeting Owner expectations missing')
 h.command('plugin', 'policy', text=json.dumps(policy)); candidate = 'meeting-full-app-126'
 h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=candidate, baseline=baseline, target=target, policy_id=policy['id'], goal='Verify owning meeting display in real App.')))
 decision = h.command('plugin', 'verify', candidate, timeout=90); h.command('plugin', 'activate', text=json.dumps(dict(operation_id=candidate, expected_graph_revision=baseline, target_graph_revision=target, decision_ref=decision['decision_ref'])), timeout=60)
 return target, installed['digest']

def run(binary, evidence):
 evidence.parent.mkdir(parents=True, exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='steward-meeting-app-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
  peer = ThreadingHTTPServer(('127.0.0.1', 0), MeetingEndpoint); peer.requests = []; peer.mode = 'success'; peer.ready = threading.Event(); peer.release = threading.Event()
  peer.meeting = dict(title='Meeting gate', fallback='Two original times', components=[dict(name='plan_visualization', props=dict(approach='Original', technology='Go', key_elements=[])), dict(name='scheduleTime', props=dict(reasonForScheduling='Review', meetingDuration=30, timeSlots=[dict(date='2026-11-17', time='09:15'), dict(date='2026-11-18', time='14:00')]))])
  thread = threading.Thread(target=peer.serve_forever, daemon=True); thread.start(); h = ChannelHarness(binary, Path(temp), platform, '')
  try:
   h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(binary)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
   h.setup(metadata=dict(name='Meeting peer', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{peer.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
   with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
    db.execute('BEGIN'); target = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]; graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?', (target,)).fetchone()[0]); require(len(graph['bindings']) == 41, 'not actual default41')
    digest = next(b['package_digest'] for b in graph['bindings'] if b['instance_id'] == 'intelligent-ui'); manifest = json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?', (digest,)).fetchone()[0]); require(manifest['version'] == '1.27.0' and len(manifest['provides']) == 9, 'new meeting added a service or default version wrong')
    base_policy = db.execute('SELECT policy_id FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0]; owner_cases = check_default_owner_policy(db, graph, base_policy)
   def browser(phase, ref='', caption='Review'):
    out = evidence.parent / phase; env = h.env.copy(); chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists(): env['STEWARD_BROWSER_EXECUTABLE'] = str(chrome)
    result = process(['node', str(ROOT / 'tests/system/ui_meeting_plugins_browser.mjs'), h.url, str(out), phase, ref, caption], cwd=ROOT, env=env, capture_output=True, text=True, timeout=45)
    (out / 'process-state.json').write_text(json.dumps(dict(url=h.url,pid=h.process.pid if h.process else None,returncode=h.process.poll() if h.process else None))); require(result.returncode == 0, 'actual meeting browser: ' + result.stderr);
    report = json.loads((out / 'report.json').read_text())
    return report
   def snapshots():
    with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
     db.execute('BEGIN'); return db.execute("SELECT w.ui_ref,c.result_json,b.graph_revision,w.message_id FROM ui_widget w JOIN rsi_call c ON c.run_id=w.run_id AND c.operation_id='ui-normalize:'||w.invocation_id JOIN rsi_run b ON b.run_id=w.run_id WHERE w.message_id IS NOT NULL ORDER BY w.rowid").fetchall()
   def answers(count):
    with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
     db.execute('BEGIN'); rows = db.execute('SELECT f.form_id,f.graph_revision,f.result_json,m.message_json FROM ui_form_answer f JOIN main_input m ON m.message_id=f.accepted_message_id ORDER BY f.form_id').fetchall(); require(len(rows) == count, 'real bound answer missing/duplicated')
     for form_id, revision, result, message in rows:
      result, message = json.loads(result), json.loads(message); require(result['error_code'] == '' and message['content'] == [dict(type='text', text=t) for t in result['parts']] and message['source_event_id'] == 'ui-form:' + form_id and revision, 'meeting bypassed original managed answer/Main input transaction')
     return rows
   h.start(); h.wait_runtime_ready('meeting App ready'); h.http('/api/message', dict(source_id='meeting-first', content=[dict(type='text', text='MEETING_GATE')]))
   require(peer.ready.wait(20), 'real prefix unavailable'); preview = browser('preview'); peer.release.set(); h.wait(lambda _: len(snapshots()) == 1, 'first meeting delivered')
   before = snapshots()[0]; require(before[2] == target and len(json.loads(before[1])['meeting_views']) == 1, 'owning display absent from original normalizer receipt'); ref = before[0] + '#1'; formal = browser('formal', ref); saved_answers = answers(1)
   selected = h.command('plugin', 'status'); h.stop(kill=True); h.start(); restart = browser('restart', ref); require(restart['formID'] == formal['formID'] and snapshots()[0] == before and answers(1) == saved_answers, 'first cold recovery changed original view/identity/accepted answer'); require(h.command('plugin', 'status') == selected, 'cold recovery reselected graph')
   h.wait(lambda state: all(r['status'] in ['completed', 'cancelled', 'failed'] for r in state['runs']), 'recovered original answer finished before independent new Run')
   h.stop(); new_target, new_digest = prepare(h, binary, Path(temp) / 'upgrade'); h.start(); h.wait_runtime_ready('meeting upgrade ready'); browser('upgrade-retains-old', ref)
   peer.ready.clear(); peer.release.clear(); h.http('/api/message', dict(source_id='meeting-new', content=[dict(type='text', text='MEETING_GATE_NEW')]))
   require(peer.ready.wait(20), 'new meeting prefix missing'); new_preview = browser('upgrade-preview', caption='Meeting v126: Review'); peer.release.set(); h.wait(lambda _: len(snapshots()) == 2, 'new meeting delivered')
   new = snapshots()[1]; require(new[2] == new_target, 'new meeting wrong graph'); new_ref = new[0] + '#1'; upgraded = browser('upgraded', new_ref, 'Meeting v126: Review'); answers(2); all_snapshots = snapshots(); h.stop()
   candidate = 'meeting-full-app-rollback'; h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=candidate, baseline=new_target, target=target, policy_id=base_policy, goal='Retain both original meeting references.'))); decision = h.command('plugin', 'verify', candidate, timeout=90); h.command('plugin', 'activate', text=json.dumps(dict(operation_id=candidate, expected_graph_revision=new_target, target_graph_revision=target, decision_ref=decision['decision_ref'])), timeout=60)
   h.start(); h.wait_runtime_ready('meeting rollback ready'); browser('rollback-old', ref); browser('rollback-new', new_ref, 'Meeting v126: Review'); h.stop()
   h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='meeting-disable', expected_graph_revision=target))); h.start(); h.http('/', expected=503); require(h.command('plugin', 'status')['graph_revision'] == '', 'cold start reactivated meeting graph'); require(snapshots() == all_snapshots, 'lifecycle recomputed owning receipt'); answers(2); h.stop()
   env = h.env.copy(); env.update(STEWARD_TEST_MEETING_CONFIG=str(h.config), STEWARD_TEST_MEETING_MESSAGE=before[3], STEWARD_TEST_MEETING_FORM=formal['formID'], STEWARD_TEST_MEETING_NEW_MESSAGE=new[3], STEWARD_TEST_MEETING_NEW_FORM=upgraded['formID'])
   result = process(['go', 'test', './internal/app', '-run', '^TestUIMeetingActualForgetRevokesOriginalProjection$', '-count=1'], cwd=ROOT, env=env, capture_output=True, text=True, timeout=90); (evidence.parent / 'forget-control.log').write_text(result.stdout + result.stderr); require(result.returncode == 0, 'actual meeting Forget fence failed: ' + result.stdout + result.stderr)
   report = dict(scope='actual production Host/bridge/Main/default41; source UI1.27 and independent1.28; remaining historical carrier/final gates incomplete', owner_cases=owner_cases, ref=ref, original_graph=target, new_graph=new_target, new_ref=new_ref, packages=[digest, new_digest], formal=formal, restart=restart, upgraded=upgraded, previews=[preview, new_preview], original_receipts_unchanged=True, real_bound_answers=2, no_new_service=True, no_new_persistent_table=True, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
   evidence.write_text(json.dumps(report, indent=2) + '\n'); return report
  finally:
   peer.release.set(); current=h.process; h.cleanup();
   if current:
    stdout,stderr=current.communicate(timeout=10); (evidence.parent / 'runtime-process.json').write_text(json.dumps(dict(returncode=current.returncode,stdout=stdout,stderr=stderr),indent=2))
   (evidence.parent / 'runtime-state.json').write_text(json.dumps(h.failure_state(), indent=2))
   with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
    db.row_factory=sqlite3.Row; db.execute('BEGIN'); state={table:[dict(r) for r in db.execute('SELECT * FROM '+table)] for table in ['rsi_run','ui_widget','ui_form_answer']}
   (evidence.parent / 'view-state.json').write_text(json.dumps(state,indent=2)); peer.shutdown(); peer.server_close(); thread.join()

if __name__ == '__main__':
 p = argparse.ArgumentParser(); p.add_argument('--binary', type=Path, required=True); p.add_argument('--evidence', type=Path, required=True); a = p.parse_args(); print(json.dumps(run_cases([0], lambda _: run(a.binary.resolve(), a.evidence.resolve()), 1)[0]))
