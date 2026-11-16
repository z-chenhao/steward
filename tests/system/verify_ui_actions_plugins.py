#!/usr/bin/env python3
"""Actual default App action feedback, unchanged effects and version lifecycle."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading
from fixture_pool import run as process, run_cases
from event_broker import Broker
from feishu_sandbox import Sandbox
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness, require

ROOT = Path(__file__).resolve().parents[2]

def prepare(h, binary, folder):
 folder.mkdir(parents=True); package=folder/'ui-package'; shutil.copytree(binary.parent/'plugins/intelligent-ui',package); replacements={}
 for name in ['plugin.go','action_view.go']:
  original=ROOT/'internal/plugin/intelligentui'/name; code=original.read_text()
  changed=code.replace('Version: "1.27.0"','Version: "1.28.0"',1) if name=='plugin.go' else code.replace('UnknownError: "Result unknown. Check the conversation."','UnknownError: "Action outcome unknown. Inspect its original receipt."',1).replace('SuccessIndicator: true','SuccessIndicator: false',1)
  require(changed!=code,'independent action feedback did not change'); replacement=folder/name;replacement.write_text(changed);replacements[str(original)]=str(replacement)
 overlay=folder/'overlay.json';overlay.write_text(json.dumps(dict(Replace=replacements)))
 process(['go','build','-trimpath','-overlay',str(overlay),'-o',str(package/'plugin'),'./cmd/plugins/intelligent-ui'],cwd=ROOT,check=True,capture_output=True,text=True,timeout=90)
 manifest=json.loads((package/'steward.plugin.json').read_text());manifest['version']='1.28.0';(package/'steward.plugin.json').write_text(json.dumps(manifest));h.command('plugin','seal',str(package));installed=h.command('plugin','install',str(package))
 with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
  db.execute('BEGIN');baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0];graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0]);policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
 next(b for b in graph['bindings'] if b['instance_id']=='intelligent-ui')['package_digest']=installed['digest'];target=h.command('plugin','prepare',text=json.dumps(graph))['revision'];policy['id']='action-feedback-owner-128';changed=0
 for case in policy['cases']:
  if case.get('contract')=='steward.ui-document/1':
   for view in case['expected'].get('action_views',[]):view['unknown_error']='Action outcome unknown. Inspect its original receipt.';view['success_indicator']=False;changed+=1
 require(changed==2,'independent action Owner expectations missing');h.command('plugin','policy',text=json.dumps(policy));candidate='action-feedback-full-app-128';h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=baseline,target=target,policy_id=policy['id'],goal='Verify actual owning action feedback without new execution authority.')));decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60);return target,installed['digest']

def run(binary,evidence):
 evidence.parent.mkdir(parents=True,exist_ok=True)
 with tempfile.TemporaryDirectory(prefix='steward-action-app-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
  peer=ThreadingHTTPServer(('127.0.0.1',0),Endpoint);peer.requests=[];peer.mode='success';thread=threading.Thread(target=peer.serve_forever,daemon=True);thread.start();h=ChannelHarness(binary,Path(temp),platform,'')
  try:
   h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
   h.setup(metadata=dict(name='Action feedback peer',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{peer.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=131072,max_output_tokens=4096,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
   peer.mode='ui-interactions';peer.widgets=[dict(title='Action feedback',fallback='Explicit refresh request',components=[dict(name='Table',props=dict(title='Values',columns=['Name'],rows=[['A']],source='Fixture')),dict(name='steward.action',props=dict(id='refresh',label='Refresh catalog',tool='builtin__tool_search',arguments=dict(query='task',limit=1)))])]
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.execute('BEGIN');target=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0];graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(target,)).fetchone()[0]);require(len(graph['bindings'])==40,'not fresh default40');digest=next(b['package_digest'] for b in graph['bindings'] if b['instance_id']=='intelligent-ui');manifest=json.loads(db.execute('SELECT manifest_json FROM rsi_package WHERE digest=?',(digest,)).fetchone()[0]);require(manifest['version']=='1.27.0' and len(manifest['provides'])==9,'default action feedback version/services wrong');base_policy=db.execute('SELECT policy_id FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0];owner_cases=db.execute("SELECT json_array_length(policy_json,'$.cases') FROM rsi_policy WHERE policy_id=?",(base_policy,)).fetchone()[0];require(owner_cases==387,'default action Owner cases missing')
   def snapshots():
    with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
     db.execute('BEGIN');return db.execute("SELECT w.ui_ref,c.result_json,b.graph_revision,w.message_id FROM ui_widget w JOIN rsi_call c ON c.run_id=w.run_id AND c.operation_id='ui-normalize:'||w.invocation_id JOIN rsi_run b ON b.run_id=w.run_id WHERE w.message_id IS NOT NULL ORDER BY w.rowid").fetchall()
   def effects(count):
    with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
     db.execute('BEGIN');rows=db.execute('SELECT component_ref,source_id,arguments_json FROM ui_action_request ORDER BY rowid').fetchall();require(len(rows)==count,'action read/recovery created a new request');require(all(json.loads(r[2])==dict(query='task',limit=1) for r in rows),'feedback altered original request arguments');require(db.execute("SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__tool_search' AND status='succeeded'").fetchone()[0]==count,'ordinary tool execution missing/duplicated');return rows
   def browser(phase,ref,variant=False):
    out=evidence.parent/phase;env=h.env.copy();chrome=Path('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')
    if chrome.exists():env['STEWARD_BROWSER_EXECUTABLE']=str(chrome)
    result=process(['node',str(ROOT/'tests/system/ui_action_plugins_browser.mjs'),h.url,str(out),phase,ref,str(variant).lower()],cwd=ROOT,env=env,capture_output=True,text=True,timeout=45);require(result.returncode==0,'actual action feedback browser: '+result.stderr);return json.loads((out/'report.json').read_text())
   h.start();h.wait_runtime_ready('action App ready');h.http('/api/message',dict(source_id='action-first',content=[dict(type='text',text='SHOW_UI_INTERACTIONS')]));h.wait(lambda _:len(snapshots())==1,'action delivered');before=snapshots()[0];require(before[2]==target and len(json.loads(before[1])['action_views'])==1,'original feedback missing from real receipt');ref=before[0]+'#1';formal=browser('formal',ref);original_effects=effects(1);selected=h.command('plugin','status');h.stop(kill=True);h.start();restart=browser('restart',ref);require(restart['source']==formal['source'] and effects(1)==original_effects and snapshots()[0]==before,'first cold read changed original request or feedback');require(h.command('plugin','status')==selected,'recovery reselected graph');h.wait_runtime_ready('original action cold runtime ready after first read');h.wait(lambda state:all(r['status'] in ['completed','cancelled','failed'] for r in state['runs']),'original action Main Run finished')
   h.stop();new_target,new_digest=prepare(h,binary,Path(temp)/'upgrade');h.start();h.wait_runtime_ready('action upgrade ready');browser('upgrade-retains-old',ref);effects(1);h.http('/api/message',dict(source_id='action-new',content=[dict(type='text',text='SHOW_UI_INTERACTIONS')]));h.wait(lambda _:len(snapshots())==2,'new action delivered');new=snapshots()[1];require(new[2]==new_target,'new feedback used wrong graph');new_ref=new[0]+'#1';upgraded=browser('upgraded',new_ref,True);effects(2);all_snapshots=snapshots();h.wait(lambda state:all(r['status'] in ['completed','cancelled','failed'] for r in state['runs']),'new action Main Run finished');h.stop()
   candidate='action-feedback-rollback';h.command('plugin','propose',text=json.dumps(dict(candidate_id=candidate,baseline=new_target,target=target,policy_id=base_policy,goal='Retain old/new original action feedback.')));decision=h.command('plugin','verify',candidate,timeout=90);h.command('plugin','activate',text=json.dumps(dict(operation_id=candidate,expected_graph_revision=new_target,target_graph_revision=target,decision_ref=decision['decision_ref'])),timeout=60);h.start();h.wait_runtime_ready('action rollback ready');browser('rollback-old',ref);browser('rollback-new',new_ref,True);effects(2);h.stop()
   h.command('plugin','deactivate',text=json.dumps(dict(operation_id='action-feedback-disable',expected_graph_revision=target)));h.start();browser('disabled-old',ref);browser('disabled-new',new_ref,True);require(h.command('plugin','status')['graph_revision']=='','cold start reactivated action plugin');require(snapshots()==all_snapshots,'lifecycle recomputed original feedback');effects(2);h.stop()
   env=h.env.copy();env.update(STEWARD_TEST_ACTION_CONFIG=str(h.config),STEWARD_TEST_ACTION_MESSAGE=before[3],STEWARD_TEST_ACTION_REF=ref,STEWARD_TEST_ACTION_SOURCE=formal['source']);result=process(['go','test','./internal/app','-run','^TestUIActionActualForgetRevokesOriginalFeedbackAndReceipt$','-count=1'],cwd=ROOT,env=env,capture_output=True,text=True,timeout=90);(evidence.parent/'forget-control.log').write_text(result.stdout+result.stderr);require(result.returncode==0,'actual action Forget failed: '+result.stdout+result.stderr)
   report=dict(scope='actual default40 UI1.27 and independent1.28 feedback/success presentation, original ordinary tool action/source/CAS/receipt, upgrade/rollback/deactivation/cold recovery/Forget; legacy/final gates still pending',owner_cases=owner_cases,ref=ref,new_ref=new_ref,original_graph=target,new_graph=new_target,packages=[digest,new_digest],formal=formal,restart=restart,upgraded=upgraded,original_receipts_unchanged=True,ordinary_tool_invocations=2,binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest());evidence.write_text(json.dumps(report,indent=2)+'\n');return report
  finally:
   h.cleanup();(evidence.parent/'runtime-state.json').write_text(json.dumps(h.failure_state(),indent=2));peer.shutdown();peer.server_close();thread.join()

if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();print(json.dumps(run_cases([0],lambda _:run(a.binary.resolve(),a.evidence.resolve()),1)[0]))
