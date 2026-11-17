#!/usr/bin/env python3
"""Actual standard30 native/CodeMode discovery and optional original23 upgrade.

The stock-CLI discovery paths use a synthetic HTTP model peer with the real
bridge, immutable graph, sealed ranker, input stream and accepted reply. The
optional archived Root/Child crash path explicitly uses the maintained scripted
children bridge; its graph, packages, ledger, SIGKILL and recovery are real.
"""
import argparse
from contextlib import closing, contextmanager
import hashlib
from http.server import ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading

from provider_endpoint import Endpoint
from verify_capabilities import CapabilityHarness
from fixture_pool import current as current_process_scope, run_cases
from verify_main import require

ROOT = Path(__file__).resolve().parents[2]
CONTRACT = 'steward.tool-discovery/1'
TARGET = 'builtin__web_search'

class DiscoveryHarness(CapabilityHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'), *args]

@contextmanager
def model_peer():
    server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
    server.requests, server.mode = [], 'tool-discovery'
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}/v1', server.requests
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        require(not thread.is_alive(), 'model peer did not join')

def config(root):
    node = shutil.which('node')
    require(node, 'pinned Node is required')
    (root / 'config.yaml').write_text(json.dumps(dict(version=1, llm=dict(
        provider_bridge_command=node, provider_bridge_script=str(ROOT/'provider-bridge/bridge.mjs'),
        provider_bridge_version='pi-ai-1.3.0-v1'), javascript=dict(command=node,
        script=str(ROOT/'runtime/javascript/src/sidecar.mjs')))))

def setup(h, endpoint):
    h.setup(metadata=dict(name='Discovery default workload', provider_kind='openai_compatible', endpoint=endpoint,
        wire_api='openai_completions', model_id='fixture-model', context_window=65536,
        max_output_tokens=4096, compatibility=dict(tool_result_format='openai'),
        capabilities=dict(text_input=True, tool_calls=True, streaming=True)))
    h.command('chat', 'work')
    return h.command('plugin', 'status')

def packages(h, selected):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        db.execute('BEGIN')
        return {row[0] for row in db.execute("SELECT p.plugin_id FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b "
            "JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest') WHERE g.revision=?", (selected['graph_revision'],))}

def owner_state(h, selected):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        db.execute('BEGIN')
        graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',
            (selected['graph_revision'],)).fetchone()[0])
        policy = json.loads(db.execute('SELECT policy_json FROM rsi_policy WHERE policy_id=('
            'SELECT policy_id FROM rsi_candidate WHERE target=? ORDER BY rowid LIMIT 1)',
            (selected['graph_revision'],)).fetchone()[0])
        return graph, policy

def activate_owner(h, graph, policy, baseline, operation):
    target = h.command('plugin','prepare',text=json.dumps(graph))['revision']
    policy = dict(policy, id=operation)
    h.command('plugin','policy',text=json.dumps(policy))
    h.command('plugin','propose',text=json.dumps(dict(candidate_id=operation, baseline=baseline,
        target=target,policy_id=operation,goal='Verify a narrow owner-selected discovery change.')))
    decision = h.command('plugin','verify',operation)
    h.command('plugin','activate',text=json.dumps(dict(operation_id=operation,
        expected_graph_revision=baseline,target_graph_revision=target,decision_ref=decision['decision_ref'])))
    return h.command('plugin','status')

def check_disabled(h, receipt, graph):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        db.execute('BEGIN')
        row=db.execute("SELECT status,result_json,attempt_count FROM tool_invocation WHERE run_id=? AND tool_name='builtin__tool_search'",
            (receipt['run_id'],)).fetchone()
        require(row and row[0]=='failed' and row[2]==1, 'disabled discovery did not settle once')
        require(json.loads(row[1]).get('error',{}).get('code')=='PROVIDER_UNAVAILABLE',
            'post-opt-in removal restored native ranking')
        binding=db.execute('SELECT graph_revision,managed_services_json FROM rsi_run WHERE run_id=?',
            (receipt['run_id'],)).fetchone()
        require(binding[0]==graph and CONTRACT in json.loads(binding[1]), 'removal erased migration boundary')
        return dict(disabled=True,graph=graph,no_native_fallback=True,attempts=row[2])

def check(h, receipt, kind, graph, managed):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        db.execute('BEGIN')
        name = 'builtin__tool_search' if kind == 'native' else 'builtin__codemode_execute'
        row = db.execute('SELECT status,result_json,attempt_count FROM tool_invocation WHERE run_id=? AND tool_name=?',
                         (receipt['run_id'], name)).fetchone()
        require(row and row[0] == 'succeeded' and row[2] == 1, 'actual discovery failed/replayed')
        result = json.loads(row[1])
        require(not result.get('error'), 'actual discovery returned error')
        payload = json.loads(result['text'])
        found = payload['tools'] if kind == 'native' else payload[-1]['value']
        require(len(found) == 1 and found[0]['name'] == TARGET, 'ranker changed original authorized identity')
        binding = db.execute('SELECT graph_revision,managed_services_json FROM rsi_run WHERE run_id=?',
                             (receipt['run_id'],)).fetchone()
        require(binding[0] == graph and (CONTRACT in json.loads(binding[1])) == managed, 'Run changed frozen graph/services')
        cached = db.execute('SELECT count(*) FROM code_search_reply c JOIN code_manifest m USING(execution_id) '
            'JOIN tool_invocation i ON i.invocation_id=m.execution_id WHERE i.run_id=?', (receipt['run_id'],)).fetchone()[0]
        require(cached == (1 if managed and kind == 'code' else 0), 'CodeMode did not freeze actual selected reply')
        require(db.execute('SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=?',
            (receipt['run_id'], TARGET)).fetchone()[0] == 0, 'metadata discovery executed the candidate tool')
        return dict(kind=kind, graph=graph, managed=managed, result=found, attempts=row[2], cached=cached)

def install_complete_bundle(h, binary, graph):
    # This is an owner-requested update. Every current package is installed;
    # unchanged old digests are not substituted to hide a version conflict.
    target=json.loads(json.dumps(graph))
    for binding in target['bindings']:
        installed=h.command('plugin','install',str(binary.parent/'plugins'/binding['instance_id']))
        require(installed['digest']==binding['package_digest'],'full bundle substituted package bytes')
    for binding in target['bindings']:
        if binding['instance_id']=='web-extract':
            binding['config']=h.command('plugin','parser-resource',str(binary.parent/'plugins/web-extract/extract.mjs'))
    return target

def prove_full_bundle_upgrade(binary, prior, endpoint, current_graph, current_policy, prior_count=23):
    require(prior_count in (23,24,28),"unsupported archived baseline")
    with tempfile.TemporaryDirectory(prefix=f'steward-original{prior_count}-full40-') as tmp:
        root=Path(tmp).resolve();config(root);h=DiscoveryHarness(prior,root,fixture='');h.env.pop('COMPOSIO_API_KEY',None)
        try:
            original=setup(h,endpoint);old_graph,old_policy=owner_state(h,original)
            require(len(packages(h,original))==prior_count,'full upgrade did not start from required archive')
            old_discovery=prior_count>=24
            require(('steward.tool-discovery' in packages(h,original))==old_discovery,'archived discovery boundary differs')
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
                db.execute('BEGIN')
                old_packages={r[0]:(r[1],r[2]) for r in db.execute('SELECT plugin_id,version,digest FROM rsi_package')}
            queued=h.send('old23-queued-before-full-upgrade','DISCOVERY_CODE full upgrade retained')
            h.binary=binary
            require(h.command('plugin','status')==original,'new binary rewrote original23 before owner activation')
            target_graph=install_complete_bundle(h,binary,current_graph)
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
                db.execute('BEGIN')
                for binding in target_graph['bindings']:
                    pid,version=db.execute('SELECT plugin_id,version FROM rsi_package WHERE digest=?',
                        (binding['package_digest'],)).fetchone()
                    if pid in old_packages:
                        original_version,original_digest=old_packages[pid]
                        if binding['package_digest']==original_digest:
                            require(version==original_version,'unchanged package changed its version identity')
                        else:
                            require(version!=original_version,'changed old package reused published version')
                        if prior_count>=24:
                            require(binding['package_digest']==original_digest,'new Apps recipe changed original24 content')
                        require(db.execute('SELECT digest FROM rsi_package WHERE plugin_id=? AND version=?',
                            (pid,old_packages[pid][0])).fetchone()[0]==old_packages[pid][1],
                            'full update replaced original package identity')
            upgraded=activate_owner(h,target_graph,current_policy,original['graph_revision'],'full40-owner-upgrade')
            require(len(packages(h,upgraded))==40,'full update did not activate complete40')
            h.command('chat','work')
            checks=[check(h,queued,'code',original['graph_revision'],old_discovery)]
            for kind in ('native','code'):
                receipt=h.send('full40-new-'+kind,'DISCOVERY_'+kind.upper()+' full current bundle')
                h.command('chat','work');checks.append(check(h,receipt,kind,upgraded['graph_revision'],True))
            retained=h.send('full40-queued-before-rollback','DISCOVERY_CODE full rollback retained')
            rolled=activate_owner(h,old_graph,old_policy,upgraded['graph_revision'],'full40-owner-rollback')
            require(rolled['graph_revision']==original['graph_revision'],'full rollback changed original graph')
            h.command('chat','work');checks.append(check(h,retained,'code',upgraded['graph_revision'],True))
            disabled=h.send('full40-rollback-discovery',
                'DISCOVERY_NATIVE retained prior24' if old_discovery else 'DISCOVERY_NATIVE removed')
            h.command('chat','work')
            checks.append(check(h,disabled,'native',rolled['graph_revision'],True) if old_discovery else
                check_disabled(h,disabled,rolled['graph_revision']))
            require(h.command('plugin','status')==rolled,'cold command repaired rolled-back graph')
            result=dict(checks=checks,original_graph=original['graph_revision'],upgraded_graph=upgraded['graph_revision'],
                rollback_graph=rolled['graph_revision'],all_current_packages_installed=True,
                original_package_versions_retained=len(old_packages),cleanup=True,
                original_package_count=prior_count,
                scope=f'actual archived{prior_count} binary and sealed packages -> allcurrent30;normal stockCLI/provider bridge/CodeMode;synthetic HTTP model')
            return result,old_graph,old_policy
        finally:
            h.cleanup()

class ArchivedTreeHarness(CapabilityHarness):
    def args(self,*args):
        return [str(self.binary),'-config',str(self.root/'config.yaml'),'-test-bridge','children',*args]

def tree_witness(h):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        db.row_factory=sqlite3.Row;db.execute('BEGIN')
        workers={r['run_id']:dict(r) for r in db.execute(
            'SELECT r.run_id,r.worker_position,r.parent_run_id,r.root_run_id,r.task_id,r.task_epoch,r.depth,'
            'r.capability_snapshot_id,r.model_snapshot_id,c.tools_json,c.tool_specs_hash,c.skill_set_hash,c.data_scope,'
            'c.can_spawn_agents,c.format_version,p.policy_json,b.graph_revision,b.managed_services_json FROM run r '
            'JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id '
            'JOIN run_context_policy p ON p.run_id=r.run_id JOIN rsi_run b ON b.run_id=r.run_id '
            "WHERE r.kind='worker' ORDER BY r.depth,r.created_at")}
        generations={r['generation_id']:dict(r) for r in db.execute(
            "SELECT generation_id,run_id,request_hash,request_json,result_json,attempt_count FROM model_generation WHERE status='completed'")}
        return dict(workers=workers,generations=generations)

def prove_archived_tree_upgrade(binary,prior,current_graph,current_policy,archived_graph,archived_policy,prior_count=23):
    require(prior_count in (23,24,28),"unsupported archived tree baseline")
    managed=prior_count>=24
    # Existing opt-in durability hook and scripted model only. Admission, old
    # package bytes, actual CLI/recovery and new child creation are production.
    with tempfile.TemporaryDirectory(prefix=f'steward-original{prior_count}-tree-') as tmp:
        root=Path(tmp).resolve();config(root);h=ArchivedTreeHarness(prior,root,fixture='children');h.env.pop('COMPOSIO_API_KEY',None)
        try:
            h.setup()
            # The existing scripted-model mode deliberately skips automatic
            # distribution selection. Assemble the archived composition once,
            # before admitting any user work, via its real owner control path.
            old_target=install_complete_bundle(h,prior,archived_graph)
            original=activate_owner(h,old_target,archived_policy,'',f'archived{prior_count}-tree-initial-owner')
            require(len(packages(h,original))==prior_count,'archived tree did not select required complete archive')
            h.send('original23-tree','Delegate: Child tree')
            held=h.hold('after_child_accept_commit',timeout=30)
            held.kill();held.wait(timeout=10)
            require(held.returncode==-9,'original tree did not stop at real committed-child boundary')
            scope=current_process_scope()
            require(scope is not None,'tree proof requires owned process groups')
            scope.retire(held);held.communicate(timeout=10)
            before=tree_witness(h)
            old_children=[r for r in before['workers'].values() if r['worker_position']=='child']
            require(len(before['workers'])==2 and len(old_children)==1 and old_children[0]['depth']==1,
                'old binary did not stop with exactly Root and Child before Grandchild admission')
            require(bool(before['generations']),'old crash boundary retained no completed generation')
            require(all(r['graph_revision']==original['graph_revision'] and (CONTRACT in
                json.loads(r['managed_services_json']))==managed for r in before['workers'].values()),
                'original tree was not frozen to actual prior23')
            h.binary=binary
            # Migration/read is the first new command. No source/policy repair.
            require(h.command('plugin','status')==original,'new host replaced original tree selection')
            require(tree_witness(h)==before,'forward migration rewrote original tree or completed generation')
            graph=install_complete_bundle(h,binary,current_graph)
            selected=activate_owner(h,graph,current_policy,original['graph_revision'],'archived-tree-full40')
            h.command('chat','work')
            after=tree_witness(h)
            require(all(after['workers'].get(k)==v for k,v in before['workers'].items()),
                'recovery changed old Worker capability/model/context policy/graph')
            require(all(after['generations'].get(k)==v for k,v in before['generations'].items()),
                'recovery replayed completed original generation')
            workers=list(after['workers'].values());children=[r for r in workers if r['worker_position']=='child']
            require(len(workers)==3 and len(children)==2 and sorted(r['depth'] for r in children)==[1,2],
                'actual original tree did not finish Child and Grandchild')
            require(all(r['graph_revision']==original['graph_revision'] and (CONTRACT in
                json.loads(r['managed_services_json']))==managed for r in workers),'newly admitted descendant changed Root version')
            created=[r for r in children if r['run_id'] not in before['workers']]
            require(len(created)==1 and created[0]['depth']==2,'upgrade created no actual new Grandchild')
            by_id=after['workers']
            for child in children:
                parent=by_id.get(child['parent_run_id'])
                require(parent is not None and child['depth']==parent['depth']+1,
                    'completed tree lost actual parent ancestry')
                require(all(child[key]==parent[key] for key in (
                    'task_id','task_epoch','model_snapshot_id','tools_json','data_scope','policy_json',
                    'graph_revision','managed_services_json')),'descendant changed frozen parent boundary')
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
                db.execute('BEGIN')
                require(db.execute("SELECT count(*) FROM run WHERE kind='worker' AND status='completed'").fetchone()[0]==3,
                    'original retained workers did not actually complete')
                require(db.execute('SELECT count(*) FROM agent_invocation').fetchone()[0]==2,
                    'recovery created duplicate child invocation')
            h.command('chat','work');require(tree_witness(h)==after,'cold drain changed completed old tree')
            require(h.command('plugin','status')==selected,'cold drain repaired current29 selection')
            return dict(original_workers=list(before['workers']),completed_workers=[r['run_id'] for r in workers],
                original_children=len(old_children),completed_children=2,
                new_descendants_after_upgrade=2-len(old_children),original_graph=original['graph_revision'],
                current_graph=selected['graph_revision'],completed_generations_preserved=len(before['generations']),
                actual_archive=True,original_package_count=prior_count,retained_discovery=managed,model='explicit existing children scripted bridge',initial_selection=f'explicit archived{prior_count} owner assembly before user admission',
                boundary='after_child_accept_commit',crash_exit=-9,cold_no_replay=True,cleanup=True,
                scope=f'actual archived{prior_count} queued tree -> full current30 update -> immediate real recovery;no actual old-tree ranked query claim')
        finally:
            h.cleanup()

def run(binary, evidence, prior=None, prior24=None, prior28=None):
    original = hashlib.sha256(binary.read_bytes()).hexdigest()
    report = dict(checks=[], binary_sha256=original, actual_prior23='not_run', actual_prior23_full_bundle='not_run', actual_prior23_tree='not_run', actual_prior24_full_bundle='not_run', actual_prior24_tree='not_run', actual_prior28_full_bundle='not_run', actual_prior28_tree='not_run', cleanup=False)
    with model_peer() as (endpoint, requests):
        with tempfile.TemporaryDirectory(prefix='steward-default40-discovery-') as tmp:
            root=Path(tmp).resolve();config(root);h=DiscoveryHarness(binary, root, fixture='');h.env.pop('COMPOSIO_API_KEY',None)
            try:
                selected=setup(h, endpoint);pkgs=packages(h,selected)
                require(len(pkgs)==40 and 'steward.tool-discovery' in pkgs and selected['epoch']==1 and not selected['stopped'],
                        'stock CLI did not independently verify/select complete40')
                report['default_packages']=sorted(pkgs)
                current_graph,current_policy=owner_state(h,selected)
                for kind in ('native','code'):
                    receipt=h.send('default40-'+kind, 'DISCOVERY_'+kind.upper()+' current')
                    h.command('chat','work')
                    report['checks'].append(check(h,receipt,kind,selected['graph_revision'],True))
                # Every command is a fresh actual process. No select/update/reload repairs.
                require(h.command('plugin','status')==selected, 'cold process replaced original29 selection')
                receipt=h.send('default40-code-cold','DISCOVERY_CODE cold')
                h.command('chat','work');report['checks'].append(check(h,receipt,'code',selected['graph_revision'],True))
            finally:
                h.cleanup()
        if prior is not None:
            prior_hash=hashlib.sha256(prior.read_bytes()).hexdigest()
            with tempfile.TemporaryDirectory(prefix='steward-original23-discovery-') as tmp:
                root=Path(tmp).resolve();config(root);old=DiscoveryHarness(prior,root,fixture='');old.env.pop('COMPOSIO_API_KEY',None)
                try:
                    selected=setup(old,endpoint);pkgs=packages(old,selected)
                    require(len(pkgs)==23 and 'steward.tool-discovery' not in pkgs, 'prior input is not actual23')
                    # The old binary admits and freezes this queued input before upgrade.
                    queued=old.send('original23-before-upgrade','DISCOVERY_CODE retained old')
                    old.binary=binary
                    require(old.command('plugin','status')==selected, 'new binary silently replaced selected prior23')
                    old.command('chat','work')
                    report['checks'].append(check(old,queued,'code',selected['graph_revision'],False))
                    current=old.send('original23-after-upgrade','DISCOVERY_NATIVE selected prior')
                    old.command('chat','work')
                    report['checks'].append(check(old,current,'native',selected['graph_revision'],False))
                    require(old.command('plugin','status')==selected, 'new work rewrote old operator selection')
                    original_graph,original_policy=owner_state(old,selected)
                    retained=old.send('original23-queued-at-opt-in','DISCOVERY_CODE retained across activation')
                    # Add only the new ranker. Every original23 package digest,
                    # config and Host resource stays byte-for-byte unchanged.
                    rank_binding=next(b for b in current_graph['bindings'] if b['instance_id']=='tool-discovery')
                    installed=old.command('plugin','install',str(binary.parent/'plugins/tool-discovery'))
                    require(installed['digest']==rank_binding['package_digest'], 'ranker package changed')
                    upgraded_graph=dict(original_graph,bindings=original_graph['bindings']+[rank_binding])
                    upgraded_policy=dict(original_policy,
                        cases=original_policy['cases']+[c for c in current_policy['cases'] if c['instance_id']=='tool-discovery'],
                        allowed_contracts=original_policy['allowed_contracts']+[CONTRACT])
                    upgraded=activate_owner(old,upgraded_graph,upgraded_policy,selected['graph_revision'],'discovery-owner-opt-in')
                    require(len(packages(old,upgraded))==24,'owner upgrade did not preserve/add exactly one package')
                    old.command('chat','work')
                    report['checks'].append(check(old,retained,'code',selected['graph_revision'],False))
                    for kind in ('native','code'):
                        receipt=old.send('original23-opted-in-'+kind,'DISCOVERY_'+kind.upper()+' owner upgrade')
                        old.command('chat','work')
                        report['checks'].append(check(old,receipt,kind,upgraded['graph_revision'],True))
                    retained_managed=old.send('managed24-queued-at-removal','DISCOVERY_CODE retained managed')
                    rolled=activate_owner(old,original_graph,original_policy,upgraded['graph_revision'],'discovery-owner-remove')
                    require(rolled['graph_revision']==selected['graph_revision'],'removal changed original23 graph')
                    old.command('chat','work')
                    report['checks'].append(check(old,retained_managed,'code',upgraded['graph_revision'],True))
                    disabled=old.send('discovery-removed-no-fallback','DISCOVERY_NATIVE removed')
                    old.command('chat','work')
                    report['checks'].append(check_disabled(old,disabled,rolled['graph_revision']))
                    require(old.command('plugin','status')==rolled,'cold process restored removed ranker')
                    report['actual_prior23']=dict(binary=str(prior),sha256=prior_hash,queued_run=queued['run_id'],
                        original_graph=selected['graph_revision'],owner_upgrade=upgraded['graph_revision'],
                        removal=rolled['graph_revision'],only_new_package_changed=True)
                finally:
                    old.cleanup()
            report['actual_prior23_full_bundle'],_,_=prove_full_bundle_upgrade(binary,prior,endpoint,current_graph,current_policy)
            report['actual_prior23_tree']=run_cases([None],lambda _: prove_archived_tree_upgrade(binary,prior,current_graph,current_policy,original_graph,original_policy),jobs=1)[0]
            require(hashlib.sha256(prior.read_bytes()).hexdigest()==prior_hash, 'prior binary changed')
        if prior24 is not None:
            prior24_hash=hashlib.sha256(prior24.read_bytes()).hexdigest()
            report['actual_prior24_full_bundle'],archived_graph,archived_policy=prove_full_bundle_upgrade(
                binary,prior24,endpoint,current_graph,current_policy,prior_count=24)
            report['actual_prior24_tree']=run_cases([None],lambda _: prove_archived_tree_upgrade(
                binary,prior24,current_graph,current_policy,archived_graph,archived_policy,prior_count=24),jobs=1)[0]
            require(hashlib.sha256(prior24.read_bytes()).hexdigest()==prior24_hash,'archived24 binary changed')
            report['actual_prior24_full_bundle'].update(binary=str(prior24),sha256=prior24_hash)
        if prior28 is not None:
            prior28_hash=hashlib.sha256(prior28.read_bytes()).hexdigest()
            report['actual_prior28_full_bundle'],archived_graph,archived_policy=prove_full_bundle_upgrade(
                binary,prior28,endpoint,current_graph,current_policy,prior_count=28)
            report['actual_prior28_tree']=run_cases([None],lambda _: prove_archived_tree_upgrade(
                binary,prior28,current_graph,current_policy,archived_graph,archived_policy,prior_count=28),jobs=1)[0]
            require(hashlib.sha256(prior28.read_bytes()).hexdigest()==prior28_hash,'original28 binary changed')
            report['actual_prior28_full_bundle'].update(binary=str(prior28),sha256=prior28_hash)
        report['model_requests']=len(requests)
    require(hashlib.sha256(binary.read_bytes()).hexdigest()==original, 'current binary changed during proof')
    report['cleanup']=True
    return report

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--binary',type=Path,required=True);p.add_argument('--evidence',type=Path,required=True)
    p.add_argument('--prior-binary',type=Path);p.add_argument('--prior24-binary',type=Path);p.add_argument('--prior28-binary',type=Path)
    a=p.parse_args();a.evidence.parent.mkdir(parents=True,exist_ok=True)
    report=run(a.binary.resolve(),a.evidence,a.prior_binary.resolve() if a.prior_binary else None,
        a.prior24_binary.resolve() if a.prior24_binary else None,a.prior28_binary.resolve() if a.prior28_binary else None)
    a.evidence.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(dict(checks=len(report['checks']),default_packages=len(report['default_packages']),actual_prior23=report['actual_prior23']!='not_run',
        full_bundle_checks=len(report['actual_prior23_full_bundle']['checks']) if isinstance(report['actual_prior23_full_bundle'],dict) else 'not_run',
        archived_tree_children=report['actual_prior23_tree']['completed_children'] if isinstance(report['actual_prior23_tree'],dict) else 'not_run',
        actual_prior24=report['actual_prior24_full_bundle']!='not_run',cleanup=report['cleanup'])))
