#!/usr/bin/env python3
"""Real Main -> Code Mode -> UI tool -> durable projection -> sandboxed browser."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
import time
from urllib.parse import quote

from event_broker import Broker
from codemode_peer import MAIN_DIRECT
from feishu_sandbox import Sandbox
from fixture_pool import run_cases
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness, require


def run(binary, evidence):
    with tempfile.TemporaryDirectory(prefix='steward-intelligent-ui-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
        server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
        server.requests, server.mode = [], 'success'
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        h = ChannelHarness(binary, Path(temp), platform, '')
        try:
            h.config.write_text(h.config.read_text() + 'llm:\n  provider_bridge_command: ' + json.dumps(str(binary)) + '\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: ' + json.dumps(broker.url) + '\n')
            # Seven complete code-bearing answers plus both streaming paths need
            # room for their history; this peer does not implement compaction.
            h.setup(metadata=dict(name='Intelligent UI peer', provider_kind='openai_compatible', endpoint=f'http://127.0.0.1:{server.server_port}/v1', wire_api='openai_completions', model_id='fixture-model', context_window=131072, max_output_tokens=4096, compatibility=dict(tool_result_format='openai'), capabilities=dict(text_input=True)))
            def visual(title,fallback,html,functions,css=''):
                return dict(title=title,fallback=fallback,components=[dict(name='generateSandboxedUi',props=dict(initialHeight=240,placeholderMessages=['Preparing the answer'],css=css,html=html,jsFunctions='function initialize(){'+functions+'}',jsExpressions=['initialize()']))])
            server.widgets = [
                dict(title='Comparison',fallback='A is 10 and B is 20.',components=[dict(name='Table',props=dict(title='Comparison',columns=['Option','Value'],rows=[['A','10'],['B','<img src=x onerror=unsafe()>']],source='User-provided fixture'))]),
                visual('Calculator','The calculator doubles its input. No network or Host access.',
                       '<label>Number <input id="n" type="number" value="21"></label><div class="actions"><button id="compute">Compute</button><button id="followup">Discuss this result</button></div><output id="answer">42</output><output id="security"></output><label>Amplitude <input id="amplitude" type="range" min="1" max="40" value="20"></label><svg id="diagram" viewBox="0 0 300 120" role="img" aria-label="Architecture and simulation"><g class="c-teal"><rect class="node" x="10" y="10" width="90" height="60" rx="8"/><text class="t" x="25" y="45">Input</text></g><path class="arr" d="M100 40H180"/><circle id="wave" class="c-purple" cx="230" cy="45" r="20"/></svg>',
                       'if(typeof window.earlyRTC==="function")throw Error("bootstrap bypass");const root=document.body;root.querySelector("#compute").addEventListener("click",()=>{const n=Number(root.querySelector("#n").value);root.querySelector("#answer").textContent=Number.isFinite(n)?String(n*2):"Invalid number"});root.querySelector("#amplitude").addEventListener("input",event=>root.querySelector("#wave").setAttribute("r",event.target.value));root.querySelector("#followup").addEventListener("click",()=>Websandbox.connection.remote.sendPrompt({text:"Discuss the computed result: "+root.querySelector("#answer").textContent}));let blocked=0;try{parent.document.body}catch{blocked++}try{localStorage.getItem("token")}catch{blocked++}fetch("/api/status").then(()=>root.querySelector("#security").textContent="NETWORK_ALLOWED").catch(()=>root.querySelector("#security").textContent="blocked:"+blocked);',
                       '</style><script>window.earlyRTC=window.RTCPeerConnection</script><style>'),
                # An actual script exception with the browser's notification
                # message must still fail; only error-less notifications survive.
                visual('Broken visual','The visual failed; this text remains available.','<p>Visual</p>','throw Error("ResizeObserver loop completed with undelivered notifications.")'),
            ]
            templates = Path(__file__).resolve().parents[2] / 'internal/skill/builtin/intelligent_ui/templates'
            for name in ('architecture-diagram', 'scenario-comparison', 'three-dimensional', 'geographic-overview'):
                server.widgets.append(json.loads((templates / (name + '.json')).read_text()))
            server.widgets[0] = dict(text='First, compare the exact values.\n\n**调用与回传：**用户向 Main 提问。', **server.widgets[0])
            server.widgets[1] = dict(text='Next, explore a value with the calculator.', **server.widgets[1])
            server.mode = 'intelligent-ui'
            h.start()
            h.wait_state(lambda status: status['readiness']=='ready', 'UI runtime ready', lambda _: h.http('/api/status'))
            h.http('/api/message', dict(source_id='intelligent-ui-request', content=[dict(type='text', text='SHOW_INTELLIGENT_UI: compare the values and show a small calculator.')]))
            first = h.wait(lambda s: len(s['deliveries']) == 1 and s['deliveries'][0]['status'] == 'sent', 'UI tool and reply delivered')
            history = h.http('/api/conversation')
            require(not any('steward-result' in json.dumps(m) for m in history), 'UI protocol leaked into prose')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                before = db.execute('SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL').fetchone()[0]
                diagnostics = db.execute("SELECT tool_name,status,result_json FROM tool_invocation WHERE tool_name='builtin__intelligent_ui_show'").fetchall() + db.execute("SELECT 'run',status,error_code FROM run").fetchall() + db.execute("SELECT 'generation',status,error_code FROM model_generation").fetchall()
                evidence.parent.joinpath('requests.json').write_text(json.dumps([r['body'] for r in server.requests if r.get('method') == 'POST']))
                require(before == len(server.widgets), f'UI staging lost or repeated artifacts: {before}/{len(server.widgets)}; {diagnostics}')
                require(db.execute("SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__intelligent_ui_show' AND status='succeeded' AND attempt_count=1").fetchone()[0] == len(server.widgets), 'UI bypassed the ordinary executor')
                require(db.execute("SELECT count(*) FROM tool_invocation WHERE tool_name IN ('builtin__skill_list','builtin__skill_view') AND status='succeeded' AND attempt_count=1").fetchone()[0] == 2, 'Skill discovery or loading bypassed the ordinary executor')
                ref = db.execute("SELECT ui_ref FROM ui_widget WHERE json_extract(document_json,'$.title')='Calculator'").fetchone()[0]+'#0'
            response = h.http('/ui/widget?ref=' + quote(ref,safe=''))
            require('open-generative-ui' in response and 'Websandbox' in response and 'open-generative-ui-activity' in response, 'upstream activity or bridge contract missing')
            h.http('/ui/widget?ref=' + quote(ref,safe=''), auth=False, expected=401)
            h.http('/ui/widget?ref=ui:outside', expected=404)
            h.browser(evidence.parent / 'intelligent-ui-browser', 'intelligent-ui')
            h.wait(lambda s: len(s['inputs']) == 2 and len(s['deliveries']) == 2 and all(d['status'] == 'sent' for d in s['deliveries']), 'explicit Widget follow-up accepted')
            # Hold the actual provider at an unfinished argument prefix, then
            # hold the Code Mode final prose after its nested UI calls staged.
            # The browser must display safe previews before either can commit.
            for mode in ('direct', 'code'):
                server.preview_path = mode
                server.preview_ready, server.preview_release = threading.Event(), threading.Event()
                server.preview_advance = threading.Event()
                server.stream_widgets = [
                    dict(title='Streaming comparison', fallback='A is 10 and B is 20.', components=[dict(name='Table', props=dict(title='Streaming comparison',columns=['Name','Value'],rows=[['A','10'],['B','20']],source='Fixture data'))]),
                    visual('Streaming visual', 'Scripts activate after delivery.', '<p id="preview-text">Script stays off</p><svg id="stream-diagram" viewBox="0 0 200 80" fill="currentColor"><text id="main-node" x="20" y="30">Main</text><text id="root-node" x="100" y="30">Root</text></svg><button id="preview-button">Compute</button><script>parent.postMessage({previewExecuted:true},"*")</script>', 'document.querySelector("#preview-text").textContent="Activated after delivery";'),
                    dict(title='Weekly roadmap', fallback='Week 1–2: interview customers. Week 3–4: build an MVP.', components=[dict(name='generateSandboxedUi', props=dict(initialHeight=240, placeholderMessages=[], css='', html='<p>Script stays off</p><p id="weekly-roadmap">Week 1–2: interview customers. Week 3–4: build an MVP.</p>', jsFunctions='', jsExpressions=''))]),
                ]
                server.mode = 'intelligent-ui-stream'
                previous_deliveries = len(h.rows()['deliveries'])
                h.http('/api/message', dict(source_id='stream-'+mode, content=[dict(type='text',text='STREAM_UI_'+mode.upper())]))
                failures = []
                def release_preview():
                    try:
                        if mode == 'direct':
                            advance = evidence.parent / ('preview-'+mode) / 'preview-advance.json'
                            advance_deadline = time.monotonic()+25
                            while not advance.exists() and time.monotonic()<advance_deadline:
                                time.sleep(.05)
                            require(advance.exists(), 'browser did not request an incremental drawing')
                            server.preview_advance.set()
                        observed = evidence.parent / ('preview-'+mode) / 'preview-observed.json'
                        deadline = time.monotonic()+30
                        while not observed.exists() and time.monotonic()<deadline:
                            time.sleep(.05)
                        require(observed.exists(), 'browser did not observe the preview')
                        with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                            db.execute('BEGIN')
                            require(db.execute('SELECT count(*) FROM ui_widget WHERE message_id IS NOT NULL').fetchone()[0] == before, 'preview prematurely bound a reply')
                            require(db.execute("SELECT count(*) FROM run_trace WHERE json_extract(event_json,'$.kind') LIKE 'ui_preview_%'").fetchone()[0] == 0, 'preview entered durable trace')
                    except Exception as error:
                        failures.append(error)
                    finally:
                        server.preview_release.set()
                release = threading.Thread(target=release_preview)
                try:
                    require(server.preview_ready.wait(15), mode+' preview gate did not open')
                    release.start()
                    h.browser(evidence.parent / ('preview-'+mode), 'intelligent-ui-preview-'+mode)
                finally:
                    server.preview_release.set()
                    if release.ident is not None:
                        release.join()
                require(not failures, str(failures))
                h.wait(lambda state: len(state['deliveries']) == previous_deliveries+1 and all(d['status']=='sent' for d in state['deliveries']), mode+' stream committed')
                before += 3
                with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                    row = db.execute("SELECT w.document_json,i.arguments_json FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE json_extract(w.document_json,'$.title')='Weekly roadmap' AND w.message_id IS NOT NULL ORDER BY w.created_at DESC LIMIT 1").fetchone()
                    require(row is not None, 'static roadmap preview disappeared at commit')
                    require(json.loads(row[0])['components'][0]['props']['jsExpressions'] == [], 'artifact did not normalize the empty script list')
                    require(json.loads(row[1])['components'][0]['props']['jsExpressions'] == '', 'accepted request was rewritten')
                h.browser(evidence.parent / ('preview-'+mode+'-committed'), 'intelligent-ui-preview-committed')
            h.stop(kill=True)
            h.start()
            h.http('/ui/widget?ref=' + quote(ref,safe=''))
            h.browser(evidence.parent / 'preview-restarted', 'intelligent-ui-preview-committed')
            with closing(sqlite3.connect(h.root / 'data/state.db')) as db, db:
                require(db.execute('SELECT count(*) FROM ui_widget').fetchone()[0] == before, 'restart regenerated UI')
                runs={r[0] for r in db.execute('SELECT run_id FROM run')}
            requests = [r['body'] for r in server.requests if r.get('method') == 'POST' and r.get('session') in runs and r['body'].get('tools')]
            require(len(requests)>=3, 'business HTTP requests not captured')
            require(all({t['function']['name'] for t in body['tools']} == MAIN_DIRECT for body in requests), 'frequent UI and discovery declarations differ from the production surface')
            for body in requests:
                ui_tool = next(t['function'] for t in body['tools'] if t['function']['name'] == 'builtin__intelligent_ui_show')
                signatures = ui_tool['parameters']['properties']['components']['items']['properties']['props']['description']
                require('generateSandboxedUi:{initialHeight:number' in signatures and 'jsExpressions:string[]' in signatures, 'actual provider lost the compact exact props signatures')
            require(all(sum(m['role'] == 'system' for m in body['messages']) == 1 for body in requests), 'multiple system messages reached the provider')
            require(all('<skills>' not in m['content'] and '<skill_index>' not in m['content'] and 'Present a compact native component' not in m['content'] for body in requests for m in body['messages'] if m['role']=='system'), 'business requests preloaded the Skill catalog')
            return dict(checks=['real Go model peer and Main Code Mode', 'Main-owned durable UI tool; upstream Table and generateSandboxedUi contract', 'opaque origin, storage/parent/network blocked', 'upstream shared theme/SVG/form styles, calculator, SVG simulation, expansion, failure/text fallback, mobile', 'Host-confirmed ordinary message follow-up', 'ordered public paragraphs and visuals; direct incremental DOM identity, inert preview, theme and mobile rendering; Code Mode staging', 'SIGKILL/restart without regeneration', 'one Agent system message and the fixed frequent direct tool surface'], widget_count=before, projection=first['projections'])
        finally:
            h.cleanup()
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    report = run_cases([0], lambda _: run(args.binary.resolve(), args.evidence.resolve()), 1)[0]
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(dict(checks=report['checks'], evidence=str(args.evidence))))
