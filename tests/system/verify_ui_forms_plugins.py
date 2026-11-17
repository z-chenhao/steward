#!/usr/bin/env python3
"""Real Owner activation, Main streaming prefix, managed Form, cold recovery."""
import argparse
import base64
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
from fixture_pool import run as run_process, run_cases
from event_broker import Broker
from feishu_sandbox import Sandbox
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness, require

from verify_ui_plan_plugins import read_published_view, read_deactivated_view

ROOT=Path(__file__).resolve().parents[2]
CONTRACT='steward.ui-form-view/1'

class FormEndpoint(Endpoint):
 def do_POST(self):
  raw_body=self.rfile.read(int(self.headers['Content-Length']));body=json.loads(raw_body)
  if not any('FORM_GATE' in str(m.get('content','')) for m in body['messages'] if m['role']=='user'):
   self.rfile=io.BytesIO(raw_body)
   return super().do_POST()
  self.server.requests.append(dict(method='POST',body=body,session=self.headers.get('session_id')))
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Connection','close');self.end_headers()
  def chunk(delta,finish=None):
   value=dict(id='form-fixture',object='chat.completion.chunk',model=body['model'],choices=[dict(index=0,delta=delta,finish_reason=finish)])
   self.wfile.write(('data: '+json.dumps(value)+'\n\n').encode());self.wfile.flush()
  if body['messages'][-1]['role']!='tool' and 'FORM_GATE' in str(body['messages'][-1].get('content','')):
   raw=json.dumps(self.server.form);cut=raw.rindex('"boolean"')+len('"boolean"}')
   chunk(dict(tool_calls=[dict(index=0,id='form-call',type='function',function=dict(name='builtin__intelligent_ui_show',arguments=raw[:cut]))]))
   self.server.prefix_sent=time.time();self.server.ready.set()
   require(self.server.release.wait(60),'managed Form preview not released')
   chunk(dict(tool_calls=[dict(index=0,function=dict(arguments=raw[cut:]))]));chunk({},'tool_calls')
  else:chunk(dict(content='Form ready.'));chunk({},'stop')
  self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()

def prepare(h,binary,folder,version="1.28.0"):
 folder.mkdir(parents=True,exist_ok=True)
 package=folder/'ui-package';shutil.copytree(binary.parent/'plugins/intelligent-ui',package)
 original=ROOT/'internal/plugin/intelligentui/plugin.go';code=original.read_text();changed=code.replace('Version: "1.27.0"','Version: "'+version+'"',1)
 require(changed!=code,'independent version did not change')
 replacement=folder/'plugin.go';replacement.write_text(changed);overlay=folder/'overlay.json';replacements={str(original):str(replacement)}
 form_source=ROOT/'internal/plugin/intelligentui/form_view.go';code=form_source.read_text();variant=code.replace('Title: props.Title','Title: "Form v124: " + props.Title',1).replace('value = "false"','value = "true"',1);require(variant!=code,'form default variant missing')
 form_replacement=folder/'form_view.go';form_replacement.write_text(variant);replacements[str(form_source)]=str(form_replacement)
 overlay.write_text(json.dumps(dict(Replace=replacements)))
 run_process(['go','build','-trimpath','-overlay',str(overlay),'-o',str(package/'plugin'),'./cmd/plugins/intelligent-ui'],cwd=ROOT,check=True,capture_output=True,text=True,timeout=90)
 manifest=json.loads((package/'steward.plugin.json').read_text());manifest['version']=version
 require(any(service['contract']==CONTRACT for service in manifest['provides']),'default package missing Form service');(package/'steward.plugin.json').write_text(json.dumps(manifest))
 h.command('plugin','seal',str(package));installed=h.command('plugin','install',str(package))
 with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
  db.execute('BEGIN');baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
  graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0])
  policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
 binding=next(b for b in graph['bindings'] if b['instance_id']=='intelligent-ui');binding['package_digest']=installed['digest']
 target=h.command('plugin','prepare',text=json.dumps(graph))['revision'];policy['id']='form-full-app-owner-'+version
 if CONTRACT not in policy['allowed_contracts']:policy['allowed_contracts'].append(CONTRACT)
 policy['cases']=[c for c in policy['cases'] if c['contract']!=CONTRACT]
 oracle=json.loads((ROOT/'internal/rsi/builtin/ui-form-views-original.json').read_text())
 for case in oracle:
  expected=case['expected']
  for view in expected['views']:
   view['title']='Form v124: '+view['title'];view['fields']=[dict(field,default='true') if field['type']=='boolean' else field for field in view['fields']]
  policy['cases'].append(dict(instance_id='intelligent-ui',contract=CONTRACT,input={},inputs=[dict(id='ui-form-views',data=base64.b64encode(json.dumps(case['facts']).encode()).decode())],expected=expected))
 h.command('plugin','policy',text=json.dumps(policy));candidate='form-full-app-'+version;h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=baseline,target=target,policy_id=policy['id'],goal='Verify managed Form in actual App.')))
 decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
 return target,installed['digest']

def run(binary,evidence):
 evidence.parent.mkdir(parents=True,exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='steward-form-app-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
  server=ThreadingHTTPServer(('127.0.0.1',0),FormEndpoint);server.requests=[];server.mode='success';server.ready=threading.Event();server.release=threading.Event()
  props=dict(title='Original form',fields=[dict(name='notes',label='Notes',type='textarea'),dict(name='confirm',label='Confirm',type='boolean')])
  server.form=dict(title='Managed Form gate',fallback='Original form',components=[dict(name='plan_visualization',props=dict(approach='Original',technology='Go',key_elements=[])),dict(name='steward.form',props=props)])
  thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();h=ChannelHarness(binary,Path(temp),platform,'')
  try:
   h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
   h.setup(metadata=dict(name='Form peer',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{server.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=131072,max_output_tokens=4096,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
   # Fresh default bootstrap must already own Form: no initial experiment select.
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');target=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
    graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(target,)).fetchone()[0]);digest=next(b['package_digest'] for b in graph['bindings'] if b['instance_id']=='intelligent-ui')
    manifest=json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?',(digest,)).fetchone()[0]);require(manifest['version']=='1.27.0' and any(s['contract']==CONTRACT for s in manifest['provides']),'default did not activate Form1.27')
    base_policy_id=db.execute('SELECT policy_id FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0];owner_cases=db.execute("SELECT json_array_length(policy_json,'$.cases') FROM rsi_policy WHERE policy_id=?",(base_policy_id,)).fetchone()[0];require(owner_cases==387,'default Form owner case missing')
   h.start();h.wait_runtime_ready('managed Form App ready')
   published = {}
   def browser(phase,ref='',caption='Original form'):
    if phase.startswith('disabled'): return read_deactivated_view(h, 'forms', ref, published[ref])
    out=evidence.parent/phase;env=h.env.copy();chrome=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists():env['STEWARD_BROWSER_EXECUTABLE']=str(chrome)
    result=run_process(['node',str(ROOT/'tests/system/ui_form_plugins_browser.mjs'),h.url,str(out),'formal-upgraded' if phase=='upgraded' else phase,ref,caption],cwd=ROOT,env=env,capture_output=True,text=True,timeout=45)
    require(result.returncode==0,'actual Form browser: '+result.stderr);
    report = json.loads((out/'report.json').read_text())
    if ref: published[ref] = dict(source=report.get('source',''), value=read_published_view(h, 'forms', ref, report.get('source','')))
    return report
   h.http('/api/message',dict(source_id='form-full-app',content=[dict(type='text',text='FORM_GATE')]))
   require(server.ready.wait(20),'real provider did not emit prefix')
   preview=browser('preview');prefix_upper_bound=preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','Form delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');ref,saved,graph=db.execute('SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_form_view_snapshot p USING(invocation_id)').fetchone();require(graph==target,'Form accepted under wrong graph');require(db.execute("SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-form%'").fetchone()[0]==0,'view reserved an effect')
   ref+='#1';formal=browser('formal',ref);selected=h.command('plugin','status');h.stop(kill=True);h.start();h.wait_runtime_ready('Form cold recovery ready');require(h.command('plugin','status')==selected,'restart changed selection');restart=browser('restart',ref);require(restart['states']==formal['states'],'cold recovery rewrote original CAS/selection')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT result_json FROM ui_form_view_snapshot').fetchone()[0]==saved,'restart recomputed original view')
   # Publish a genuinely different template through the same real Owner path.
   h.stop();new_target,new_digest=prepare(h,binary,Path(temp)/'upgrade','1.28.0');h.start();h.wait_runtime_ready('Form upgrade ready');browser('upgrade-retains-old',ref)
   server.ready.clear();server.release.clear();h.http('/api/message',dict(source_id='form-upgrade',content=[dict(type='text',text='FORM_GATE_NEW')]))
   require(server.ready.wait(20),'new version did not stream');new_preview=browser('upgrade-preview',caption='Form v124: Original form');new_prefix_upper_bound=new_preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==2 and all(d['status']=='sent' for d in s['deliveries']),'new Form delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');new_ref,new_graph=db.execute('SELECT w.ui_ref,p.graph_revision FROM ui_widget w JOIN ui_form_view_snapshot p USING(invocation_id) WHERE p.graph_revision=?',(new_target,)).fetchone();require(new_graph==new_target,'new Form changed graph');snapshots=db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_form_view_snapshot ORDER BY invocation_id').fetchall()
   new_ref+='#1';browser('upgraded',new_ref,'Form v124: Original form');h.stop()
   candidate='form-full-app-rollback';h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=new_target,target=target,policy_id=base_policy_id,goal='Retain both original Form versions.')));decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=new_target,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
   h.start();h.wait_runtime_ready('Form rollback ready');browser('rollback-old',ref);browser('rollback-new',new_ref,'Form v124: Original form');h.stop()
   h.command('plugin','deactivate',text=json.dumps(dict(operation_id='form-disable',expected_graph_revision=target)));h.start()
   # Historical reads do not depend on current work readiness or a repair select.
   browser('disabled-old',ref);browser('disabled-new',new_ref,'Form v124: Original form');require(h.command('plugin','status')['graph_revision']=='','cold start reactivated plugins')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_form_view_snapshot ORDER BY invocation_id').fetchall()==snapshots,'version switch recomputed accepted views')
   h.stop()
   control_env=h.env.copy();control_env.update(STEWARD_TEST_FORM_CONFIG=str(h.config),STEWARD_TEST_FORM_REF=ref)
   control=run_process(['go','test','./internal/app','-run','^TestUIFormViewActualForgetRevokesOriginalViewAndDraft$','-count=1'],cwd=ROOT,env=control_env,capture_output=True,text=True,timeout=90)
   (evidence.parent/'forget-control.log').write_text(control.stdout+control.stderr)
   require(control.returncode==0,'real App Forget fence failed: '+control.stderr+control.stdout)
   report=dict(checks=['actual App and production bridge','fresh default40 Form1.27 with387 independent Owner cases','incomplete streaming Form from original package','formal original ordinal1 native controls','SIGKILL cold recovery without reselect or view recomputation'],graph=graph,package_digest=digest,ref=ref,preview=preview,formal=formal,restart=restart,snapshot_sha256=hashlib.sha256(saved.encode()).hexdigest(),prefix_to_browser_upper_bound_seconds=prefix_upper_bound,binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),version_upgrade=dict(graph=new_target,package_digest=new_digest,ref=new_ref,preview=new_preview,prefix_to_browser_upper_bound_seconds=new_prefix_upper_bound),version_checks=['old reference immediately after upgrade','real new prefix and template','both references after rollback','both references after deactivation/cold start without reselect','all snapshot bytes unchanged'],default_owner_cases=owner_cases,scope='fresh default40 UI1.27 and independent1.28 defaults upgrade; remaining renderers and final delivery gates incomplete')
   evidence.write_text(json.dumps(report,indent=2)+'\n');return report
  finally:
   server.release.set();h.cleanup()
   (evidence.parent/'runtime-state.json').write_text(json.dumps(h.failure_state(),indent=2))
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.row_factory=sqlite3.Row;db.execute('BEGIN');diagnostics={form:[dict(r) for r in db.execute('SELECT * FROM '+form)] for form in ['rsi_run','ui_widget','ui_form_view_snapshot']}
   (evidence.parent/'view-state.json').write_text(json.dumps(diagnostics,indent=2))
   server.shutdown();server.server_close();thread.join()

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();report=run_cases([0],lambda _:run(a.binary.resolve(),a.evidence.resolve()),1)[0];print(json.dumps(report))
