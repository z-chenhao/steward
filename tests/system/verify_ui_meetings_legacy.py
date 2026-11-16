#!/usr/bin/env python3
"""Pinned UI1.23 native meeting carrier -> current Host, first read unchanged."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
from event_broker import Broker
from feishu_sandbox import Sandbox
from fixture_pool import run as process, run_cases
from verify_channels import ChannelHarness, require
from verify_ui_plan_plugins import PlanEndpoint

ROOT = Path(__file__).resolve().parents[2]
OLD_SHA256 = '0e4fd9d43270170991c6af0797bfef2e0c02ed4049cb8a2b7ee3ee4ae73c4a90'

def run(old, new, evidence):
 require(hashlib.sha256(old.read_bytes()).hexdigest() == OLD_SHA256, 'retained UI1.23 artifact changed')
 evidence.mkdir(parents=True, exist_ok=False)
 with tempfile.TemporaryDirectory(prefix='steward-meeting-legacy-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
  peer = ThreadingHTTPServer(('127.0.0.1', 0), PlanEndpoint); peer.requests = []; peer.mode = 'success'; peer.ready = threading.Event(); peer.release = threading.Event(); peer.release.set()
  thread = threading.Thread(target=peer.serve_forever, daemon=True); thread.start(); h = ChannelHarness(old, Path(temp), platform, '')
  try:
   h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(old)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
   h.setup(metadata=dict(name='Historical meeting', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{peer.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
   meeting = dict(name='scheduleTime', props=dict(reasonForScheduling='Review', meetingDuration=30, timeSlots=[dict(date='2026-11-17', time='09:15'), dict(date='2026-11-18', time='14:00')]))
   peer.plan = dict(title='Historical times', fallback='Original meeting options', components=[dict(name='plan_visualization', props=dict(approach='Ship safe core', technology='Go', key_elements=[])), meeting, meeting])
   h.start(); h.wait_runtime_ready('old native meeting ready'); h.http('/api/message', dict(source_id='legacy-meeting', content=[dict(type='text', text='PLAN_GATE')]))
   h.wait(lambda s: len(s['deliveries']) == 1 and s['deliveries'][0]['status'] == 'sent', 'old meeting delivered')
   with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
    db.execute('BEGIN'); base = db.execute('SELECT ui_ref FROM ui_widget').fetchone()[0]
   def browser(phase):
    env = h.env.copy(); chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists(): env['STEWARD_BROWSER_EXECUTABLE'] = str(chrome)
    out = evidence / phase; result = process(['node', str(ROOT / 'tests/system/ui_meeting_legacy_browser.mjs'), h.url, str(out), phase, base], cwd=ROOT, env=env, capture_output=True, text=True, timeout=45)
    require(result.returncode == 0, 'legacy meeting browser: ' + result.stderr); return json.loads((out / 'report.json').read_text())
   before_browser = browser('before')
   # Let the original accepted input finish normally before retaining the carrier.
   h.wait(lambda s: all(r['status'] in ['completed', 'cancelled', 'failed'] for r in s['runs']), 'original meeting answer completed')
   selected = h.command('plugin', 'status'); h.stop(kill=True)
   def facts():
    with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
     db.row_factory = sqlite3.Row; db.execute('BEGIN'); return dict(schema=db.execute('PRAGMA user_version').fetchone()[0], widgets=[dict(r) for r in db.execute('SELECT * FROM ui_widget ORDER BY invocation_id')], services=[dict(r) for r in db.execute('SELECT * FROM rsi_run ORDER BY run_id')], receipts=[dict(r) for r in db.execute('SELECT * FROM rsi_call ORDER BY run_id,instance_id,operation_id')], answers=[dict(r) for r in db.execute('SELECT * FROM ui_form_answer ORDER BY form_id')])
   before = facts(); require(len(before['answers']) == 1, 'old accepted answer missing')
   normalizers = [json.loads(r['result_json']) for r in before['receipts'] if r['operation_id'].startswith('ui-normalize:')]
   require(normalizers and all('meeting_views' not in r for r in normalizers), 'old artifact already owns meeting views')
   requests = len(peer.requests); h.binary = new; h.start()
   # First dependent read after the actual binary replacement; no extra select,
   # update, model request, or reload is used to repair the retained projection.
   after_browser = browser('after')
   require(before_browser['accepted'] == after_browser['accepted'] and before_browser['pending'] == after_browser['pending'], 'current Host reinterpreted original accepted/pending native meeting')
   require(h.command('plugin', 'status') == selected, 'replacement reselected plugins'); after = facts(); require(after == before, 'replacement rewrote original facts/receipts/answer/service fences'); require(len(peer.requests) == requests, 'historical meeting regenerated through model')
   report = dict(scope='actual retained UI1.23/schema80 native meeting -> current Host; accepted and pending original references, first-read DOM/identity/styles/answer; no reselect or repair', old_binary_sha256=OLD_SHA256, new_binary_sha256=hashlib.sha256(new.read_bytes()).hexdigest(), original_ref=base, before=before_browser, after=after_browser, original_facts_unchanged=True, no_manufactured_display=True, no_model_regeneration=True)
   (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n'); return report
  finally:
   peer.release.set(); h.cleanup(); (evidence / 'runtime-state.json').write_text(json.dumps(h.failure_state(), indent=2)); peer.shutdown(); peer.server_close(); thread.join()

if __name__ == '__main__':
 p = argparse.ArgumentParser(); p.add_argument('--old-binary', type=Path, required=True); p.add_argument('--binary', type=Path, required=True); p.add_argument('--evidence', type=Path, required=True); a = p.parse_args(); print(json.dumps(run_cases([0], lambda _: run(a.old_binary.resolve(), a.binary.resolve(), a.evidence.resolve()), 1)[0]))
