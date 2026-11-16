"""Reproduce the initial Owner case using captured native evidence only.

The mechanical bindings isolate already authorized Host fragments. No candidate
module, executable or current view/copy asset is read to obtain the expectation.
"""
import hashlib
import json
from pathlib import Path
import re
import sys

BASE = Path(__file__).resolve().parent

def asset(source):
    return {'source': source, 'digest': 'sha256:' + hashlib.sha256(source.encode()).hexdigest()}

feature=re.compile(r'(?:[.#](?:tasks?[-:]|execution[-:]|attention[-:]|scheduled?[-:]|conversation[-:]|composer\b|thread\b|welcome\b|message\b|reply-draft\b|agent-processing\b|typing-dots\b|attachment-(?:controls|picker|status)\b|chat-workspace\b|setup-callout\b|outgoing\b)|#new-messages\b)')
def selectors(value):
    out=[];start=0;depth=0
    for i,c in enumerate(value):
        if c in '([':depth+=1
        if c in ')]':depth-=1
        if c==',' and depth==0:out.append(value[start:i].strip());start=i+1
    out.append(value[start:].strip());return out

def split(source,all_owned=False):
    source=re.sub(r'/\*.*?\*/','',source,flags=re.S)
    owned=[];shared=[];text=source.strip()
    while text:
        at=text.index('{');header=text[:at].strip();end=at+1;depth=1
        while depth:
            if text[end]=='{':depth+=1
            if text[end]=='}':depth-=1
            end+=1
        body=text[at+1:end-1];text=text[end:].strip()
        if header.startswith('@keyframes'):
            a,b=header+'{'+body+'}', ''
        elif header.startswith('@'):
            a,b=split(body,all_owned)
            a=header+'{'+a+'}' if a else '';b=header+'{'+b+'}' if b else ''
        else:
            aa=[];bb=[]
            for selector in selectors(header):
                (aa if all_owned or feature.search(selector) or selector==':root' and '--chat-accent:' in body else bb).append(selector)
            a=','.join(aa)+'{'+body+'}' if aa else '';b=','.join(bb)+'{'+body+'}' if bb else ''
        if a:owned.append(a)
        if b:shared.append(b)
    return '\n'.join(owned),'\n'.join(shared)

def build():
    evidence = json.loads((BASE / 'provenance.json').read_text())
    for item in evidence['files']:
        data = (BASE / item.get('capture', item['source'])).read_bytes()
        if hashlib.sha256(data).hexdigest() != item['sha256']:
            raise ValueError('native evidence changed: ' + item['source'])
    names = ('tasks', 'task', 'attention', 'conversation', 'schedule-editor')
    source = '\n'.join((BASE / ('internal/interaction/web/templates/' + name + '.html')).read_text() for name in names)
    copy_source = source
    for component, binding in (('approval-card', 'workbenchApproval'), ('help', 'workbenchHelp'), ('identifier', 'workbenchIdentifier')):
        source = re.sub(r'{{template "' + component + r'" ([^}]+)}}', r'{{' + binding + r' \1}}', source)
    source = source.replace('{{template "computer-panel" .}}', '{{workbenchComputer}}').replace('{{template "worker-panel" .}}', '{{workbenchWorkers}}')
    count = (BASE / 'internal/interaction/web/tasks.go.txt').read_text()
    for original in ('return len(v.Executions.Items)', 'count := len(v.Scheduled)', 'if entry.Schedule == nil'):
        if original not in count:
            raise ValueError('original record count differs')
    source = source.replace('{{define "tasks-list"}}', '''{{define "tasks-list"}}
{{$recordCount := 0}}{{if .Tasks.Configurations}}{{$recordCount = len .Tasks.Scheduled}}{{range .Tasks.List.Items}}{{if not .Schedule}}{{$recordCount = add $recordCount 1}}{{end}}{{end}}{{else}}{{$recordCount = len .Tasks.Executions.Items}}{{end}}''')
    source = source.replace('(taskPageCount .Tasks)', '$recordCount').replace('{{taskPageCount .Tasks}}', '{{$recordCount}}')
    frontend = '\n'.join((BASE / f).read_text() for f in ('web/src/live-updates.ts', 'web/src/task-executions.ts', 'web/src/task-filters.tsx'))
    keys = set(re.findall(r'\btr "((?:[^"\\]|\\.)*)"', copy_source))
    keys.update(m[1] for m in re.findall(r"\bt\((['\"])(.*?)(?<!\\)\1\)", frontend))
    keys.update(('Needs me', 'Execution details', 'Previous execution', 'Next execution', 'Tasks', 'Task', 'Workspace', 'Chat'))
    policy = json.loads((BASE / 'policy.json').read_text())
    keys.update(v['key'] for v in policy['history'])
    keys.update(('active', 'completed', 'failed', 'cancelled', 'queued', 'running', 'waiting', 'paused', 'Working', 'Stopped', 'Waiting', 'Needs input', 'Pending approval', 'unknown', 'succeeded'))
    dictionary = json.loads((BASE / 'internal/interaction/web/locales/zh-CN.json').read_text())
    # Dynamic say()/feedback callers use literals beyond direct t() calls.
    # Derive their translations from captured original client + dictionary only.
    for match in re.finditer(r"([\"'])(?:\\.|(?!\1).)*?\1", frontend):
        literal = match.group()[1:-1].replace("\\'", "'").replace('\\"', '"').replace('\\\\', '\\')
        if literal.strip() in dictionary:
            keys.add(literal)
    entries = [dict(key=k, en=k, zh_cn=k.replace(k.strip(), dictionary.get(k.strip(), k.strip()))) for k in sorted(keys) if k]
    copy = json.dumps(entries, ensure_ascii=False, indent=2) + '\n'
    styles = '\n'.join(split((BASE / f'web/src/{name}.css').read_text(), name == 'task-planning')[0] for name in ('workspace', 'task-planning', 'conversation')) + '\n'
    return dict(instance_id='web-workbench', contract='steward.web-workbench/1', input={}, expected=dict(view=asset(source), stylesheet=asset(styles), copy=asset(copy), policy=policy))

if __name__ == '__main__':
    expected = build()
    if len(sys.argv) == 2:
        actual = json.loads(Path(sys.argv[1]).read_text())
        if expected != actual:
            raise SystemExit('Owner case differs from independently captured native evidence')
        print('Owner case matches captured native evidence')
    else:
        print(json.dumps(expected, ensure_ascii=False, indent=2))
