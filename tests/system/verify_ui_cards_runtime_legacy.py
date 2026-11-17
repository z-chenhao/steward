#!/usr/bin/env python3
"""Retained UI1.13 DOM Runtime snapshot -> current Host Worker, without repair."""
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
from verify_ui_cards_plugins import PNG

ROOT = Path(__file__).resolve().parents[2]
# Actual UI1.13/schema72 carrier predates the Worker representation.
OLD_SHA256 = '0b8309f6172ae6dc7060cc82533f0aee9c1232f463b3225f69e5d2ebb36c6ed1'


def run(old, new, evidence):
    require(hashlib.sha256(old.read_bytes()).hexdigest() == OLD_SHA256, 'retained DOM Runtime carrier changed')
    evidence.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix='steward-cards-runtime-legacy-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        peer = ThreadingHTTPServer(('127.0.0.1', 0), PlanEndpoint)
        peer.requests = []; peer.mode = 'success'; peer.ready = threading.Event(); peer.release = threading.Event(); peer.release.set()
        thread = threading.Thread(target=peer.serve_forever, daemon=True); thread.start()
        h = ChannelHarness(old, Path(temp), platform, '')
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(old)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            h.setup(metadata=dict(name='Legacy Cards peer', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{peer.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            image = process(h.args('artifact', 'save', 'legacy.png', 'image/png'), input=PNG, capture_output=True, env=h.env, cwd=h.root, timeout=30)
            require(image.returncode == 0, 'old Artifact save failed')
            props = dict(title='Card choices', items=[dict(title='Alpha', text='Original card', image_ref=json.loads(image.stdout)['artifact_ref'], url='https://example.com/alpha', label='Details'), dict(title='Beta', text='Ship safe core')])
            peer.plan = dict(title='Historical Cards', fallback='Alpha and Beta', components=[dict(name='Table', props=dict(title='Values', columns=['Category', 'Value'], rows=[['Alpha', '10'], ['Beta', '20']], source='Fixture')), dict(name='steward.cards', props=props)])
            h.start(); h.wait_runtime_ready('old native Cards ready')
            h.http('/api/message', dict(source_id='legacy-cards', content=[dict(type='text', text='PLAN_GATE')]))
            h.wait(lambda state: len(state['deliveries']) == 1 and state['deliveries'][0]['status'] == 'sent', 'old native Cards delivered')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                base = db.execute('SELECT ui_ref FROM ui_widget').fetchone()[0]
            ref = base + '#1'
            def browser(phase):
                env = h.env.copy(); chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
                if chrome.exists(): env['STEWARD_BROWSER_EXECUTABLE'] = str(chrome)
                result = process(['node', str(ROOT / 'tests/system/ui_cards_runtime_legacy_browser.mjs'), h.url, str(evidence / phase), phase, ref], cwd=ROOT, env=env, capture_output=True, text=True, timeout=40)
                require(result.returncode == 0, 'legacy Cards browser: ' + result.stderr)
                return json.loads((evidence / phase / 'report.json').read_text())
            original = browser('before')
            selected = h.command('plugin', 'status'); h.stop(kill=True)
            def facts():
                with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                    db.row_factory = sqlite3.Row; db.execute('BEGIN')
                    return dict(schema=db.execute('PRAGMA user_version').fetchone()[0], widgets=[dict(row) for row in db.execute('SELECT * FROM ui_widget ORDER BY invocation_id')], states=[dict(row) for row in db.execute('SELECT * FROM ui_view_state ORDER BY component_ref')], services=[dict(row) for row in db.execute('SELECT * FROM rsi_run ORDER BY run_id')], cards=[dict(row) for row in db.execute('SELECT * FROM ui_cards_view_snapshot ORDER BY invocation_id')])
            before = facts()
            require(all('steward.ui-cards-view/1' in json.loads(row['managed_services_json']) for row in before['services']), 'actual old Run lacks managed Cards')
            requests = len(peer.requests)
            h.binary = new; h.start()
            # First dependent UI read after the actual binary replacement.
            restored = browser('after')
            require(restored['html'] == original['html'] and restored['state'] == original['state'] and restored['tableHTML'] == original['tableHTML'] and restored['tableState'] == original['tableState'], 'new Host reinterpreted native Cards or rewrote state')
            require(h.command('plugin', 'status') == selected, 'binary replacement reselected plugins')
            after = facts()
            require(all(before[key] == after[key] for key in ['widgets', 'states', 'services', 'cards']), 'recovery rewrote original facts, state versions or service fences')
            require(len(peer.requests) == requests, 'historical UI regenerated through model')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                require(db.execute('SELECT count(*) FROM ui_cards_view_snapshot').fetchone()[0] == 1, 'old Cards snapshot disappeared')
                accepted = json.loads(db.execute('SELECT result_json FROM ui_cards_view_snapshot').fetchone()[0])['views'][0]
                require('worker' not in accepted and hashlib.sha256(accepted['runtime'].encode()).hexdigest() == '4b7947e8673b2b87a280d8daffb6bb7e002158b106e6407da250ae9500a26c59', 'old Runtime was replaced or changed')
                require(db.execute('SELECT count(*) FROM ui_table_view_snapshot').fetchone()[0] == 0, 'old Table acquired a manufactured snapshot')
            table_ref = ref.rsplit('#', 1)[0] + '#0'
            for route in ['/ui/table?ref=', '/ui/table?format=context&ref=', '/ui/table?format=export&rows=0&ref=']:
                h.http(route + quote(table_ref, safe=''), expected=404)
            report = dict(scope='actual UI1.13/schema72 DOM Runtime -> current Host/schema73 archived Worker; original Cards DOM/nonempty state/image/link and native Table group; no setup/reselect/repair', old_binary_sha256=OLD_SHA256, new_binary_sha256=hashlib.sha256(new.read_bytes()).hexdigest(), original_ref=ref, schema_before=before['schema'], schema_after=after['schema'], original_facts_unchanged=True, original_runtime_snapshot_unchanged=True, table_managed_routes_rejected=True, no_model_regeneration=True, before=original, after=restored)
            (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
            return report
        finally:
            peer.release.set(); h.cleanup()
            (evidence / 'runtime-state.json').write_text(json.dumps(h.failure_state(), indent=2))
            peer.shutdown(); peer.server_close(); thread.join()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--old-binary', type=Path, required=True); parser.add_argument('--binary', type=Path, required=True); parser.add_argument('--evidence', type=Path, required=True); args = parser.parse_args()
    print(json.dumps(run_cases([0], lambda _: run(args.old_binary.resolve(), args.binary.resolve(), args.evidence.resolve()), 1)[0]))
