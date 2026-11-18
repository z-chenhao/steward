#!/usr/bin/env python3
"""Actual default bundle -> Main Tool -> HTTP search peer -> cold restart."""
import argparse
from contextlib import closing, contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading

from provider_endpoint import Endpoint
from verify_capabilities import CapabilityHarness
from verify_main import require


class WebHarness(CapabilityHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'), *args]


@contextmanager
def model_peer(mode='web-plugins'):
    server = ThreadingHTTPServer(('127.0.0.1', 0), Endpoint)
    server.requests, server.mode = [], mode
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
def source_peer(marker):
    calls = []

    class Peer(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            calls.append(self.path)
            body = json.dumps({'results': [{'url': 'https://example.com/original',
                                            'title': marker, 'content': marker + ' original provider evidence'}]}).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = ThreadingHTTPServer(('127.0.0.1', 0), Peer)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}', calls
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        require(not thread.is_alive(), 'HTTP peer did not join')


def selected(h, configured=True):
    value = h.command('plugin', 'status')
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
        graph = json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',
                                      (value['graph_revision'],)).fetchone()[0])
        packages = {row[0] for row in db.execute(
            "SELECT p.plugin_id FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b "
            "JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest') WHERE g.revision=?",
            (value['graph_revision'],))}
    require(value['epoch'] == 1 and not value['stopped'] and len(packages) == 41 and
            {'steward.apps-directory', 'steward.apps-accounts', 'steward.apps-logos',
             'steward.apps-interface', 'steward.github-pr-followup','steward.artifact-workbench'}.issubset(packages),
            'complete default composition was not selected')
    binding = next(b for b in graph['bindings'] if b['instance_id'] == 'web-extract')
    revision = binding['config']['runtime_revision']
    require((revision.startswith('sha256:') and not binding['config'].get('unavailable_error')) if configured
            else revision == 'web-parser-v1:unavailable', 'optional Node configuration was not frozen exactly')
    return value, sorted(packages)


def search(h, key, marker):
    receipt = h.send(key, 'WEB_PLUGIN_SEARCH ' + key)
    h.command('chat', 'work')
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
        raw = db.execute("SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='builtin__web_search'",
                         (receipt['run_id'],)).fetchone()
        require(raw, 'Main did not execute the real Web Tool')
        result = json.loads(raw[0])
        require(not result.get('error') and marker in result.get('text', ''),
                'default plugin failed to preserve original HTTP source evidence: ' + raw[0])
        frozen = db.execute('SELECT managed_services_json FROM rsi_run WHERE run_id=?',
                            (receipt['run_id'],)).fetchone()[0]
        require('steward.web-content-route/1' in frozen and 'steward.web-extract/1' in frozen,
                'Main escaped the complete managed service set')
        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id IN ('web-access','web-extract')").fetchone()[0] == 0,
                'pure Web methods created a duplicate effect ledger')
    return receipt['run_id']


def run(binary):
    report = {'checks': [], 'cleanup': False}
    with tempfile.TemporaryDirectory(prefix='steward-web-default-') as tmp, \
            source_peer('ORIGINAL_SOURCE_A') as (first, first_calls), \
            source_peer('ORIGINAL_SOURCE_B') as (second, second_calls):
        root = Path(tmp).resolve()
        h = WebHarness(binary.resolve(), root, fixture='')
        node = shutil.which('node')
        require(node, 'doctor: pinned Node is required for the real model bridge and Code Mode')
        repo = Path(__file__).resolve().parents[2]
        config = {'version': 1, 'web_access': {'source_order': ['searxng'], 'searxng_url': first},
                  'llm': {'provider_bridge_command': node,
                          'provider_bridge_script': str(repo / 'provider-bridge/bridge.mjs'),
                          'provider_bridge_version': 'pi-ai-1.3.0-v1'},
                  'javascript': {'command': node, 'script': str(repo / 'runtime/javascript/src/sidecar.mjs')}}
        (root / 'config.yaml').write_text(json.dumps(config))
        with model_peer() as (endpoint, requests):
            try:
                metadata = dict(name='Web workload', provider_kind='openai_compatible', endpoint=endpoint,
                                wire_api='openai_completions', model_id='fixture-model', context_window=65536,
                                max_output_tokens=4096, compatibility={'tool_result_format': 'openai'},
                                capabilities={'text_input': True, 'tool_calls': True, 'streaming': True})
                with tempfile.TemporaryDirectory(prefix='steward-web-unconfigured-') as optional:
                    optional_root = Path(optional).resolve()
                    optional_config = {key: value for key, value in config.items() if key != 'javascript'}
                    (optional_root / 'config.yaml').write_text(json.dumps(optional_config))
                    unconfigured = WebHarness(binary.resolve(), optional_root, fixture='')
                    try:
                        unconfigured.setup(metadata=metadata)
                        unconfigured.command('chat', 'work')
                        before, _ = selected(unconfigured, configured=False)
                        unconfigured.command('chat', 'work')
                        require(unconfigured.command('plugin', 'status') == before,
                                'cold restart repaired an explicitly unavailable parser')
                        report['checks'].append({'unconfigured_default_and_cold_restart': True})
                    finally:
                        unconfigured.cleanup()
                h.setup(metadata=metadata)
                h.command('chat', 'work')
                original, packages = selected(h)
                report['default_packages'] = packages
                report['checks'].append({'first_run': search(h, 'default-web-first', 'ORIGINAL_SOURCE_A')})
                # Every command is a new actual process. The next dependent action
                # follows cold reopen immediately, without select/update repair.
                report['checks'].append({'cold_run': search(h, 'default-web-cold', 'ORIGINAL_SOURCE_A')})
                require(h.command('plugin', 'status') == original, 'cold restart reselected versions')
                config['web_access']['searxng_url'] = second
                (root / 'config.yaml').write_text(json.dumps(config))
                report['checks'].append({'new_owner_config_run': search(h, 'default-web-new-endpoint', 'ORIGINAL_SOURCE_B')})
                require(h.command('plugin', 'status') == original, 'network config changed the parser version')
                require(len(first_calls) == 2 and len(second_calls) == 1,
                        'restart/config change dropped or replayed source requests')
                disabled = h.command('plugin', 'deactivate', text=json.dumps({
                    'operation_id': 'owner-disable-default-web',
                    'expected_graph_revision': original['graph_revision']}))
                h.command('chat', 'work')
                require(h.command('plugin', 'status')['graph_revision'] == '',
                        'cold restart reenabled owner-disabled default plugins')
                report['disabled'] = disabled
                require(any(any(t['function']['name'] == 'builtin__codemode_execute' for t in
                                r.get('body', {}).get('tools', [])) for r in requests),
                        'actual model bridge did not receive the production Code Mode surface')
            finally:
                h.cleanup()
    report['cleanup'] = True
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    report = run(args.binary)
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'checks': len(report['checks']), 'default_packages': len(report['default_packages']),
                      'cleanup': report['cleanup']}))
