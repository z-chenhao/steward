#!/usr/bin/env python3
"""Actual default bundle and HTTP model menu; independent executable lifecycle."""
import argparse
from owner_inventory import check_default_owner
from contextlib import closing
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
from html.parser import HTMLParser
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from event_broker import Broker
from terminal_driver import Terminal
from feishu_sandbox import Sandbox
from fixture_pool import run as run_process, run_cases
from verify_channels import ChannelHarness, TOKEN, require
from verify_web_plugins import model_peer

ROOT = Path(__file__).resolve().parents[2]
class ProviderSelection(HTMLParser):
    def __init__(self, markup):
        super().__init__()
        self.service = False
        self.selected = []
        self.feed(markup)

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == 'select':
            self.service = values.get('name') == 'service'
        elif tag == 'option' and self.service and 'selected' in values:
            self.selected.append(values.get('value'))

    def handle_endtag(self, tag):
        if tag == 'select':
            self.service = False

CONTRACT = 'steward.model-menu/1'
SETTINGS = 'steward.model-settings/1'


def activate(h, name, baseline, target, policy):
    h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=name, baseline=baseline,
        target=target, policy_id=policy, goal='Independent model menu display.')))
    decision = h.command('plugin', 'verify', name, timeout=90)
    h.command('plugin', 'activate', text=json.dumps(dict(operation_id=name,
        expected_graph_revision=baseline, target_graph_revision=target,
        decision_ref=decision['decision_ref'])), timeout=60)


def run(binary, evidence, candidate_source=ROOT):
    evidence.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='steward-model-menu-') as tmp, Sandbox() as platform, Broker(Path(tmp)) as broker, model_peer('model-menu') as (endpoint, model_requests):
        h = ChannelHarness(binary, Path(tmp), platform, fixture='')
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(binary)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            profile = h.setup(metadata=dict(name='Model menu peer', provider_kind='openai_compatible',
                endpoint=endpoint, wire_api='openai_completions', model_id='fixture-model',
                context_window=131072, max_output_tokens=4096,
                compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            before = h.command('status')
            def check_browser_projection(independent):
                facts = dict(previous=[dict(key='account:old',label='Old',sort_key='old')],received=[dict(key='profile:current:2',label='Current',sort_key='current')],reason='ack',unavailable=False)
                request = Request(h.url + '/api/models/project', data=json.dumps(facts).encode(), headers={'Authorization':'Bearer '+TOKEN,'Content-Type':'application/json'})
                with urlopen(request,timeout=30) as response:
                    choices = json.load(response)
                require([c['key'] for c in choices] == (['profile:current:2'] if independent else ['account:old','profile:current:2']), 'HTTP browser projection ignored selected policy revision')
            def check_terminal(endpoint):
                with Terminal(h.args('tui'),h.env,h.root) as terminal:
                    terminal.expect('You> ');terminal.send('/setup')
                    terminal.expect('Connection [1/2]');terminal.send('1')
                    terminal.expect('Endpoint URL ['+endpoint+']');terminal.expect('> ');terminal.send('/cancel')
                    terminal.expect('Setup cancelled.');terminal.expect('You> ')
                    require(terminal.stop()==0,'terminal recipe cancellation did not close')
            def check_shortcut(model, window):
                value = h.command('model', 'add', endpoint, model)
                require(value['context_window'] == window and value['max_output_tokens'] == 1024 and value['wire_api'] == 'openai_completions' and value['capabilities']['text_input'] and not value['capabilities']['image_input'] and value['model_id'] == model and value['endpoint'] == endpoint, 'actual CLI ignored owner recipe or changed identity')
            check_shortcut('cli-original', 8192)
            check_terminal('http://127.0.0.1:8000/v1')
            invalid_root = Path(tmp) / 'invalid-cli-root'
            rejected = run_process([str(binary), 'model', 'add', 'https://user:synthetic@example.test/v1', 'bad-model'], env={**h.env, 'STEWARD_DATA_ROOT':str(invalid_root)}, capture_output=True, text=True, timeout=20)
            require(rejected.returncode != 0 and not (invalid_root / 'state.db').exists(), 'invalid identity initialized runtime state')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                db.execute('BEGIN')
                baseline = db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
                graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?', (baseline,)).fetchone()[0])
                policy = json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
            check_default_owner(graph, policy)
            original_policy = policy['id']
            require(sum(c['contract'] == CONTRACT for c in policy['cases']) == 8, 'model-menu Owner cases missing')
            def submit_connection(name):
                # Probe may reject capabilities after saving a candidate; inspect
                # the authoritative saved profile independently of that outcome.
                request = Request(h.url + '/model/add', data=urlencode(dict(service='custom', endpoint=endpoint, model='fixture-model', name=name)).encode(), headers={'Authorization': 'Bearer ' + TOKEN, 'Content-Type':'application/x-www-form-urlencoded', 'Accept':'application/json'})
                try:
                    with urlopen(request, timeout=30) as response:
                        require(response.status == 200, 'unexpected add response')
                except HTTPError as error:
                    require(error.code == 409, 'unexpected add error: ' + str(error.code))
            h.start(); h.wait_runtime_ready('model menu ready')
            original = h.http('/api/models')
            settings_path = '/settings/models/' + profile
            def check_settings(status):
                view = h.http(settings_path)
                require('id="model-' + profile + '"' in view and 'data-status="' + status + '"' in view, 'actual Settings did not bind owned status to original profile')
                variant = status.startswith('v2 ')
                require(('<h1>Independent models</h1>' in view) == variant, 'HTTP ignored the selected sealed template resource')
                localized = h.http(settings_path, headers={'Cookie':'steward-language=zh-CN'})
                require('aria-label="' + ('独立版本模型' if variant else '模型') + '"' in localized, 'HTTP ignored selected owner dictionary')
                h.env['STEWARD_MODEL_POLICY_ENDPOINT'] = 'http://127.0.0.1:' + ('9000' if variant else '8000') + '/v1'
                h.browser(evidence.parent / ('provider-v2' if variant else 'provider-original'), 'model-plugin-policy')
                updates = h.http('/api/updates?view=/settings/models')
                require(('data-model-owner-version="v2"' in updates['regions']['signin-status']) == variant, 'live sign-in fragment used another template version')
            check_settings('connected')
            check_browser_projection(False)
            h.browser(evidence.parent / 'composer-original', 'composer-models')
            def check_draft(service):
                view = h.http('/settings/models/new?method=api')
                require(ProviderSelection(view).selected == [service], 'actual new connection ignored owned defaults')
                source = h.http('/settings/models/new?source=' + profile)
                require('value="fixture-model"' in source and 'value="Model menu peer"' in source, 'reconnect lost original source prefill')
            check_draft('openai')
            require(original['current'].startswith('profile:' + profile + ':') and any(c['label'] == 'Model menu peer' for c in original['choices']), 'actual HTTP did not bind original profile menu')
            def send_after_recovery(phase, expected_label):
                status = h.http('/api/status')
                menu = h.http('/api/models')
                require(status['active_model_profile_id'] == profile and
                        status['config_version'] == before['config_version'] and
                        menu['current'] == original['current'] and
                        menu['config_version'] == original['config_version'],
                        f'{phase}: original selected model or version was lost before sending')
                marker = 'Immediately send with the original model after ' + phase
                start = len(model_requests)
                h.env.update(STEWARD_MODEL_RECOVERY_LABEL=expected_label, STEWARD_MODEL_RECOVERY_MESSAGE=marker)
                h.browser(evidence.parent / phase, 'model-recovery-send')
                requests = [r['body']['model'] for r in model_requests[start:] if r['method'] == 'POST'
                            and any(m['role'] == 'user' and marker in str(m.get('content'))
                                    for m in r['body']['messages'])]
                require(requests == ['fixture-model'], f'{phase}: original model did not receive the browser message exactly once')
                h.wait(lambda state: len([r for r in state['deliveries'] if r['status'] == 'sent']) ==
                       (1 if phase == 'upgrade' else 2), f'{phase}: browser response not durably delivered')
                return dict(current=menu['current'], config_version=menu['config_version'],
                            provider_model=requests[0], delivery='sent')
            h.stop(kill=True); h.start(); h.wait_runtime_ready('model menu cold ready')
            require(h.http('/api/models') == original, 'first cold read changed original menu')
            check_settings('connected')
            h.stop()
            package = Path(tmp) / 'model-menu-v2'
            shutil.copytree(binary.parent / 'plugins/model-connections', package)
            source = candidate_source / 'internal/plugin/modelconnections/menu.go'
            code = source.read_text().replace('Version: "1.1.0"', 'Version: "1.2.0"', 1)
            code = code.replace('Label: label', 'Label: "v2 "+label').replace('Label: m.Name', 'Label: "v2 "+m.Name')
            require(code != source.read_text(), 'independent executable not changed')
            replacement = Path(tmp) / 'menu.go'; replacement.write_text(code)
            settings_source = candidate_source / 'internal/plugin/modelconnections/settings.go'
            settings_replacement = Path(tmp) / 'settings.go'
            view_file = package / 'views/models.html'
            variant_view = view_file.read_text().replace('<h1>{{modelTr "Models"}}</h1>', '<h1>{{modelTr "Independent models"}}</h1>', 1).replace('{{define "signin"}}', '{{define "signin"}}<span hidden data-model-owner-version="v2"></span>', 1)
            variant_view = variant_view.replace('data-model-owner-endpoint-default="http://127.0.0.1:8000/v1"', 'data-model-owner-endpoint-default="http://127.0.0.1:9000/v1"', 1)
            view_file.write_text(variant_view)
            copy_file = package / 'views/copy.json'
            translations = json.loads(copy_file.read_text())
            for entry in translations:
                if entry['key'] == 'Models':
                    entry['value'] = '独立版本模型'
            variant_copy = json.dumps(translations, ensure_ascii=False, indent=2) + '\n'
            copy_file.write_text(variant_copy)
            settings_code = settings_source.read_text().replace('Status: status', 'Status: "v2 "+status', 1).replace('Service: "openai"', 'Service: "custom"', 1)
            settings_code = settings_code.replace('//go:embed view/models.html\nvar modelViewSource string', 'var modelViewSource = ' + json.dumps(variant_view))
            settings_code = settings_code.replace('//go:embed view/copy.json\nvar modelCopySource string', 'var modelCopySource = ' + json.dumps(variant_copy))
            settings_replacement.write_text(settings_code)
            connection_source = candidate_source / 'internal/plugin/modelconnections/legacyv1/connection.go'
            connection_replacement = Path(tmp) / 'connection.go'
            connection_replacement.write_text(connection_source.read_text().replace('out.ContextWindow = 8192', 'out.ContextWindow = 6144', 1).replace('out.EndpointDefault = \"http://127.0.0.1:8000/v1\"','out.EndpointDefault = \"http://127.0.0.1:9000/v1\"',1).replace('\treturn out\n', '\tif f.Added != nil { out.AddedAction = \"save\" }\n\treturn out\n', 1))
            browser_source = candidate_source / 'internal/plugin/modelconnections/browser.go'
            browser_replacement = Path(tmp) / 'browser.go'
            marker = 'func projectBrowserMenu(f contract.BrowserFacts) []contract.ChoiceRef {'
            require(browser_source.read_text().count(marker) == 1, 'browser policy marker missing')
            browser_replacement.write_text(browser_source.read_text().replace(marker, marker + '\n if f.Reason == "ack" { refs:=[]contract.ChoiceRef{};for i:=range f.Received {refs=append(refs,contract.ChoiceRef{Source:"received",Index:i})};return refs }',1))
            picker_source = candidate_source / 'internal/plugin/modelconnections/picker.go'
            picker_replacement = Path(tmp) / 'picker.go'
            picker_code = picker_source.read_text().replace('t("Model")', '"v2 " + t("Model")', 1).replace('saving || loading', 'saving || (loading && ready)', 1)
            picker_code = picker_code.replace('row.PendingText = t("Loading models…")', 'row.PendingText = t("Loading models…") + " v2"', 1)
            picker_replacement.write_text(picker_code)
            overlay = Path(tmp) / 'overlay.json'; overlay.write_text(json.dumps(dict(Replace={str(source): str(replacement), str(settings_source): str(settings_replacement), str(connection_source):str(connection_replacement),str(browser_source):str(browser_replacement),str(picker_source):str(picker_replacement)})))
            run_process(['go', 'build', '-trimpath', '-overlay', str(overlay), '-o', str(package / 'plugin'), './cmd/plugins/model-connections'], cwd=candidate_source, check=True, capture_output=True, text=True, timeout=90)
            manifest = json.loads((package / 'steward.plugin.json').read_text()); manifest['version'] = '1.2.0'
            (package / 'steward.plugin.json').write_text(json.dumps(manifest))
            h.command('plugin', 'seal', str(package)); installed = h.command('plugin', 'install', str(package))
            binding = next(b for b in graph['bindings'] if b['instance_id'] == 'model-connections')
            binding['package_digest'] = installed['digest']
            target = h.command('plugin', 'prepare', text=json.dumps(graph))['revision']
            policy['id'] = 'model-menu-v2-owner'
            for case in policy['cases']:
                if case['contract'] == 'steward.model-connection-policy/1' and case['input'].get('preset') == 'cli-shorthand':
                    case['expected']['context_window'] = 6144
                if case['contract'] == 'steward.model-connection-policy/1' and case['input'].get('added') is not None:
                    case['expected']['added_action'] = 'save'
                if case['contract'] == 'steward.model-connection-policy/1' and case['input'].get('preset') == 'terminal-setup':
                    case['expected']['endpoint_default'] = 'http://127.0.0.1:9000/v1'
                if case['contract'] == 'steward.model-picker/1':
                    for language in ('en', 'zh_cn'):
                        case['expected'][language]['label'] = 'v2 ' + case['expected'][language]['label']
                        for state in case['expected'][language]['states']:
                            state['busy'] = bool(state['id'] & 4 or state['id'] & 1 and state['id'] & 2)
                            if not state['id'] & 1:
                                state['pending_text'] += ' v2'
                if case['contract'] == SETTINGS:
                    if 'copy' in case['expected']:
                        case['expected']['copy']['source'] = variant_copy
                        case['expected']['copy']['digest'] = 'sha256:' + hashlib.sha256(variant_copy.encode()).hexdigest()
                    if 'view' in case['expected']:
                        case['expected']['view']['source'] = variant_view
                        case['expected']['view']['digest'] = 'sha256:' + hashlib.sha256(variant_view.encode()).hexdigest()
                    if 'draft' in case['expected'] and not case['input']['draft']['has_source']:
                        case['expected']['draft']['service'] = 'custom'
                    for row in case['expected']['rows']:
                        row['status'] = 'v2 ' + row['status']
                if case['contract'] == CONTRACT:
                    browser = case['input'].get('browser')
                    if browser and browser['reason'] == 'ack':
                        case['expected']['order'] = [dict(source='received',index=i) for i in range(len(browser['received']))]
                    for choice in case['expected']['choices']:
                        choice['label'] = 'v2 ' + choice['label']
            h.command('plugin', 'policy', text=json.dumps(policy))
            activate(h, 'model-menu-upgrade', baseline, target, policy['id'])
            h.start(); h.wait_runtime_ready('model menu v2 ready')
            recovered = {'upgrade': send_after_recovery('upgrade', 'v2 Model menu peer')}
            upgraded = h.http('/api/models')
            require(upgraded['current'] == original['current'] and upgraded['config_version'] == original['config_version'] and [c['key'] for c in upgraded['choices']] == [c['key'] for c in original['choices']] and [c['label'] for c in upgraded['choices']] == ['v2 ' + c['label'] for c in original['choices']], 'HTTP did not use independent labels or changed activation facts')
            run_process(['node', str(ROOT / 'tests/system/channels_browser.mjs'), h.url, str(evidence.parent/'picker-independent'), 'model-picker-version'],cwd=ROOT,env={**h.env,'STEWARD_MODEL_PICKER_LABEL':'v2 Model','STEWARD_MODEL_PICKER_PENDING':'Loading models… v2','STEWARD_MODEL_PICKER_BUSY':'false'},check=True,capture_output=True,text=True,timeout=60)
            check_shortcut('cli-v2', 6144)
            check_terminal('http://127.0.0.1:9000/v1')
            check_settings('v2 connected')
            check_browser_projection(True)
            check_draft('custom')
            requests_before_save = len(model_requests)
            submit_connection('HTTP independent save')
            saved = [p for p in h.command('model', 'list') if p['name'] == 'HTTP independent save']
            require(len(saved) == 1 and len(model_requests) == requests_before_save, 'independent owner save still ran native post-add probe')
            h.stop(); activate(h, 'model-menu-rollback', target, baseline, original_policy)
            h.start(); h.wait_runtime_ready('model menu rollback ready')
            recovered['rollback'] = send_after_recovery('rollback', 'Model menu peer')
            require(h.http('/api/models') == original, 'rollback did not restore exact original menu')
            check_settings('connected')
            check_draft('openai')
            run_process(['node', str(ROOT / 'tests/system/channels_browser.mjs'), h.url, str(evidence.parent/'picker-rollback'), 'model-picker-version'],cwd=ROOT,env={**h.env,'STEWARD_MODEL_PICKER_LABEL':'Model','STEWARD_MODEL_PICKER_PENDING':'Loading models…','STEWARD_MODEL_PICKER_BUSY':'true'},check=True,capture_output=True,text=True,timeout=60)
            check_shortcut('cli-rollback', 8192)
            check_terminal('http://127.0.0.1:8000/v1')
            check_browser_projection(False)
            requests_before_probe = len(model_requests)
            submit_connection('HTTP owned candidate')
            require(len(model_requests) > requests_before_probe, 'rollback did not restore owned post-add probe')
            added = [p for p in h.command('model', 'list') if p['name'] == 'HTTP owned candidate']
            require(len(added) == 1 and added[0]['context_window'] == 128000 and added[0]['max_output_tokens'] == 16384 and added[0]['capabilities']['image_input'], 'actual submission ignored owning candidate defaults')
            h.stop(); h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='model-menu-disable', expected_graph_revision=baseline)))
            h.start(); h.http('/api/models', expected=503); h.http(settings_path, expected=503); h.http('/settings/models/new?method=api', expected=503); h.http('/api/updates?view=/settings/models', expected=503)
            h.stop(kill=True); h.start(); h.http('/api/models', expected=503); h.http(settings_path, expected=503); h.http('/settings/models/new?method=api', expected=503); h.http('/api/updates?view=/settings/models', expected=503)
            request = Request(h.url + '/api/models/project',data=json.dumps(dict(previous=[],received=[],reason='ack',unavailable=False)).encode(),headers={'Authorization':'Bearer '+TOKEN,'Content-Type':'application/json'})
            try:
                urlopen(request,timeout=30).close()
                raise AssertionError('cold stopped browser policy restored native fallback')
            except HTTPError as error:
                require(error.code==503,'unexpected stopped browser projection error')
            submit_connection('Must not be created')
            require(not any(p['name'] == 'Must not be created' for p in h.command('model', 'list')), 'cold deactivation accepted native candidate construction')
            stopped = h.command('model', 'add', endpoint, 'cli-must-not-exist', ok=False)
            require(stopped.returncode != 0 and not any(p['model_id']=='cli-must-not-exist' for p in h.command('model','list')), 'cold deactivation restored native CLI defaults')
            explicit = h.command('model','add',text=json.dumps(dict(name='Explicit operator metadata',provider_kind='openai_compatible',endpoint=endpoint,wire_api='openai_responses',model_id='explicit-operator-model',context_window=32768,max_output_tokens=2048,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True,image_input=True))))
            require(explicit['context_window']==32768 and explicit['max_output_tokens']==2048 and explicit['wire_api']=='openai_responses' and explicit['capabilities']['image_input'], 'operator metadata was rewritten or coupled to plugin availability')
            after = h.command('status')
            require(after['config_version'] == before['config_version'] and after['active_model_profile_id'] == before['active_model_profile_id'], 'display lifecycle changed authoritative model activation')
            report = dict(checks=['actual fresh default graph and reviewed Owner cases', 'actual HTTP original/cold menu and Settings rows', 'independent1.2 executable labels and Settings statuses', 'exact key/current/config-version preservation', 'rollback original menu', 'deactivation and first cold read reject native fallback', 'authoritative model activation unchanged', 'HTTP independent post-add save and rollback probe', 'HTTP browser merge independent executable and rollback', 'actual PTY terminal recipe default/independent/rollback with cancellation and original selection preserved', 'actual managed composer browser loading/refresh/ack/unknown readback', 'actual independent picker labels/loading/busy and exact rollback without activation'], baseline=baseline, upgraded_graph=target, original=original, upgraded=upgraded, package_digest=installed['digest'], binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), scope='F-001 menu and Settings row projection; new connection defaults and source prefill; sealed model templates and live sign-in fragments; owned localization; CLI shorthand owner recipe and explicit operator metadata separation; remaining browser/auth boundaries not yet fully audited')
            report['recovery_send'] = recovered
            evidence.write_text(json.dumps(report, indent=2) + '\n')
            return report
        finally:
            h.cleanup()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--binary', type=Path, required=True); parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--candidate-source', type=Path, default=ROOT, help='source for the candidate plugin upgrade when the verifier is in a separate checkout')
    args = parser.parse_args()
    print(json.dumps(run_cases([0], lambda _: run(args.binary.resolve(), args.evidence.resolve(), args.candidate_source.resolve()), 1)[0]))
