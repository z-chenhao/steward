#!/usr/bin/env python3
"""Native UI extensions, durable view state and the ordinary tool/action loop."""
import argparse
from contextlib import closing
from http.server import ThreadingHTTPServer
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
from event_broker import Broker
from feishu_sandbox import Sandbox
from provider_endpoint import Endpoint
from verify_channels import ChannelHarness,require

def run(binary,evidence):
 with tempfile.TemporaryDirectory(prefix='steward-ui-interactions-') as temp,Sandbox() as platform,Broker(Path(temp)) as broker:
  server=ThreadingHTTPServer(('127.0.0.1',0),Endpoint);server.requests=[];server.mode='success';thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();h=ChannelHarness(binary,Path(temp),platform,'')
  try:
   h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: '+json.dumps(str(binary))+'\n  provider_bridge_script: -model-bridge\n  provider_bridge_version: steward-go-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
   h.setup(metadata=dict(name='UI interaction peer',provider_kind='openai_compatible',endpoint=f'http://127.0.0.1:{server.server_port}/v1',wire_api='openai_completions',model_id='fixture-model',context_window=32768,max_output_tokens=4096,compatibility=dict(tool_result_format='openai'),capabilities=dict(text_input=True)))
   server.mode='ui-interactions'
   components=[dict(name='Table',props=dict(title='Values',columns=['Category','Value'],rows=[['A','10'],['B','42'],['C','25']],source='User-provided fixture')),dict(name='steward.lineChart',props=dict(title='Trend',description='Illustrative values',data=[dict(label='A',value=10),dict(label='B',value=42),dict(label='C',value=25)])),dict(name='steward.scatterChart',props=dict(title='Relationship',description='Illustrative values',data=[dict(label='A',x=1,y=10),dict(label='B',x=2,y=42)])),dict(name='steward.form',props=dict(title='Trip',fields=[dict(name='date',label='Departure',type='date',required=True),dict(name='count',label='Travelers',type='number',min=1,max=5,required=True),dict(name='mode',label='Travel mode',type='select',required=True,options=[dict(value='train',label='Train'),dict(value='bus',label='Bus')]+[dict(value=f'route_{i:02}',label=f'Route {i:02}') for i in range(3,15)]),dict(name='extras',label='Extras',type='multiselect',options=[dict(value='meal',label='Meal'),dict(value='bag',label='Bag')]),dict(name='notes',label='Notes',type='textarea'),dict(name='confirm',label='Confirmed',type='boolean',required=True)])),dict(name='steward.cards',props=dict(title='Options',items=[dict(title='Train',text='Direct route'),dict(title='Bus',text='Two stops'),dict(title='Walk',text='Local route')])),dict(name='steward.action',props=dict(id='refresh',label='Refresh catalog',tool='builtin__tool_search',arguments=dict(query='task',limit=1),bindings=[dict(argument='query',component=0,path='/query')])),dict(name='generateSandboxedUi',props=dict(initialHeight=160,placeholderMessages=[],css='.wide-diagram{min-width:1400px;display:flex;justify-content:space-between;align-items:center}',html='<label>Quantity<input type="number" id="quantity" value="2"></label><div class="actions"><button id="fetch">Refresh from Ghost</button><button id="modal">Open details</button></div><div class="wide-diagram"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 64" width="240" height="64" onload="window.svgExportProbe=true"><rect x="1" y="1" width="238" height="62" rx="8" fill="none" stroke="currentColor"/><text x="16" y="37" fill="currentColor">Quantity</text><script>window.svgExportScript=true</script></svg><span id="far-edge">Far edge</span></div><div id="nested-scroll" style="width:160px;overflow-x:auto"><div style="width:500px">Wide nested content</div></div><output id="value">4</output>',jsFunctions='function initialize(){document.getElementById("quantity").oninput=e=>{document.getElementById("value").textContent=String(Number(e.target.value)*2)};document.getElementById("fetch").onclick=async()=>{const result=await Websandbox.connection.remote.callTool({action:"refresh"});document.getElementById("value").textContent=result.accepted?"Refreshed":"Unavailable"};document.getElementById("modal").onclick=()=>Websandbox.connection.remote.requestModal()}',jsExpressions=['initialize()']))]
   components.insert(4,dict(name='steward.form',props=dict(title='Feedback',fields=[dict(name='comment',label='Comment',type='text',required=True),dict(name='tags',label='Tags',type='multiselect',options=[dict(value='x',label='X'),dict(value='y',label='Y')])])))
   components.append(dict(name='steward.lineChart',props=dict(title='Shared growth',description='User-provided fixture',unit='万人',labels=['1月','2月','3月'],series=[dict(name='A',values=[12,15,14]),dict(name='B',values=[10,13,17])])))
   components.append(dict(name='barChart',props=dict(title='Large category comparison',description='Illustrative annual USD values',data=[dict(label='研究与创新部门一号' if i==1 else 'Research department '+str(i),value=i*1000000000) for i in range(1,8)])))
   server.widgets=[dict(title='Explorer',fallback='A: 10. B: 42. C: 25.',components=components)]
   h.start();h.wait_state(lambda status:status['readiness']=='ready','UI ready',lambda _:h.http('/api/status'));h.http('/api/message',dict(source_id='ui-extensions',content=[dict(type='text',text='SHOW_UI_INTERACTIONS')]));h.wait(lambda s:len(s['deliveries'])==1 and s['deliveries'][0]['status']=='sent','native UI delivered')
   h.browser(evidence.parent/'browser','ui-interactions')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    require(db.execute('SELECT count(*) FROM ui_action_request').fetchone()[0]==2,'action lost acknowledgement or refresh duplicated an input')
    require(db.execute("SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__tool_search' AND status='succeeded'").fetchone()[0]==2,'UI action did not use actual durable tool invocations')
    require([r[0] for r in db.execute("SELECT json_extract(arguments_json,'$.query') FROM ui_action_request ORDER BY rowid")] == ['task','memory'],'action arguments did not follow the declared current UI binding')
    require(db.execute('SELECT count(*) FROM ui_view_state').fetchone()[0]>=4,'view state was not stored')
    initial=db.execute('SELECT count(*) FROM ui_widget').fetchone()[0]
   h.stop(kill=True);h.start();h.browser(evidence.parent/'restart','ui-interactions-restart')
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:require(db.execute('SELECT count(*) FROM ui_widget').fetchone()[0]==initial,'restart regenerated presentation')
   return dict(checks=['real Main-generated native extensions','sort/filter/chart linking and zoom','typed form validation, drafts, native fallback and accepted replay','CAS persistence and frozen model context','UI actions use existing Main and durable tool ledger with retry identity','local CSV/SVG/PNG exports','fullscreen/modal/PiP with retained frame and keyboard/mobile layouts','SIGKILL/restart restores state without regeneration'])
  finally:
   with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
    db.row_factory=sqlite3.Row
    diagnostic={table:[dict(r) for r in db.execute(query)] for table,query in dict(actions='SELECT * FROM ui_action_request',inputs='SELECT sequence,run_id,message_id,message_json FROM main_input',runs="SELECT run_id,status,error_code,json_extract(checkpoint_json,'$.step') AS step FROM run",generations='SELECT generation_id,run_id,step_index,status,error_code FROM model_generation',tools='SELECT tool_name,arguments_json,status,result_json FROM tool_invocation').items()}
    (evidence.parent/'runtime-state.json').write_text(json.dumps(diagnostic))
   (evidence.parent/'model-requests.json').write_text(json.dumps([dict(body=r['body'],session=r.get('session')) for r in server.requests if r.get('method')=='POST']))
   h.cleanup();server.shutdown();server.server_close();thread.join()
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();a.evidence.parent.mkdir(parents=True,exist_ok=True);report=run(a.binary.resolve(),a.evidence.resolve());a.evidence.write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report))
