#!/usr/bin/env python3
"""Real App Cards candidate: streaming, original Artifact, links and cold reads."""
import argparse
import base64
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading
from event_broker import Broker
from feishu_sandbox import Sandbox
from fixture_pool import run as process, run_cases
from verify_channels import ChannelHarness, require
from verify_ui_plan_plugins import PlanEndpoint

ROOT = Path(__file__).resolve().parents[2]
CONTRACT = 'steward.ui-cards-view/1'
PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=')


def prepare(h, binary, folder, version='1.28.0'):
    folder.mkdir()
    package = folder / 'package'
    shutil.copytree(binary.parent / 'plugins/intelligent-ui', package)
    original = ROOT / 'internal/plugin/intelligentui/plugin.go'
    code = original.read_text()
    replacement = folder / 'plugin.go.txt'
    changed = code.replace('Version: "1.27.0"', 'Version: "' + version + '"', 1)
    require(changed != code, 'independent version fixture did not change default producer')
    replacement.write_text(changed)
    overlay = folder / 'overlay.json'
    replacements = {str(original): str(replacement)}
    if version == '1.28.0':
        source = ROOT / 'internal/plugin/intelligentui/cards_view.go'
        code = source.read_text()
        anchor = '<h3 class="native-answer-title" data-card-text="{{literal .Title}}">{{.Title}}</h3>'
        require(code.count(anchor) == 1, 'Cards owning template anchor changed')
        variant = folder / 'cards_view.go.txt'
        variant.write_text(code.replace(anchor, '<h3 class="native-answer-title" data-card-text="{{literal (printf "Cards v124: %s" .Title)}}">Cards v124: {{.Title}}</h3>', 1))
        replacements[str(source)] = str(variant)
    overlay.write_text(json.dumps(dict(Replace=replacements)))
    process(['go', 'build', '-overlay', str(overlay), '-o', str(package / 'plugin'), './cmd/plugins/intelligent-ui'], cwd=ROOT, check=True, capture_output=True, timeout=90)
    manifest_path = package / 'steward.plugin.json'
    manifest = json.loads(manifest_path.read_text())
    manifest['version'] = version
    require(any(service['contract'] == CONTRACT for service in manifest['provides']), 'default package lacks Cards service')
    manifest_path.write_text(json.dumps(manifest))
    h.command('plugin', 'seal', str(package))
    installed = h.command('plugin', 'install', str(package))
    with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
        db.execute('BEGIN')
        baseline = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
        graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?', (baseline,)).fetchone()[0])
        policy = json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
    next(b for b in graph['bindings'] if b['instance_id'] == 'intelligent-ui')['package_digest'] = installed['digest']
    target = h.command('plugin', 'prepare', text=json.dumps(graph))['revision']
    policy['id'] = 'cards-full-app-owner-' + version
    if CONTRACT not in policy['allowed_contracts']:
        policy['allowed_contracts'].append(CONTRACT)
    policy['cases'] = [case for case in policy['cases'] if case['contract'] != CONTRACT]
    original_case = json.loads((ROOT / 'internal/plugin/intelligentui/testdata/original-cards.json').read_text())
    expected = dict(component=0, html=json.loads((ROOT / 'internal/rsi/builtin/ui-cards-worker.json').read_text())['expected']['views'][0]['html'], css=(ROOT / 'internal/plugin/intelligentui/view/cards.css').read_text(), worker=(ROOT / 'internal/plugin/intelligentui/view/cards-worker.js').read_text(), compact_max_width=760, contexts=[dict(title='Card choices', visible_item=label) for label in ['Alpha', 'Beta']])
    if version == '1.28.0':
        expected['html'] = expected['html'].replace('>Card choices</h3>', '>Cards v124: Card choices</h3>').replace('Q2FyZCBjaG9pY2Vz', 'Q2FyZHMgdjEyNDogQ2FyZCBjaG9pY2Vz')
    facts = json.dumps(dict(cards=[dict(component=0, props=original_case['props'])])).encode()
    policy['cases'].append(dict(instance_id='intelligent-ui', contract=CONTRACT, input={}, inputs=[dict(id='ui-cards', data=base64.b64encode(facts).decode())], expected=dict(views=[expected], error_code='')))
    h.command('plugin', 'policy', text=json.dumps(policy))
    candidate = 'cards-full-app-' + version
    h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=candidate, baseline=baseline, target=target, policy_id=policy['id'], goal='Verify actual Cards App before default activation.')))
    decision = h.command('plugin', 'verify', candidate, timeout=90)
    h.command('plugin', 'activate', text=json.dumps(dict(operation_id=candidate, expected_graph_revision=baseline, target_graph_revision=target, decision_ref=decision['decision_ref'])), timeout=60)
    return target, installed['digest']


def run(binary, evidence):
    evidence.mkdir(parents=True, exist_ok=False)
    with tempfile.TemporaryDirectory(prefix='steward-cards-app-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        peer = ThreadingHTTPServer(('127.0.0.1', 0), PlanEndpoint)
        peer.requests = []; peer.mode = 'success'; peer.ready = threading.Event(); peer.release = threading.Event()
        thread = threading.Thread(target=peer.serve_forever, daemon=True); thread.start()
        h = ChannelHarness(binary, Path(temp), platform, '')
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(binary)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            h.setup(metadata=dict(name='Cards App peer', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{peer.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            image = process(h.args('artifact', 'save', 'card.png', 'image/png'), input=PNG, capture_output=True, env=h.env, cwd=h.root, timeout=30)
            require(image.returncode == 0, 'real Artifact save failed')
            resource = json.loads(image.stdout)['artifact_ref']
            props = dict(title='Card choices', items=[dict(title='Alpha', text='Original card', image_ref=resource, url='https://example.com/alpha', label='Details'), dict(title='Beta', text='Ship safe core')])
            peer.plan = dict(title='Managed Cards', fallback='Alpha and Beta', components=[dict(name='Table', props=dict(title='Values', columns=['Category', 'Value'], rows=[['Alpha', '10'], ['Beta', '20']], source='Fixture')), dict(name='steward.cards', props=props)])
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                graph = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
                frozen = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?', (graph,)).fetchone()[0])
                digest = next(binding['package_digest'] for binding in frozen['bindings'] if binding['instance_id'] == 'intelligent-ui')
                manifest = json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?', (digest,)).fetchone()[0])
                require(manifest['version'] == '1.27.0' and any(service['contract'] == CONTRACT for service in manifest['provides']), 'fresh default did not activate Cards1.19')
                base_policy_id, policy_raw = db.execute('SELECT policy_id,policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()
                owner_cases = len(json.loads(policy_raw)['cases'])
                require(owner_cases == 387, 'default Cards Owner oracle missing')
            h.start(); h.wait_runtime_ready('managed Cards App ready')
            def browser(phase, ref='', heading='Card choices'):
                env = h.env.copy(); chrome = Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
                if chrome.exists(): env['STEWARD_BROWSER_EXECUTABLE'] = str(chrome)
                result = process(['node', str(ROOT / 'tests/system/ui_cards_plugins_browser.mjs'), h.url, str(evidence / phase), phase, ref, heading], cwd=ROOT, env=env, capture_output=True, text=True, timeout=45)
                require(result.returncode == 0, 'actual Cards browser: ' + result.stderr)
                return json.loads((evidence / phase / 'report.json').read_text())
            h.http('/api/message', dict(source_id='cards-app', content=[dict(type='text', text='PLAN_GATE')]))
            require(peer.ready.wait(20), 'provider did not emit Cards prefix')
            preview = browser('preview'); peer.release.set()
            h.wait(lambda state: len(state['deliveries']) == 1 and state['deliveries'][0]['status'] == 'sent', 'Cards delivered')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                ref, saved, accepted_graph = db.execute('SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_cards_view_snapshot p USING(invocation_id)').fetchone()
                require(accepted_graph == graph, 'Cards published under wrong graph')
                require(db.execute("SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-cards%'").fetchone()[0] == 0, 'pure Cards view reserved effects')
            ref += '#1'
            formal = browser('formal', ref)
            selected = h.command('plugin', 'status'); h.stop(kill=True); h.start(); h.wait_runtime_ready('Cards cold recovery ready')
            require(h.command('plugin', 'status') == selected, 'restart changed plugin selection')
            restart = browser('restart', ref)
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                require(db.execute('SELECT result_json FROM ui_cards_view_snapshot').fetchone()[0] == saved, 'cold recovery recomputed view')
                states = db.execute('SELECT component_ref,state_json,model_context_json FROM ui_view_state ORDER BY component_ref').fetchall()
                require(any(row[0] == ref and json.loads(row[1]).get('index') == 1 and json.loads(row[2]).get('visible_item') == 'Beta' for row in states), 'actual CAS/frozen context missing')
            h.stop()
            upgraded_graph, upgraded_digest = prepare(h, binary, Path(temp) / 'upgrade', '1.28.0')
            h.start(); h.wait_runtime_ready('Cards upgraded runtime ready')
            old_after_upgrade = browser('upgrade-retains-old', ref)
            peer.ready.clear(); peer.release.clear()
            h.http('/api/message', dict(source_id='cards-upgraded', content=[dict(type='text', text='PLAN_GATE_NEW')]))
            require(peer.ready.wait(20), 'new Cards version did not stream')
            upgraded_preview = browser('upgrade-preview', heading='Cards v124: Card choices'); peer.release.set()
            h.wait(lambda state: len(state['deliveries']) == 2 and all(d['status'] == 'sent' for d in state['deliveries']), 'upgraded Cards delivered')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                upgraded_ref, upgraded_raw = db.execute('SELECT w.ui_ref,p.result_json FROM ui_widget w JOIN ui_cards_view_snapshot p USING(invocation_id) WHERE p.graph_revision=?', (upgraded_graph,)).fetchone()
                require('Cards v124: Card choices' in upgraded_raw and 'Cards v124:' not in saved, 'owning template version did not change')
                snapshots = db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_cards_view_snapshot ORDER BY invocation_id').fetchall()
            upgraded_ref += '#1'
            upgraded = browser('formal-upgraded', upgraded_ref, 'Cards v124: Card choices')
            h.stop()
            candidate = 'cards-full-app-rollback'
            h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=candidate, baseline=upgraded_graph, target=graph, policy_id=base_policy_id, goal='Retain both published Cards versions.')))
            decision = h.command('plugin', 'verify', candidate, timeout=90)
            h.command('plugin', 'activate', text=json.dumps(dict(operation_id=candidate, expected_graph_revision=upgraded_graph, target_graph_revision=graph, decision_ref=decision['decision_ref'])), timeout=60)
            h.start(); h.wait_runtime_ready('Cards rollback ready')
            rollback_old = browser('rollback-old', ref)
            rollback_new = browser('rollback-new', upgraded_ref, 'Cards v124: Card choices')
            h.stop()
            h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='cards-disable', expected_graph_revision=graph)))
            h.start()
            # These historical interactions immediately follow cold start: no
            # select, setup or state write may repair the original selection.
            disabled_old = browser('disabled-old', ref)
            disabled_new = browser('disabled-new', upgraded_ref, 'Cards v124: Card choices')
            require(h.command('plugin', 'status')['graph_revision'] == '', 'cold startup silently reactivated plugins')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                require(db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_cards_view_snapshot ORDER BY invocation_id').fetchall() == snapshots, 'version lifecycle rewrote accepted snapshots')
            lifecycle = dict(upgraded_graph=upgraded_graph, upgraded_digest=upgraded_digest, upgraded_ref=upgraded_ref, preview=upgraded_preview, upgraded=upgraded, old_after_upgrade=old_after_upgrade, rollback=[rollback_old, rollback_new], disabled=[disabled_old, disabled_new], snapshots_unchanged=True)
            h.stop()
            revocation_env = h.env.copy()
            revocation_env.update(STEWARD_TEST_CARDS_CONFIG=str(h.config), STEWARD_TEST_CARDS_REF=ref, STEWARD_TEST_CARDS_ARTIFACT=resource)
            revocation = process(['go', 'test', '-race', '-v', './internal/app', '-run', '^TestUICardsActualOpenRevocationFence$', '-count=1'], cwd=ROOT, env=revocation_env, capture_output=True, text=True, timeout=180)
            (evidence / 'revocation.log').write_text(revocation.stdout + revocation.stderr)
            require(revocation.returncode == 0, 'actual App post-open revocation: ' + revocation.stdout + revocation.stderr)
            report = dict(scope='fresh default40 UI1.27 Cards, real App/SQLite/Artifact/bridge/browser; explicit1.27/1.28 template upgrade, rollback and deactivation; post-open Artifact deletion/Forget covered; final gates pending', graph=graph, package_digest=digest, lifecycle=lifecycle, default_owner_cases=owner_cases, post_open_revocation=True, ref=ref, preview=preview, formal=formal, restart=restart, states=states, snapshot_sha256=hashlib.sha256(saved.encode()).hexdigest(), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
            (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
            return report
        finally:
            peer.release.set(); h.cleanup()
            (evidence / 'runtime-state.json').write_text(json.dumps(h.failure_state(), indent=2))
            peer.shutdown(); peer.server_close(); thread.join()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--binary', type=Path, required=True); parser.add_argument('--evidence', type=Path, required=True); args = parser.parse_args()
    print(json.dumps(run_cases([0], lambda _: run(args.binary.resolve(), args.evidence.resolve()), 1)[0]))
