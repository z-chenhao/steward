#!/usr/bin/env python3
"""The private PR check partition; each lane owns its checkout and processes."""
import argparse
import json
import os
import sys

from runner import CORE
from race import CAPABILITIES_SQLITE_PATTERN
from verify_channels import CHANNEL_GROUPS

# Keep the public local Make targets intact. CI partitions their existing work
# across isolated runners (ordered longest-first so runner capacity favors the
# critical path) rather than rebuilding a shared, mutable worktree.
SOFTWARE = {
    'go-domains': 'make test-go-domains',
    'go': 'make test-go-core',
    'connected-apps-app': 'make test-connected-apps-app',
    'browser-apps': 'make test-browser-apps',
    'tool-discovery': 'make test-tool-discovery',
    'prompts': 'make test-prompts',
    'connected-apps': 'make test-connected-apps-core',
    'browser-infra': 'make test-browser-web test-infra',
    'provider': 'npm test --prefix provider-bridge',
    'lint': 'make lint',
    'frontend': 'npm run check --prefix web\nnpm test --prefix web\nnpm test --prefix runtime/javascript',
}
SYSTEM = {
    'startup-ui': ('startup', 'intelligent-ui', 'ui-interactions', 'capability-form-plugins'),
    'tools-provider': ('tools', 'artifacts', 'code-recovery', 'local-provider', 'account', 'model-menu-plugins', 'feishu-plugins'),
    'main-tasks': ('main', 'tasks', 'children'),
}
MEMORY_GATES = ('memory-maintenance', 'recall')
CAPABILITIES = ('task-code-environments', 'task-code-recovery', 'task-code-exact-report', 'tool-loading', 'main-business')
RACE = "go test -race -p 1 ./internal/agent ./internal/coding ./internal/computer/projectcontainer ./internal/config/setup ./internal/interaction/web ./internal/interaction/feishu ./internal/llm/bridge ./internal/app ./internal/llm/secret ./internal/tui -run '" + CAPABILITIES_SQLITE_PATTERN + "' -count=1"
JS_ONLY_LANES = ('frontend', 'provider')


def plan(kind):
    if kind == 'software':
        names = tuple(SOFTWARE)
    else:
        channel_lanes = tuple('channels-' + group for group in sorted(CHANNEL_GROUPS, key=lambda g: g != 'web'))
        names = (
            'memory-context',
            'memory-race',
            'artifact-plugins',
            'capabilities-race',
            'web-plugins',
            'web-access-race',
            'mcp-plugins',
            'github-plugins',
            'connected-apps-plugins',
            'startup-ui',
            'coding-development',
            'tools-provider',
            *channel_lanes,
            'main-tasks',
            'computer-plugins',
            'tool-discovery-plugins',
        )
    return dict(include=[dict(
        lane=name,
        go=str(name not in JS_ONLY_LANES).lower(),
        sandbox=str(name not in JS_ONLY_LANES).lower(),
        frontend=str(name in ('frontend', 'browser-infra', 'lint')).lower(),
        browser=str(name in ('browser-infra', 'browser-apps', 'lint', 'tools-provider', 'startup-ui', 'connected-apps-plugins') or name.startswith('channels-')).lower(),
    ) for name in names])


def command(lane):
    if lane in SOFTWARE:
        return SOFTWARE[lane]
    if lane in SYSTEM:
        gates = ' '.join('--gate ' + gate for gate in SYSTEM[lane])
        return f'make build\npython3 tests/system/runner.py {gates} --binary bin/steward --evidence evidence/e2e-{lane}'
    if lane.startswith('channels-') and lane.removeprefix('channels-') in CHANNEL_GROUPS:
        group = lane.removeprefix('channels-')
        return f'make build\npython3 tests/system/verify_channels.py --group {group} --binary bin/steward --evidence evidence/{lane}/report.json'
    if lane == 'computer-plugins':
        return 'make build\npython3 tests/system/runner.py --gate computer-plugins --binary bin/steward --evidence evidence/e2e-computer-plugins'
    if lane == 'web-access-race':
        return 'make test-web-access-core'
    if lane == 'web-plugins':
        return 'make build test-web-access-extract\npython3 tests/system/runner.py --gate web-plugins --binary bin/steward --evidence evidence/e2e-web-plugins'
    if lane == 'mcp-plugins':
        return ("go test -race -p 1 -timeout=15m ./internal/tool/mcp/... ./internal/plugin/mcpbridge/... -run '^Test(MCP|ToolUnknown|ArtifactQueryRecovery|TaskControlCancelTreeAndInvocationOutcomes)' -count=1\n"
                'python3 tests/system/race.py --part mcp-sqlite\n'
                'make build\npython3 tests/system/runner.py --gate mcp-plugins --binary bin/steward --evidence evidence/e2e-mcp-plugins')
    if lane == 'connected-apps-plugins':
        return 'make build\npython3 tests/system/runner.py --gate connected-apps-plugins --binary bin/steward --evidence evidence/e2e-connected-apps-plugins'
    if lane == 'tool-discovery-plugins':
        return 'make build\npython3 tests/system/runner.py --gate tool-discovery-plugins --binary bin/steward --evidence evidence/e2e-tool-discovery-plugins'
    if lane == 'github-plugins':
        return 'make test-github-plugins\npython3 tests/system/runner.py --gate github-plugins --binary bin/steward --evidence evidence/e2e-github-plugins'
    if lane == 'artifact-plugins':
        return 'make test-artifact-plugins\npython3 tests/system/runner.py --gate artifact-plugins --binary bin/steward --evidence evidence/e2e-artifact-plugins'
    if lane == 'coding-development':
        return ('make test-coding-environment CODING_ENV_EVIDENCE=evidence/coding-environment\n'
                'make test-coding-development CODING_DEVELOPMENT_EVIDENCE=evidence/coding-development')
    if lane == 'memory-context':
        gates = ' '.join('--gate ' + gate for gate in MEMORY_GATES)
        return f"make test-memory MEMORY_RACE_ARGS='--shard 1/2' build\npython3 tests/system/runner.py {gates} --binary bin/steward --evidence evidence/e2e-{lane}"
    if lane == 'memory-race':
        return 'python3 tests/system/race.py --part memory --shard 2/2'
    if lane == 'capabilities-race':
        commands = ['make build', RACE,
                    'python3 tests/system/race.py --part capabilities-sqlite',
                    "go test -race ./internal/observability/sqlite ./internal/rsi -run '^TestTrace(ManagedHistoryWithoutCatalog|ColdMissingCatalog|PartialActivationSchema)' -count=1"]
        commands.extend(f'python3 tests/system/verify_capabilities.py --binary bin/steward --scenario {scenario} --evidence evidence/{scenario}.json'
                        for scenario in CAPABILITIES)
        return '\n'.join(commands)
    raise ValueError('unknown CI lane: ' + lane)


def passed(needs):
    # A missing/cancelled matrix is never green. Only the explicit docs path may
    # skip code, and scope must itself have completed successfully.
    if set(needs) != {'scope', 'software', 'system'} or needs['scope'].get('result') != 'success':
        return False
    docs = needs['scope'].get('outputs', {}).get('docs_only')
    if docs not in ('true', 'false'):
        return False
    expected = 'skipped' if docs == 'true' else 'success'
    return all(needs[name].get('result') == expected for name in ('software', 'system'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument('--plan', choices=('software', 'system'))
    action.add_argument('--lane')
    action.add_argument('--results', help='GitHub needs JSON for the required aggregate check')
    args = parser.parse_args()
    gates = [gate for group in SYSTEM.values() for gate in group]
    if len(gates) != len(set(gates)) or set(gates) != set(CORE) - {'channels'}:
        parser.error('CI must run every core gate exactly once')
    if args.plan:
        print(json.dumps(plan(args.plan)))
        return 0
    if args.results is not None:
        return 0 if passed(json.loads(args.results)) else 1
    shell = command(args.lane)
    # Reuse the existing cancellation owner and source-stability evidence.
    os.execv(sys.executable, [sys.executable, 'tests/system/process_step.py',
        '--name', args.lane, '--phase', 'test', '--', 'bash', '-eu', '-c', shell])


if __name__ == '__main__':
    raise SystemExit(main())
