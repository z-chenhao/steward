#!/usr/bin/env python3
"""Actual CLI status, trusted credential facts and independent Feishu guidance."""
import argparse
from contextlib import closing
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
from feishu_sandbox import Sandbox
from fixture_pool import run as run_process, run_cases
from verify_channels import ChannelHarness, feishu_topic_delivery, feishu_task_questions, feishu_artifact_reviews
from verify_main import MainHarness, require

ROOT=Path(__file__).resolve().parents[2]
CONTRACT='steward.feishu-status/1'

def select(h,name,baseline,target,policy):
    h.command('plugin','propose',text=json.dumps(dict(candidate_id=name,baseline=baseline,target=target,policy_id=policy,goal='Independent Feishu status guidance')))
    decision=h.command('plugin','verify',name)
    h.command('plugin','activate',text=json.dumps(dict(operation_id=name,expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])))

def delivery_lifecycle(binary,package,root):
    root.mkdir()
    with Sandbox() as platform:
        h=ChannelHarness(binary,root,platform,'tasks')
        def frozen():
            with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
                db.execute('BEGIN')
                return dict(db.execute('SELECT delivery_id,spec_json FROM delivery_projection'))
        def send(source,label,count):
            h.callback(source,'A bounded reply for '+source)
            h.wait(lambda state: len(state['deliveries'])==count and all(d['status']=='sent' for d in state['deliveries']),source+' delivery')
            message=next(m for m in platform.evidence()['messages'] if m.get('reply_to')==source)
            card=json.loads(message['content'])
            require(card['elements'][1]['actions'][0]['text']['content']==label,'actual delivery ignored selected card owner')
        try:
            h.config.write_text(h.config.read_text()+'  workspace_url: https://owner-workspace.example.test\n')
            h.setup()
            # Install through the production owner path before the original
            # delivery, while retaining the deterministic model fixture.
            h.enable_default_plugins()
            h.start();send('card-before' ,'Open full response and files',1);h.stop()
            original=frozen()
            with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
                db.execute('BEGIN')
                baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
                graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0])
                policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
            original_policy=policy['id']
            installed=h.command('plugin','install',str(package))
            next_graph=json.loads(json.dumps(graph))
            next(b for b in next_graph['bindings'] if b['instance_id']=='feishu-chat')['package_digest']=installed['digest']
            target=h.command('plugin','prepare',text=json.dumps(next_graph))['revision']
            policy['id']='feishu-delivery-v2'
            for case in policy['cases']:
                if case['contract']==CONTRACT and case['expected']['action']:
                    case['expected']['action']='v2 '+case['expected']['action']
            h.command('plugin','policy',text=json.dumps(policy));select(h,'card-upgrade',baseline,target,policy['id'])
            h.start();send('card-upgraded','v2 Open full response and files',2);h.stop()
            require(all(frozen().get(k)==v for k,v in original.items()),'upgrade rewrote accepted card bytes')
            select(h,'card-rollback',target,baseline,original_policy)
            h.start();send('card-rollback','Open full response and files',3)
            with platform.lock:platform.drop_response=True
            h.callback('card-uncertain','A bounded uncertain reply')
            h.wait(lambda state:any(d['status']=='unknown' for d in state['deliveries']),'uncertain card')
            h.stop(kill=True)
            attempts=len(platform.evidence()['send_attempts']);accepted=frozen()
            h.command('plugin','deactivate',text=json.dumps(dict(operation_id='card-stop',expected_graph_revision=baseline)))
            with platform.lock:platform.drop_response=False
            h.command('delivery','work')
            require(len(platform.evidence()['send_attempts'])==attempts,'stopped cold recovery replayed unknown external effect')
            require(frozen()==accepted and any(d['status']=='unknown' for d in h.rows()['deliveries']),'stopped cold recovery replaced frozen facts')
            return dict(checks=['actual source-bound HTTP card before/after independent executable upgrade and rollback','prior accepted card bytes retained','unknown card query-only after stop and cold CLI recovery'],messages=len(platform.evidence()['messages']),attempts=attempts)
        finally:
            h.cleanup()

def run(binary,evidence):
    evidence.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='feishu-status-') as tmp,Sandbox() as platform:
        root=Path(tmp)
        disabled_root=root/'disabled';disabled_root.mkdir()
        disabled=MainHarness(binary,disabled_root,fixture='')
        require(disabled.command('channel','status')==dict(channel='feishu',mode='disabled',state='not_configured',action='Configure feishu and bind the exact owner and chat through channel manage.'),'actual disabled guidance changed')
        configured_root=root/'configured';configured_root.mkdir()
        h=ChannelHarness(binary,configured_root,platform,fixture='')
        try:
            original=h.command('channel','status')
            require(original==dict(channel='feishu',mode='external_token',state='unchecked',action='Run channel check to verify credentials without sending a message.'),'actual status ignored owning guidance or altered facts')
            before=h.command('status')
            ready=h.command('channel','check');require(ready==dict(channel='feishu',mode='external_token',state='ready'),'read-only check changed authoritative facts')
            with closing(sqlite3.connect(h.root/'data/state.db')) as db,db:
                db.execute('BEGIN')
                baseline=db.execute('SELECT active_graph FROM rsi_control WHERE singleton=1').fetchone()[0]
                graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(baseline,)).fetchone()[0])
                policy=json.loads(db.execute('SELECT policy_json FROM rsi_policy ORDER BY rowid DESC LIMIT 1').fetchone()[0])
            require(len(graph['bindings'])==40 and len(policy['cases'])==387,'fresh default40/387 composition wrong')
            original_policy=policy['id']
            package=root/'independent';shutil.copytree(binary.parent/'plugins/feishu-chat',package)
            source=ROOT/'internal/plugin/feishuchat/status.go';code=source.read_text()
            require(code.count('Version: "1.0.0"')==1 and code.count('return contract.StatusProjection{Action: action}')==1,'guidance overlay markers missing')
            code=code.replace('Version: "1.0.0"','Version: "1.1.0"',1).replace('return contract.StatusProjection{Action: action}','if action!="" {action="v2 "+action};return contract.StatusProjection{Action: action}',1)
            replacement=root/'status.go';replacement.write_text(code)
            card_source=ROOT/'internal/plugin/feishuchat/card.go'
            card_code=card_source.read_text().replace('Open full response and files','v2 Open full response and files').replace('Feedback saved. Ghost will review it.','v2 Feedback saved. Ghost will review it.')
            card_replacement=root/'card.go';card_replacement.write_text(card_code)
            overlay=root/'overlay.json';overlay.write_text(json.dumps(dict(Replace={str(source):str(replacement),str(card_source):str(card_replacement)})))
            run_process(['go','build','-trimpath','-overlay',str(overlay),'-o',str(package/'plugin'),'./cmd/plugins/feishu-chat'],cwd=ROOT,check=True,capture_output=True,text=True,timeout=90)
            manifest=json.loads((package/'steward.plugin.json').read_text());manifest['version']='1.1.0';(package/'steward.plugin.json').write_text(json.dumps(manifest))
            h.command('plugin','seal',str(package));installed=h.command('plugin','install',str(package))
            next_graph=json.loads(json.dumps(graph))
            next(binding for binding in next_graph['bindings'] if binding['instance_id']=='feishu-chat')['package_digest']=installed['digest']
            target=h.command('plugin','prepare',text=json.dumps(next_graph))['revision']
            policy['id']='feishu-guidance-v2'
            for case in policy['cases']:
                if case['contract']==CONTRACT and case['expected']['action']:
                    case['expected']['action']='v2 '+case['expected']['action']
            h.command('plugin','policy',text=json.dumps(policy));select(h,'feishu-upgrade',baseline,target,policy['id'])
            upgraded=h.command('channel','status')
            require(upgraded=={**original,'action':'v2 '+original['action']},'actual CLI ignored independent executable or changed facts')
            require(h.command('channel','status')==upgraded,'cold read lost owning version')
            with platform.lock:platform.current_token='different-synthetic-token'
            rejected=h.command('channel','check')
            require(rejected==dict(channel='feishu',mode='external_token',state='credentials_rejected',action='v2 Check the configured application credentials or replace the external access token.'),'credential rejection was replaced by a presentation claim')
            select(h,'feishu-rollback',target,baseline,original_policy)
            require(h.command('channel','status')==original,'rollback lost exact original guidance')
            h.command('plugin','deactivate',text=json.dumps(dict(operation_id='feishu-stop',expected_graph_revision=baseline)))
            stopped=h.command('channel','status')
            require(stopped==dict(channel='feishu',mode='external_token',state='unchecked',guidance_unavailable=True),'stop restored native guidance or hid facts')
            require(h.command('channel','status')==stopped,'first cold stopped read changed facts or restored guidance')
            rejected=h.command('channel','check')
            require(rejected==dict(channel='feishu',mode='external_token',state='credentials_rejected',guidance_unavailable=True),'independent credential control was disabled or falsely reported')
            after=h.command('status')
            require(after['config_version']==before['config_version'] and after.get('active_model_profile_id')==before.get('active_model_profile_id'),'guidance mutated model authority')
            require(not platform.send_attempts and not platform.messages and not platform.uploads,'status/check sent messages or uploaded files')
            report=dict(checks=['actual fresh default40/387','actual disabled/unchecked/ready/rejected trusted facts','independent executable guidance and exact rollback','stop/first cold read never restore native guidance','credential control remains available without presentation','zero message/upload effects and unchanged model configuration'],baseline=baseline,upgraded_graph=target,package_digest=installed['digest'],binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),scope='F-020 status and card/feedback presentation; trusted transport and authority retained')
            report['delivery_lifecycle']=delivery_lifecycle(binary,package,root/'delivery')
            business=dict(checks=[],scenarios={})
            for scenario in (feishu_topic_delivery,feishu_task_questions,feishu_artifact_reviews):
                scenario(binary,business,managed=True)
            report['managed_business']=business
            evidence.write_text(json.dumps(report,indent=2)+'\n');return report
        finally:
            h.cleanup();disabled.cleanup()

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--binary',type=Path,required=True);parser.add_argument('--evidence',type=Path,required=True);args=parser.parse_args()
    print(json.dumps(run_cases([0],lambda _:run(args.binary.resolve(),args.evidence.resolve()),1)[0]))
