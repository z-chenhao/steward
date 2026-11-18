#!/usr/bin/env python3
"""Real Owner activation, Main streaming prefix, managed Table, cold recovery."""
import argparse
from owner_inventory import check_default_owner_policy
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
CONTRACT='steward.ui-table-view/1'

class TableEndpoint(Endpoint):
 def do_POST(self):
  raw_body=self.rfile.read(int(self.headers['Content-Length']));body=json.loads(raw_body)
  if not any('TABLE_GATE' in str(m.get('content','')) for m in body['messages'] if m['role']=='user'):
   self.rfile=io.BytesIO(raw_body)
   return super().do_POST()
  self.server.requests.append(dict(method='POST',body=body,session=self.headers.get('session_id')))
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Connection','close');self.end_headers()
  def chunk(delta,finish=None):
   value=dict(id='table-fixture',object='chat.completion.chunk',model=body['model'],choices=[dict(index=0,delta=delta,finish_reason=finish)])
   self.wfile.write(('data: '+json.dumps(value)+'\n\n').encode());self.wfile.flush()
  if body['messages'][-1]['role']!='tool' and 'TABLE_GATE' in str(body['messages'][-1].get('content','')):
   raw=json.dumps(self.server.table);cut=raw.index('Ship safe core',raw.index('"components"'))+len('Ship')
   chunk(dict(tool_calls=[dict(index=0,id='table-call',type='function',function=dict(name='builtin__intelligent_ui_show',arguments=raw[:cut]))]))
   self.server.prefix_sent=time.time();self.server.ready.set()
   require(self.server.release.wait(60),'managed Table preview not released')
   chunk(dict(tool_calls=[dict(index=0,function=dict(arguments=raw[cut:]))]));chunk({},'tool_calls')
  else:chunk(dict(content='Table ready.'));chunk({},'stop')
  self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()

def prepare(h,binary,folder,version="1.28.0"):
 folder.mkdir(parents=True,exist_ok=True)
 package=folder/'ui-package';shutil.copytree(binary.parent/'plugins/intelligent-ui',package)
 original=ROOT/'internal/plugin/intelligentui/plugin.go';code=original.read_text();changed=code.replace('Version: "1.27.0"','Version: "'+version+'"',1)
 require(changed!=code and ('Version: "'+version+'"') in changed,'experimental fixture boundary changed')
 replacement=folder/'plugin.go';replacement.write_text(changed);overlay=folder/'overlay.json';replacements={str(original):str(replacement)}
 if version=='1.28.0':
  table_source=ROOT/'internal/plugin/intelligentui/table_view.go';table_code=table_source.read_text();variant=table_code.replace('facts, err := json.Marshal(props)', 'display := props\n display.Title = \"Table v124: \" + props.Title\n facts, err := json.Marshal(display)',1).replace('}{props, base64.StdEncoding.EncodeToString(facts)}','}{display, base64.StdEncoding.EncodeToString(facts)}',1);require(variant!=table_code,'Table variant template not changed');table_replacement=folder/'table_view.go';table_replacement.write_text(variant);replacements[str(table_source)]=str(table_replacement)
 overlay.write_text(json.dumps(dict(Replace=replacements)))
 run_process(['go','build','-trimpath','-overlay',str(overlay),'-o',str(package/'plugin'),'./cmd/plugins/intelligent-ui'],cwd=ROOT,check=True,capture_output=True,text=True,timeout=90)
 manifest=json.loads((package/'steward.plugin.json').read_text());manifest['version']=version
 require(any(service['contract']==CONTRACT for service in manifest['provides']),'default package missing Table service');(package/'steward.plugin.json').write_text(json.dumps(manifest))
 h.command('plugin','seal',str(package));installed=h.command('plugin','install',str(package))
 with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
  db.execute('BEGIN');baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
  graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0])
  policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
 binding=next(b for b in graph['bindings'] if b['instance_id']=='intelligent-ui');binding['package_digest']=installed['digest']
 target=h.command('plugin','prepare',text=json.dumps(graph))['revision'];policy['id']='table-full-app-owner-'+version
 if CONTRACT not in policy['allowed_contracts']:policy['allowed_contracts'].append(CONTRACT)
 policy['cases']=[c for c in policy['cases'] if c['contract']!=CONTRACT]
 oracle=json.loads((ROOT/'internal/rsi/builtin/ui-table-original.json').read_text())
 if version=='1.28.0':
  for view in oracle['expected']['views']:
   import re
   encoded=re.search(r'data-table-facts="([^"]+)"',view['html']).group(1)
   display=json.loads(base64.b64decode(encoded));display['title']='Table v124: '+display['title']
   view['html']=view['html'].replace(encoded,base64.b64encode(json.dumps(display,ensure_ascii=False,separators=(',',':')).encode()).decode(),1).replace('Original table evidence','Table v124: Original table evidence')
 policy['cases'].append(dict(instance_id='intelligent-ui',contract=CONTRACT,input={},inputs=[dict(id='ui-tables',data=list(json.dumps(oracle['facts']).encode()))],expected=oracle['expected']))
 # InputData.Data is Go []byte on the owner CLI wire, encoded as base64.
 policy['cases'][-1]['inputs'][0]['data']=base64.b64encode(bytes(policy['cases'][-1]['inputs'][0]['data'])).decode()
 h.command('plugin','policy',text=json.dumps(policy));candidate='table-full-app-'+version;h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=baseline,target=target,policy_id=policy['id'],goal='Verify managed Table in actual App.')))
 decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
 return target,installed['digest']

def run(binary,evidence):
 evidence.parent.mkdir(parents=True,exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='steward-table-app-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
  server=ThreadingHTTPServer(('127.0.0.1',0),TableEndpoint);server.requests=[];server.mode='success';server.ready=threading.Event();server.release=threading.Event()
  original=json.loads((ROOT/'internal/plugin/intelligentui/testdata/original-table.json').read_text())
  props=dict(original['props']);props['source']='Ship safe core'
  server.table=dict(title='Managed Table gate',fallback='Original values',components=[dict(name='plan_visualization',props=dict(approach='Original',technology='Go',key_elements=[])),dict(name='Table',props=props)])
  thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();h=ChannelHarness(binary,Path(temp),platform,'')
  try:
   h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
   h.setup(metadata=dict(name='Table peer',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{server.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=131072,max_output_tokens=4096,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
   # Fresh default bootstrap must already own Table: no initial experiment select.
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');target=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
    graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(target,)).fetchone()[0]);digest=next(b['package_digest'] for b in graph['bindings'] if b['instance_id']=='intelligent-ui')
    manifest=json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?',(digest,)).fetchone()[0]);require(manifest['version']=='1.27.0' and any(s['contract']==CONTRACT for s in manifest['provides']),'default did not activate Table1.15')
    base_policy_id=db.execute('SELECT policy_id FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0];owner_cases = check_default_owner_policy(db, graph, base_policy_id)
   h.start();h.wait_runtime_ready('managed Table App ready')
   published = {}
   def browser(phase,ref='',caption='Original table evidence'):
    if phase.startswith('disabled'): return read_deactivated_view(h, 'table', ref, published[ref])
    out=evidence.parent/phase;env=h.env.copy();chrome=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists():env['STEWARD_BROWSER_EXECUTABLE']=str(chrome)
    result=run_process(['node',str(ROOT/'tests/system/ui_table_plugins_browser.mjs'),h.url,str(out),'formal-upgraded' if phase=='upgraded' else phase,ref,caption],cwd=ROOT,env=env,capture_output=True,text=True,timeout=45)
    require(result.returncode==0,'actual Table browser: '+result.stderr);
    report = json.loads((out/'report.json').read_text())
    if ref: published[ref] = dict(source=report.get('source',''), value=read_published_view(h, 'table', ref, report.get('source','')))
    return report
   h.http('/api/message',dict(source_id='table-full-app',content=[dict(type='text',text='TABLE_GATE')]))
   require(server.ready.wait(20),'real provider did not emit prefix')
   preview=browser('preview');prefix_upper_bound=preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','Table delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');ref,saved,graph=db.execute('SELECT w.ui_ref,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_table_view_snapshot p USING(invocation_id)').fetchone();require(graph==target,'Table accepted under wrong graph');require(db.execute("SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-table%'").fetchone()[0]==0,'view reserved an effect')
   ref+='#1';formal=browser('formal',ref);selected=h.command('plugin','status');h.stop(kill=True);h.start();h.wait_runtime_ready('Table cold recovery ready');require(h.command('plugin','status')==selected,'restart changed selection');restart=browser('restart',ref);require(restart['states']==formal['states'],'cold recovery rewrote original CAS/selection')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT result_json FROM ui_table_view_snapshot').fetchone()[0]==saved,'restart recomputed original view')
   # Existing-valid literal cells must retain their streaming preview after
   # HTML/entity/original-text representation expands private resource bytes.
   normal=server.table
   server.table=dict(title='Literal cells',fallback='Original literal cells',components=[normal['components'][0],dict(name='Table',props=dict(title='Literal cells',columns=['N'],rows=[["'"*2000] for _ in range(30)],source='Ship safe core'))])
   server.ready.clear();server.release.clear();h.http('/api/message',dict(source_id='table-literal-budget',content=[dict(type='text',text='TABLE_GATE_LITERAL')]))
   require(server.ready.wait(20),'literal prefix missing');literal_preview=browser('literal-preview',caption='Literal cells');server.release.set()
   h.wait(lambda state:len(state['deliveries'])==2 and all(row['status']=='sent' for row in state['deliveries']),'literal Table delivered')
   server.table=normal
   # Publish a genuinely different template through the same real Owner path.
   h.stop();new_target,new_digest=prepare(h,binary,Path(temp)/'upgrade','1.28.0');h.start();h.wait_runtime_ready('Table upgrade ready');browser('upgrade-retains-old',ref)
   server.ready.clear();server.release.clear();h.http('/api/message',dict(source_id='table-upgrade',content=[dict(type='text',text='TABLE_GATE_NEW')]))
   require(server.ready.wait(20),'new version did not stream');new_preview=browser('upgrade-preview',caption='Table v124:');new_prefix_upper_bound=new_preview['detectedAt']/1000-server.prefix_sent;server.release.set();h.wait(lambda s:len(s['deliveries'])==3 and all(d['status']=='sent' for d in s['deliveries']),'new Table delivered')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');new_ref,new_graph=db.execute('SELECT w.ui_ref,p.graph_revision FROM ui_widget w JOIN ui_table_view_snapshot p USING(invocation_id) WHERE p.graph_revision=?',(new_target,)).fetchone();require(new_graph==new_target,'new Table changed graph');snapshots=db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_table_view_snapshot ORDER BY invocation_id').fetchall()
   new_ref+='#1';browser('upgraded',new_ref,'Table v124:');h.stop()
   candidate='table-full-app-rollback';h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=new_target,target=target,policy_id=base_policy_id,goal='Retain both original Table versions.')));decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=new_target,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60)
   h.start();h.wait_runtime_ready('Table rollback ready');browser('rollback-old',ref);browser('rollback-new',new_ref,'Table v124:');h.stop()
   h.command('plugin','deactivate',text=json.dumps(dict(operation_id='table-disable',expected_graph_revision=target)));h.start()
   # Historical reads do not depend on current work readiness or a repair select.
   browser('disabled-old',ref);browser('disabled-new',new_ref,'Table v124:');require(h.command('plugin','status')['graph_revision']=='','cold start reactivated plugins')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');require(db.execute('SELECT invocation_id,graph_revision,result_json FROM ui_table_view_snapshot ORDER BY invocation_id').fetchall()==snapshots,'version switch recomputed accepted views')
   h.stop()
   control_env=h.env.copy();control_env.update(STEWARD_TEST_TABLE_CONFIG=str(h.config),STEWARD_TEST_TABLE_REF=ref)
   control=run_process(['go','test','./internal/app','-run','^TestUITableActualForgetRevokesOriginalViewAndExport$','-count=1'],cwd=ROOT,env=control_env,capture_output=True,text=True,timeout=90)
   (evidence.parent/'forget-control.log').write_text(control.stdout+control.stderr)
   require(control.returncode==0,'real App Forget fence failed: '+control.stderr+control.stdout)
   report=dict(checks=['actual App and production bridge','fresh default40 Table1.27 with reviewed Owner cases','incomplete streaming Table from original package','formal original ordinal1 iframe','SIGKILL cold recovery without reselect or view recomputation'],graph=graph,package_digest=digest,ref=ref,preview=preview,formal=formal,restart=restart,snapshot_sha256=hashlib.sha256(saved.encode()).hexdigest(),prefix_to_browser_upper_bound_seconds=prefix_upper_bound,binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),version_upgrade=dict(graph=new_target,package_digest=new_digest,ref=new_ref,preview=new_preview,prefix_to_browser_upper_bound_seconds=new_prefix_upper_bound),version_checks=['old reference immediately after upgrade','real new prefix and template','both references after rollback','both references after deactivation/cold start without reselect','all snapshot bytes unchanged'],literal_preview=literal_preview,default_owner_cases=owner_cases,scope='fresh default40 UI1.27 and independent1.28 template upgrade; remaining renderers and final delivery gates incomplete')
   evidence.write_text(json.dumps(report,indent=2)+'\n');return report
  finally:
   server.release.set();h.cleanup()
   (evidence.parent/'runtime-state.json').write_text(json.dumps(h.failure_state(),indent=2))
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.row_factory=sqlite3.Row;db.execute('BEGIN');diagnostics={table:[dict(r) for r in db.execute('SELECT * FROM '+table)] for table in ['rsi_run','ui_widget','ui_table_view_snapshot']}
   (evidence.parent/'view-state.json').write_text(json.dumps(diagnostics,indent=2))
   server.shutdown();server.server_close();thread.join()

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();report=run_cases([0],lambda _:run(a.binary.resolve(),a.evidence.resolve()),1)[0];print(json.dumps(report))
