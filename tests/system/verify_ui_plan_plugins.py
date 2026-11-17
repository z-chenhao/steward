#!/usr/bin/env python3
"""Real Owner activation, Main streaming prefix, managed Plan, cold recovery."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import io
import json
import re
from urllib.parse import urlencode
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading
import time
from fixture_pool import run as run_process, run_cases
from event_broker import Broker
from feishu_sandbox import Sandbox
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness, require


def read_published_view(h, kind, ref, source=''):
 routes = {'plan':'/ui/plan','cards':'/ui/cards','table':'/ui/table','charts':'/ui/chart','forms':'/ui/form-view','meetings':'/ui/form-view','actions':'/api/ui/action'}
 query = dict(ref=ref)
 if kind == 'actions': query['source'] = source
 body = h.http(routes[kind] + '?' + urlencode(query), headers={'Sec-Fetch-Dest':'iframe'})
 if isinstance(body, str): body = re.sub(r'nonce="[A-Za-z0-9+/=_-]+"', 'nonce="<per-response>"', body)
 result = dict(body=body)
 if kind in ('cards','table','charts','forms'):
  result['state'] = h.http('/api/ui/state?' + urlencode(dict(ref=ref)))
 return result


def read_deactivated_view(h, kind, ref, prior):
 # Deactivate stops the entire graph, including the current workspace owner.
 # Original published resources remain readable without repairing selection.
 require(h.command('plugin','status')['graph_revision'] == '', 'cold startup reactivated plugins')
 observed = read_published_view(h, kind, ref, prior['source'])
 require(observed == prior['value'], 'deactivation changed original published resource or state: ' + ref)
 h.http('/', expected=503)
 require(h.command('plugin','status')['graph_revision'] == '', 'historical read reactivated plugins')
 return dict(scope='cold published HTTP reference; current workspace unavailable',ref=ref,source=prior['source'],snapshot_sha256=hashlib.sha256(json.dumps(observed,sort_keys=True).encode()).hexdigest())

ROOT=Path(__file__).resolve().parents[2]
CONTRACT='steward.ui-plan-view/1'

class PlanEndpoint(Endpoint):
 def do_POST(self):
  raw_body=self.rfile.read(int(self.headers['Content-Length']));body=json.loads(raw_body)
  if not any('PLAN_GATE' in str(m.get('content','')) for m in body['messages'] if m['role']=='user'):
   self.rfile=io.BytesIO(raw_body)
   return super().do_POST()
  self.server.requests.append(dict(method='POST',body=body,session=self.headers.get('session_id')))
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Connection','close');self.end_headers()
  def chunk(delta,finish=None):
   value=dict(id='plan-fixture',object='chat.completion.chunk',model=body['model'],choices=[dict(index=0,delta=delta,finish_reason=finish)])
   self.wfile.write(('data: '+json.dumps(value)+'\n\n').encode());self.wfile.flush()
  if body['messages'][-1]['role']!='tool' and 'PLAN_GATE' in str(body['messages'][-1].get('content','')):
   raw=json.dumps(self.server.plan);cut=raw.index('Ship safe core',raw.index('"components"'))+len('Ship')
   chunk(dict(tool_calls=[dict(index=0,id='plan-call',type='function',function=dict(name='builtin__intelligent_ui_show',arguments=raw[:cut]))]))
   self.server.prefix_sent=time.time();self.server.ready.set()
   require(self.server.release.wait(60),'managed Plan preview not released')
   chunk(dict(tool_calls=[dict(index=0,function=dict(arguments=raw[cut:]))]));chunk({},'tool_calls')
  else:chunk(dict(content='Plan ready.'));chunk({},'stop')
  self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()

def prepare(h,binary,folder,version="1.28.0"):
 folder.mkdir(parents=True,exist_ok=True)
 package=folder/'ui-package';shutil.copytree(binary.parent/'plugins/intelligent-ui',package)
 original=ROOT/'internal/plugin/intelligentui/plugin.go';code=original.read_text();changed=code.replace('Version: "1.27.0"','Version: "'+version+'"',1)
 require(changed!=code and ('Version: "'+version+'"') in changed,'experimental fixture boundary changed')
 replacement=folder/'plugin.go';replacement.write_text(changed);overlay=folder/'overlay.json';replacements={str(original):str(replacement)}
 if version=='1.28.0':
  plan_source=ROOT/'internal/plugin/intelligentui/plan_view.go';plan_code=plan_source.read_text();variant=plan_code.replace('Plan: {{.Technology}}','Plan v124: {{.Technology}}',1);require(variant!=plan_code,'Plan variant template not changed');plan_replacement=folder/'plan_view.go';plan_replacement.write_text(variant);replacements[str(plan_source)]=str(plan_replacement)
 overlay.write_text(json.dumps(dict(Replace=replacements)))
 run_process(['go','build','-trimpath','-overlay',str(overlay),'-o',str(package/'plugin'),'./cmd/plugins/intelligent-ui'],cwd=ROOT,check=True,capture_output=True,text=True,timeout=90)
 manifest=json.loads((package/'steward.plugin.json').read_text());manifest['version']=version
 require(any(service['contract']==CONTRACT for service in manifest['provides']),'default package missing Plan service');(package/'steward.plugin.json').write_text(json.dumps(manifest))
 h.command('plugin','seal',str(package));installed=h.command('plugin','install',str(package))
 with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
  db.execute('BEGIN');baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
  graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0])
  policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
 binding=next(b for b in graph['bindings'] if b['instance_id']=='intelligent-ui');binding['package_digest']=installed['digest']
 target=h.command('plugin','prepare',text=json.dumps(graph))['revision'];policy['id']='plan-full-app-owner-'+version
 if CONTRACT not in policy['allowed_contracts']:policy['allowed_contracts'].append(CONTRACT)
 policy['cases']=[c for c in policy['cases'] if c['contract']!=CONTRACT]
 cases=json.loads((ROOT/'internal/plugin/intelligentui/testdata/original-plan.json').read_text());css=(ROOT/'internal/plugin/intelligentui/testdata/original-plan.css').read_text().replace('@media(max-width:760px){.native-plan summary{min-height:44px}}','html[data-host-compact] .native-plan summary{min-height:44px}')
 policy['cases'].append(dict(instance_id='intelligent-ui',contract=CONTRACT,input={},inputs=[dict(id='ui-plan',data=list(json.dumps(dict(plans=[dict(component=0,props=cases[0]['input'])])).encode()))],expected=dict(views=[dict(component=0,html=cases[0]['expected']['html'].replace('Plan: Go','Plan v124: Go' if version=='1.28.0' else 'Plan: Go'),css=css,compact_max_width=760)],error_code='')))
 # InputData.Data is Go []byte on the owner CLI wire, encoded as base64.
 import base64
 policy['cases'][-1]['inputs'][0]['data']=base64.b64encode(bytes(policy['cases'][-1]['inputs'][0]['data'])).decode()
 h.command('plugin','policy',text=json.dumps(policy));candidate='plan-full-app-'+version;h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=baseline,target=target,policy_id=policy['id'],goal='Verify managed Plan in actual App.')))
 decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
 return target,installed['digest']

def run(binary,evidence):
 evidence.parent.mkdir(parents=True,exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='steward-plan-app-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
  server=ThreadingHTTPServer(('127.0.0.1',0),PlanEndpoint);server.requests=[];server.mode='success';server.ready=threading.Event();server.release=threading.Event()
  server.plan=dict(title='Managed Plan gate',fallback='Ship safe core',components=[dict(name='Table',props=dict(title='Position zero',columns=['N'],rows=[['0']],source='Fixture')),dict(name='plan_visualization',props=dict(approach='Ship safe core',technology='Go',key_elements=['Loop','Guardian']))])
  thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();h=ChannelHarness(binary,Path(temp),platform,'')
  try:
   h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
   h.setup(metadata=dict(name='Plan peer',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{server.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=131072,max_output_tokens=4096,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
   # Fresh default bootstrap must already own Plan: no initial experiment select.
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');target=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
    graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(target,)).fetchone()[0]);digest=next(b['package_digest'] for b in graph['bindings'] if b['instance_id']=='intelligent-ui')
    manifest=json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?',(digest,)).fetchone()[0]);require(manifest['version']=='1.27.0' and any(s['contract']==CONTRACT for s in manifest['provides']),'default did not activate Plan1.15')
    base_policy_id=db.execute('SELECT policy_id FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0];owner_cases=db.execute("SELECT json_array_length(policy_json,'$.cases') FROM rsi_policy WHERE policy_id=?",(base_policy_id,)).fetchone()[0];require(owner_cases==387,'default Plan owner case missing')
   h.start();h.wait_runtime_ready('managed Plan App ready')
   published = {}
   def browser(phase,ref='',caption='Plan:'):
    if phase.startswith('disabled'): return read_deactivated_view(h, 'plan', ref, published[ref])
    out=evidence.parent/phase;env=h.env.copy();chrome=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists():env['STEWARD_BROWSER_EXECUTABLE']=str(chrome)
    result=run_process(['node',str(ROOT/'tests/system/ui_plan_plugins_browser.mjs'),h.url,str(out),'preview' if phase.endswith('preview') else 'formal',ref,caption],cwd=ROOT,env=env,capture_output=True,text=True,timeout=45)
    require(result.returncode==0,'actual Plan browser: '+result.stderr);
    report = json.loads((out/'report.json').read_text())
    if ref: published[ref] = dict(source=report.get('source',''), value=read_published_view(h, 'plan', ref, report.get('source','')))
    return report
   h.http('/api/message',dict(source_id='plan-full-app',content=[dict(type='text',text='PLAN_GATE')]))
   require(server.ready.wait(20),'real provider did not emit prefix')
   preview=browser('preview');prefix_upper_bound=preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','Plan delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');ref,saved,graph=db.execute('SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_plan_view_snapshot p USING(invocation_id)').fetchone();require(graph==target,'Plan accepted under wrong graph');require(db.execute("SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-plan%'").fetchone()[0]==0,'view reserved an effect')
   ref+='#1';formal=browser('formal',ref);selected=h.command('plugin','status');h.stop(kill=True);h.start();h.wait_runtime_ready('Plan cold recovery ready');require(h.command('plugin','status')==selected,'restart changed selection');restart=browser('restart',ref)
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT result_json FROM ui_plan_view_snapshot').fetchone()[0]==saved,'restart recomputed original view')
   # Publish a genuinely different template through the same real Owner path.
   h.stop();new_target,new_digest=prepare(h,binary,Path(temp)/'upgrade','1.28.0');h.start();h.wait_runtime_ready('Plan upgrade ready');browser('upgrade-retains-old',ref)
   server.ready.clear();server.release.clear();h.http('/api/message',dict(source_id='plan-upgrade',content=[dict(type='text',text='PLAN_GATE_NEW')]))
   require(server.ready.wait(20),'new version did not stream');new_preview=browser('upgrade-preview',caption='Plan v124:');new_prefix_upper_bound=new_preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==2 and all(d['status']=='sent' for d in s['deliveries']),'new Plan delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');new_ref,new_graph=db.execute('SELECT w.ui_ref,p.graph_revision FROM ui_widget w JOIN ui_plan_view_snapshot p USING(invocation_id) WHERE p.graph_revision=?',(new_target,)).fetchone();require(new_graph==new_target,'new Plan changed graph');snapshots=db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_plan_view_snapshot ORDER BY invocation_id').fetchall()
   new_ref+='#1';browser('upgraded',new_ref,'Plan v124:');h.stop()
   candidate='plan-full-app-rollback';h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=new_target,target=target,policy_id=base_policy_id,goal='Retain both original Plan versions.')));decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=new_target,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
   h.start();h.wait_runtime_ready('Plan rollback ready');browser('rollback-old',ref);browser('rollback-new',new_ref,'Plan v124:');h.stop()
   h.command('plugin','deactivate',text=json.dumps(dict(operation_id='plan-disable',expected_graph_revision=target)));h.start()
   # Historical reads do not depend on current work readiness or a repair select.
   browser('disabled-old',ref);browser('disabled-new',new_ref,'Plan v124:');require(h.command('plugin','status')['graph_revision']=='','cold start reactivated plugins')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_plan_view_snapshot ORDER BY invocation_id').fetchall()==snapshots,'version switch recomputed accepted views')
   report=dict(checks=['actual App and production bridge','fresh default40 Plan1.27 with349 independent Owner cases','incomplete streaming Plan from original package','formal original ordinal1 iframe','SIGKILL cold recovery without reselect or view recomputation'],graph=graph,package_digest=digest,ref=ref,preview=preview,formal=formal,restart=restart,snapshot_sha256=hashlib.sha256(saved.encode()).hexdigest(),prefix_to_browser_upper_bound_seconds=prefix_upper_bound,binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),version_upgrade=dict(graph=new_target,package_digest=new_digest,ref=new_ref,preview=new_preview,prefix_to_browser_upper_bound_seconds=new_prefix_upper_bound),version_checks=['old reference immediately after upgrade','real new prefix and template','both references after rollback','both references after deactivation/cold start without reselect','all snapshot bytes unchanged'],default_owner_cases=owner_cases,scope='fresh default40 UI1.27 and independent1.28 template upgrade; remaining renderers and final delivery gates incomplete')
   evidence.write_text(json.dumps(report,indent=2)+'\n');return report
  finally:
   server.release.set();h.cleanup()
   (evidence.parent/'runtime-state.json').write_text(json.dumps(h.failure_state(),indent=2))
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.row_factory=sqlite3.Row;db.execute('BEGIN');diagnostics={table:[dict(r) for r in db.execute('SELECT * FROM '+table)] for table in ['rsi_run','ui_widget','ui_plan_view_snapshot']}
   (evidence.parent/'view-state.json').write_text(json.dumps(diagnostics,indent=2))
   server.shutdown();server.server_close();thread.join()

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();report=run_cases([0],lambda _:run(a.binary.resolve(),a.evidence.resolve()),1)[0];print(json.dumps(report))
