#!/usr/bin/env python3
"""Default bundle -> real model bridge/CodeMode -> sandboxed stdio MCP peer.

Cold processes retain the selected graph, exact approval and unknown-effect
receipt. An explicitly selected pre-MCP composition keeps its original path.
No remote account, live service or fixture-side reselect after recovery.
"""
import argparse
from contextlib import closing, contextmanager
from http.server import ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading

from provider_endpoint import Endpoint
from verify_capabilities import CapabilityHarness
from verify_main import require
import merchant_trial


class MCPHarness(CapabilityHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'), *args]


@contextmanager
def model_peer():
    server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
    server.requests, server.mode = [], 'mcp-plugins'
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}/v1', server.requests
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        require(not thread.is_alive(), 'model peer did not join')


@contextmanager
def state(h):
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
        db.execute('BEGIN')
        yield db


def composition(h):
    selected = h.command('plugin', 'status')
    with state(h) as db:
        graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',
                                      (selected['graph_revision'],)).fetchone()[0])
        packages = {row[0] for row in db.execute(
            "SELECT p.plugin_id FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b "
            "JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest') WHERE g.revision=?",
            (selected['graph_revision'],))}
        policy = json.loads(db.execute('SELECT policy_json FROM rsi_policy WHERE policy_id=('
            'SELECT policy_id FROM rsi_candidate WHERE target=?)', (selected['graph_revision'],)).fetchone()[0])
    return selected, graph, packages, policy


def read(h, key, marker, managed=True, unavailable=False):
    receipt = h.send(key, 'MCP_PLUGIN_READ ' + key)
    h.command('chat', 'work')
    with state(h) as db:
        raw = db.execute("SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='mcp__fixture__read'",
                         (receipt['run_id'],)).fetchone()
        require(raw, 'Main did not execute the actual MCP Tool')
        result = json.loads(raw[0])
        if unavailable:
            require(result.get('error', {}).get('code') == 'PROVIDER_UNAVAILABLE',
                    'deactivated method restored native execution: ' + raw[0])
        else:
            require(not result.get('error') and result.get('text') == marker,
                    'original MCP text blocks were lost or replaced: ' + raw[0])
        services = db.execute('SELECT managed_services_json FROM rsi_run WHERE run_id=?',
                              (receipt['run_id'],)).fetchone()[0]
        require(all((name in services) == managed for name in
                    ('steward.mcp-method/1', 'steward.mcp-namespace/1')),
                'Run did not retain the actual MCP migration boundary')
        tools = json.loads(db.execute('SELECT c.tools_json FROM run r JOIN capability_snapshot c '
            'ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?', (receipt['run_id'],)).fetchone()[0])
        mcp = [t for t in tools if t['name'].startswith('mcp__fixture__')]
        require(len(mcp) == 2 and all(t.get('namespace_description', '') ==
                ('' if unavailable else 'Original MCP default namespace') for t in mcp),
                'new snapshot lost its selected namespace policy')
        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='mcp-bridge'").fetchone()[0] == 0,
                'pure MCP methods created a second effect ledger')
    return receipt['run_id']


def select_composition_without_mcp(h, binary, graph, policy, baseline=""):

    # Real owner verification/activation; the same operation can retain an
    # installation's pre-MCP choice or remove only MCP from an active graph.
    # No fixture writes to control tables.
    omitted = {'mcp-bridge'} | ({'connected-apps'} if not baseline else set())
    omitted_contracts = {'steward.mcp-method/1', 'steward.mcp-namespace/1'}
    if not baseline:
        omitted_contracts.update(('steward.apps-method/1', 'steward.apps-guidance/1', 'steward.apps-mail-review/1'))
    prior = dict(graph, bindings=[b for b in graph['bindings'] if b['instance_id'] not in omitted])
    if not baseline:
        # This is initial owner assembly in a separate data root, before any
        # selection or dependent recovery action. Package installation alone
        # cannot recreate another installation's immutable Host resources.
        captured = h.command('plugin', 'parser-resource', str(binary.parent / 'plugins/web-extract/extract.mjs'))
        original = next(b['config'] for b in prior['bindings'] if b['instance_id'] == 'web-extract')
        require(captured == original, 'initial prior composition did not capture its exact owner parser resource')
    for binding in prior['bindings']:
        installed = h.command('plugin', 'install', str(binary.parent / 'plugins' / binding['instance_id']))
        require(installed['digest'] == binding['package_digest'], 'prior package identity changed')
    target = h.command('plugin', 'prepare', text=json.dumps(prior))['revision']
    owner = dict(policy, id='mcp-prior-owner-policy', cases=[c for c in policy['cases']
                 if c['instance_id'] not in omitted], allowed_contracts=[c for c in
                 policy['allowed_contracts'] if c not in omitted_contracts])
    h.command('plugin', 'policy', text=json.dumps(owner))
    candidate = 'mcp-prior-owner-composition'
    h.command('plugin', 'propose', text=json.dumps(dict(candidate_id=candidate, baseline=baseline, target=target,
        policy_id=owner['id'], goal='Retain an explicit pre-MCP owner composition.')))
    decision = h.command('plugin', 'verify', candidate)
    h.command('plugin', 'activate', text=json.dumps(dict(operation_id=candidate,
        expected_graph_revision=baseline, target_graph_revision=target, decision_ref=decision['decision_ref'])))
    return h.command('plugin', 'status')


def run(binary):
    report = dict(checks=[], cleanup=False, live_service='not_run')
    with tempfile.TemporaryDirectory(prefix='steward-mcp-default-') as tmp, model_peer() as (endpoint, requests):
        root = Path(tmp).resolve()
        peer = root / 'peer'
        peer.mkdir()
        node = shutil.which('node')
        require(node, 'doctor: pinned Node is required for the actual model bridge and CodeMode')
        repo = Path(__file__).resolve().parents[2]
        config = {'version': 1, 'mcp': [dict(name='fixture', command=str(binary), directory=str(peer),
            args=['-test-mcp-server', str(peer)], tools=dict(read='read_only', send='external_effect'),
            description='  Original MCP default namespace\nSECOND_LINE_MUST_NOT_ENTER_MODEL')],
            'llm': dict(provider_bridge_command=node, provider_bridge_script=str(repo / 'provider-bridge/bridge.mjs'),
                        provider_bridge_version='pi-ai-1.3.0-v1'),
            'javascript': dict(command=node, script=str(repo / 'runtime/javascript/src/sidecar.mjs'))}
        config['mcp'].append(merchant_trial.configure(root, repo))
        metadata = dict(name='MCP workload', provider_kind='openai_compatible', endpoint=endpoint,
                        wire_api='openai_completions', model_id='fixture-model', context_window=65536,
                        max_output_tokens=4096, compatibility=dict(tool_result_format='openai'),
                        capabilities=dict(text_input=True, tool_calls=True, streaming=True))
        (root / 'config.yaml').write_text(json.dumps(config))
        h = MCPHarness(binary, root, fixture='')
        reply = peer / 'mcp-result.json'
        effects = peer / 'mcp-effects.jsonl'
        reply.write_text(json.dumps(dict(content=[dict(type='text', text='ORIGINAL_PEER_'), dict(type='text', text='FIRST')])))
        try:
            h.setup(metadata=metadata)
            h.command('chat', 'work')
            selected, graph, packages, policy = composition(h)
            require(selected['epoch'] == 1 and not selected['stopped'] and len(packages) == 41 and
                    {'steward.mcp-bridge', 'steward.apps-directory', 'steward.apps-accounts',
                     'steward.apps-logos', 'steward.apps-interface', 'steward.github-pr-followup','steward.artifact-workbench'}.issubset(packages),
                    'complete default MCP composition was not verified/selected')
            report['default_packages'] = sorted(packages)
            merchant_trial.verify(h, state, root)
            report['checks'].append(dict(merchant_ceiling_and_query_only_recovery=True))
            report['checks'].append(dict(first_run=read(h, 'mcp-default-first', 'ORIGINAL_PEER_FIRST')))
            # The first dependent action after a new process must use the old
            # graph, with no select/update/reload fixture repair.
            reply.write_text(json.dumps(dict(content=[dict(type='text', text='ORIGINAL_PEER_'), dict(type='text', text='COLD')])))
            report['checks'].append(dict(cold_run=read(h, 'mcp-default-cold', 'ORIGINAL_PEER_COLD')))
            require(h.command('plugin', 'status') == selected, 'cold process replaced the selected graph')

            receipt = h.send('mcp-default-effect', 'MCP_PLUGIN_EFFECT exact-default-effect')
            h.command('chat', 'work')
            pending = [a for a in h.command('approval', 'list') if a['status'] == 'pending']
            require(len(pending) == 1 and pending[0]['tool'] == 'mcp__fixture__send' and not effects.exists(),
                    'MCP dispatched before its exact ordinary Tool approval')
            approval = pending[0]
            h.command('approval', 'approve', approval['approval_id'], approval['frozen_arguments_hash'])
            h.command('chat', 'work')
            with state(h) as db:
                require(db.execute("SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name='mcp__fixture__send' "
                    "AND status='unknown' AND replay_policy='never' AND attempt_count=1", (receipt['run_id'],)).fetchone()[0] == 1,
                    'original unknown effect did not retain its never receipt')
            before = effects.read_bytes()
            require(before.count(b'exact-default-effect') == 1, 'approved effect was not dispatched exactly once')
            h.command('chat', 'work')
            require(effects.read_bytes() == before and h.command('plugin', 'status') == selected,
                    'cold recovery replayed unknown or repaired selection')
            report['checks'].append(dict(exact_approval_and_unknown_cold_recovery=True))

            prior_root = root / 'prior'
            prior_root.mkdir()
            (prior_root / 'config.yaml').write_text(json.dumps(config))
            prior = MCPHarness(binary, prior_root, fixture='')
            try:
                prior.setup(metadata=metadata)
                original_prior = select_composition_without_mcp(prior, binary, graph, policy)
                report['checks'].append(dict(prior_composition_run=read(prior, 'mcp-prior-cold', 'ORIGINAL_PEER_COLD', managed=False)))
                require(prior.command('plugin', 'status') == original_prior,
                        'runtime silently opted an existing composition into MCP')
            finally:
                prior.cleanup()

            stopped_root = root / 'stopped'
            stopped_root.mkdir()
            (stopped_root / 'config.yaml').write_text(json.dumps(config))
            stopped = MCPHarness(binary, stopped_root, fixture='')
            try:
                stopped.setup(metadata=metadata)
                stopped.command('plugin', 'stop')
                original_stop = stopped.command('plugin', 'status')
                stopped.command('chat', 'work')
                value = stopped.command('plugin', 'status')
                require(value == original_stop and value['stopped'] and value['graph_revision'] == '',
                        'default startup overrode an owner stop')
                report['checks'].append(dict(stop_before_initial_selection=True))
            finally:
                stopped.cleanup()

            without_mcp = select_composition_without_mcp(h, binary, graph, policy, selected['graph_revision'])
            report['checks'].append(dict(disabled_run=read(h, 'mcp-default-disabled', '', unavailable=True)))
            require(h.command('plugin', 'status') == without_mcp, 'cold process reselected a removed MCP method')
            h.command('plugin', 'deactivate', text=json.dumps(dict(operation_id='mcp-default-disable',
                expected_graph_revision=without_mcp['graph_revision'])))
            h.command('chat', 'work')
            require(h.command('plugin', 'status')['graph_revision'] == '' and effects.read_bytes() == before,
                    'restart reenabled a disabled method or repeated an effect')
            require('SECOND_LINE_MUST_NOT_ENTER_MODEL' not in json.dumps(requests),
                    'unselected namespace source entered a model request')
            require(any(any(t['function']['name'] == 'builtin__codemode_execute' for t in
                    r.get('body', {}).get('tools', [])) for r in requests),
                    'normal provider bridge never received the production CodeMode surface')
        finally:
            h.cleanup()
    report['cleanup'] = True
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    report = run(args.binary.resolve())
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(dict(checks=len(report['checks']), default_packages=len(report['default_packages']),
                         cleanup=report['cleanup'])))
