#!/usr/bin/env python3
"""Default bundle/cold CLI plus actual App/bridge/CodeMode account lifecycle.

Only model HTTP and in-process external Composio peers are synthetic. No
production endpoint override, real account or fixture-side recovery repair.
"""
import argparse
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import tempfile

from fixture_pool import run as owned_run
from verify_capabilities import CapabilityHarness
from verify_main import require
from verify_web_plugins import model_peer

ROOT = Path(__file__).resolve().parents[2]
TEST = 'TestConnectedAppsDefaultProductionCompositionAndColdLifecycle'


class AppsHarness(CapabilityHarness):
    def args(self, *args):
        return [str(self.binary), '-config', str(self.root / 'config.yaml'), *args]


def run(binary, evidence):
    report = dict(checks=[], live_service='not_run', cleanup=False,
                  binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
    with tempfile.TemporaryDirectory(prefix='steward-apps-default-cli-') as tmp, model_peer() as (endpoint, _):
        root = Path(tmp).resolve()
        node = shutil.which('node')
        require(node, 'pinned Node is required for the production bridge and CodeMode')
        config = dict(version=1, llm=dict(provider_bridge_command=str(binary),
            provider_bridge_script='-model-bridge',
            provider_bridge_version='steward-go-v1'), javascript=dict(command=node,
            script=str(ROOT / 'runtime/javascript/src/sidecar.mjs')))
        (root / 'config.yaml').write_text(json.dumps(config))
        h = AppsHarness(binary, root, fixture='')
        h.env.pop('COMPOSIO_API_KEY', None)
        try:
            metadata = dict(name='Apps default workload', provider_kind='openai_compatible', endpoint=endpoint,
                wire_api='openai_completions', model_id='fixture-model', context_window=65536,
                max_output_tokens=4096, compatibility=dict(tool_result_format='openai'),
                capabilities=dict(text_input=True, tool_calls=True, streaming=True))
            h.setup(metadata=metadata)
            h.command('chat', 'work')
            selected = h.command('plugin', 'status')
            with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT']) / 'state.db')) as db, db:
                db.execute('BEGIN')
                packages = {row[0] for row in db.execute("SELECT p.plugin_id FROM rsi_graph g,"
                    "json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON "
                    "p.digest=json_extract(b.value,'$.package_digest') WHERE g.revision=?",
                    (selected['graph_revision'],))}
                require(len(packages) == 40 and {'steward.connected-apps', 'steward.github-pr-followup','steward.artifact-workbench','steward.memory-inspector'}.issubset(packages) and
                    selected['epoch'] == 1 and not selected['stopped'], 'actual CLI did not verify/select all40 packages')
                state = json.loads(db.execute('SELECT document_json FROM connected_apps').fetchone()[0])
                require(not state.get('connections') and not state.get('key_ref'),
                    'package installation created account or credential authority')
            report['default_packages'] = sorted(packages)
            report['checks'].append(dict(actual_cli_initial_default40=True, no_account_authority=True))
            h.command('chat', 'work')
            require(h.command('plugin', 'status') == selected, 'cold CLI rewrote original plugin selection')
            report['checks'].append(dict(actual_cli_cold_preserves_selection=True))
        finally:
            h.cleanup()
    # App accepts the external protocol peer through its existing test-only
    # Client field. It uses the exact standard bundle just proven by the CLI.
    environment = dict(os.environ, STEWARD_TEST_APP_DEFAULT_BUNDLE=str(binary))
    # Eight real compositions run serially; observed race runs take roughly
    # two minutes each. Bound the whole suite without truncating later modes.
    result = owned_run(['go', 'test', '-race', '-json', './internal/app', '-run', '^' + TEST + '$',
        '-count=1', '-timeout=20m'], cwd=ROOT, env=environment, capture_output=True, text=True, timeout=1260)
    log = evidence.with_name(evidence.stem + '-app-go.jsonl')
    log.write_text(result.stdout)
    if result.stderr:
        evidence.with_name(evidence.stem + '-app-stderr.log').write_text(result.stderr)
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    terminal = [event['Action'] for event in events if event.get('Test') == TEST and
                event['Action'] in ('pass', 'fail', 'skip')]
    scenarios = {suffix: [event['Action'] for event in events if event.get('Test') == TEST + '/' + suffix
        and event['Action'] in ('pass', 'fail', 'skip')] for suffix in ('managed33', 'managed32', 'prior30', 'prior29', 'prior28', 'prior24', 'prior23', 'prior22')}
    require(result.returncode == 0 and terminal == ['pass'] and all(value == ['pass'] for value in scenarios.values()),
        'actual default App lifecycle failed or was skipped: ' + result.stdout[-12000:] + result.stderr[-2000:])
    report['checks'].append(dict(actual_app_default40_account_lifecycle=True,
        test=TEST, terminal=terminal, scenarios=scenarios, log=str(log), queued_scope=True, cold_read=True,
        exact_approval=True, unknown_non_replay=True, independent_disable_and_owner_control=True))
    require(hashlib.sha256(binary.read_bytes()).hexdigest() == report['binary_sha256'],
        'built distribution changed during actual acceptance')
    report['cleanup'] = True
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    report = run(args.binary.resolve(), args.evidence)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(dict(checks=len(report['checks']), default_packages=len(report['default_packages']), cleanup=report['cleanup'])))
