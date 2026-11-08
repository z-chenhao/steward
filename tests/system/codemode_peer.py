"""Owned model-peer support for the production Code Mode-only tool surface.

The peer still sends real Code Mode source. All nested calls execute through the
production runtime, approval gates and durable ToolExecutor; this only keeps the
fixture's deterministic response plans independent of their transport wrapper.
"""
import copy
import json

ENTRY = 'builtin__codemode_execute'
MAIN_DIRECT = {ENTRY, 'builtin__tool_search', 'builtin__intelligent_ui_show', 'builtin__skill_list', 'builtin__skill_view', 'builtin__task_manage', 'builtin__task_search'}
WORKER_DIRECT = MAIN_DIRECT - {'builtin__intelligent_ui_show', 'builtin__task_manage', 'builtin__task_search'}
MARKER = '// fixture-call: '
MAIN = ['builtin__intelligent_ui_show', 'builtin__task_search', 'builtin__task_manage',
        'builtin__artifact_manage', 'builtin__evidence_read', 'builtin__memory_manage',
        'builtin__memory_search', 'builtin__automation_manage', 'builtin__image_ocr',
        'builtin__skill_list', 'builtin__skill_view']
WORKER = ['builtin__read', 'builtin__bash', 'builtin__edit', 'builtin__write',
          'builtin__artifact_manage', 'builtin__memory_search',
          'builtin__skill_list', 'builtin__skill_view', ENTRY]


def project(body):
    names = [t.get('function', {}).get('name') for t in body.get('tools', [])]
    if ENTRY not in names or not set(names) <= MAIN_DIRECT:
        return body, False
    body = copy.deepcopy(body)
    root = any('general Worker executing' in str(m.get('content', '')) for m in body['messages'])
    body['tools'] = [dict(type='function', function=dict(name=name)) for name in (WORKER if root else MAIN)]
    for message in body['messages']:
        for call in message.get('tool_calls', []):
            function = call.get('function', {})
            if function.get('name') != ENTRY:
                continue
            source = json.loads(function['arguments']).get('source', '')
            if source.startswith(MARKER):
                original = json.loads(source.split('\n', 1)[0][len(MARKER):])
                function['name'] = original['name']
                function['arguments'] = json.dumps(original['arguments'])
        if message['role'] == 'tool':
            try:
                items = json.loads(message['content'])
                if isinstance(items, list) and len(items) == 1 and items[0].get('type') == 'text':
                    message['content'] = items[0].get('text', items[0].get('value', ''))
            except (ValueError, TypeError, AttributeError):
                pass
    return body, True


def wrap(delta):
    delta = copy.deepcopy(delta)
    for call in delta.get('tool_calls', []):
        function = call['function']
        if function['name'] == ENTRY:
            continue
        original = dict(name=function['name'], arguments=json.loads(function['arguments']))
        source = MARKER + json.dumps(original) + '\n'
        source += ('const value=await tools[' + json.dumps(original['name']) + '](' +
                   json.dumps(original['arguments']) + ');text(typeof value==="string"?value:JSON.stringify(value));')
        function['name'] = ENTRY
        function['arguments'] = json.dumps(dict(source=source))
    return delta


def runtime_yaml():
    from pathlib import Path
    import shutil
    script = Path(__file__).resolve().parents[2] / 'runtime/javascript/src/sidecar.mjs'
    return ('javascript:\n  command: ' + json.dumps(shutil.which('node')) +
            '\n  script: ' + json.dumps(str(script)) + '\n')
