#!/usr/bin/env python3
"""Retained pre-Form carrier -> current Host, without setup or reselect."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
from urllib.parse import quote
from event_broker import Broker
from feishu_sandbox import Sandbox
from fixture_pool import run as process, run_cases
from verify_channels import ChannelHarness, require
from verify_ui_plan_plugins import PlanEndpoint

ROOT = Path(__file__).resolve().parents[2]
# This retained actual UI1.21/schema79 artifact predates the Form producer.
OLD_SHA256 = '7d52892dc0416180f4f190db93287f689e706e398624c6230d36122d120fc4ea'


def run(old, new, evidence):
    require(hashlib.sha256(old.read_bytes()).hexdigest() == OLD_SHA256, 'retained pre-Form carrier changed')
    evidence.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix='steward-form-legacy-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        peer = ThreadingHTTPServer(('127.0.0.1', 0), PlanEndpoint)
        peer.requests = []; peer.mode = 'success'; peer.ready = threading.Event(); peer.release = threading.Event(); peer.release.set()
        thread = threading.Thread(target=peer.serve_forever, daemon=True); thread.start()
        h = ChannelHarness(old, Path(temp), platform, '')
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(old)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            h.setup(metadata=dict(name='Legacy Form peer', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{peer.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            props=dict(title='Original form',fields=[dict(name='notes',label='Notes',type='textarea'),dict(name='confirm',label='Confirm',type='boolean')])
            peer.plan=dict(title='Historical Form',fallback='Original form',components=[dict(name='plan_visualization',props=dict(approach='Ship safe core',technology='Go',key_elements=[])),dict(name='steward.form',props=props)])
            h.start(); h.wait_runtime_ready('old native Form ready')
            h.http('/api/message', dict(source_id='legacy-form', content=[dict(type='text', text='PLAN_GATE')]))
            h.wait(lambda state: len(state['deliveries']) == 1 and state['deliveries'][0]['status'] == 'sent', 'old native Form delivered')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                base = db.execute('SELECT ui_ref FROM ui_widget').fetchone()[0]
            ref = base + '#1'
            def browser(phase):
                env = h.env.copy(); chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
                if chrome.exists(): env['STEWARD_BROWSER_EXECUTABLE'] = str(chrome)
                result = process(['node', str(ROOT / 'tests/system/ui_form_legacy_browser.mjs'), h.url, str(evidence / phase), phase, ref], cwd=ROOT, env=env, capture_output=True, text=True, timeout=40)
                require(result.returncode == 0, 'legacy Cards browser: ' + result.stderr)
                return json.loads((evidence / phase / 'report.json').read_text())
            original = browser('before')
            selected = h.command('plugin', 'status'); h.stop(kill=True)
            def facts():
                with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                    db.row_factory = sqlite3.Row; db.execute('BEGIN')
                    return dict(schema=db.execute('PRAGMA user_version').fetchone()[0], widgets=[dict(row) for row in db.execute('SELECT * FROM ui_widget ORDER BY invocation_id')], states=[dict(row) for row in db.execute('SELECT * FROM ui_view_state ORDER BY component_ref')], services=[dict(row) for row in db.execute('SELECT * FROM rsi_run ORDER BY run_id')], cards=[dict(row) for row in db.execute('SELECT * FROM ui_cards_view_snapshot ORDER BY invocation_id')])
            before = facts()
            require(all('steward.ui-form-view/1' not in json.loads(row['managed_services_json']) for row in before['services']), 'old Run unexpectedly declares managed Form')
            requests = len(peer.requests)
            h.binary = new; h.start()
            # First dependent UI read after the actual binary replacement.
            restored = browser('after')
            require(restored['fields'] == original['fields'] and restored['states'] == original['states'], 'new Host reinterpreted native Form or rewrote state')
            require(h.command('plugin', 'status') == selected, 'binary replacement reselected plugins')
            after = facts()
            require(all(before[key] == after[key] for key in ['widgets', 'states', 'services', 'cards']), 'recovery rewrote original facts, state versions or service fences')
            require(len(peer.requests) == requests, 'historical UI regenerated through model')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                require(db.execute('SELECT count(*) FROM ui_form_view_snapshot').fetchone()[0] == 0, 'old Form acquired a manufactured snapshot')
            for route in ['/ui/form-view?ref=']:
                h.http(route+quote(ref,safe=''),expected=404)
            report = dict(scope='retained UI1.21/schema79 -> current Host/schema81; original native Form fields, nonempty draft, CAS and context; no setup/reselect/row repair', old_binary_sha256=OLD_SHA256, new_binary_sha256=hashlib.sha256(new.read_bytes()).hexdigest(), original_ref=ref, schema_before=before['schema'], schema_after=after['schema'], original_facts_unchanged=True, no_manufactured_snapshot=True, managed_routes_rejected=True, no_model_regeneration=True, before=original, after=restored)
            (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
            return report
        finally:
            peer.release.set(); h.cleanup()
            (evidence / 'runtime-state.json').write_text(json.dumps(h.failure_state(), indent=2))
            peer.shutdown(); peer.server_close(); thread.join()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--old-binary', type=Path, required=True); parser.add_argument('--binary', type=Path, required=True); parser.add_argument('--evidence', type=Path, required=True); args = parser.parse_args()
    print(json.dumps(run_cases([0], lambda _: run(args.old_binary.resolve(), args.binary.resolve(), args.evidence.resolve()), 1)[0]))
