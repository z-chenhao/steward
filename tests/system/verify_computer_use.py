#!/usr/bin/env python3
"""CLI/QuickJS -> ordinary ToolExecutor -> actual SSH/Cua/Browser adapters.

Peers are deterministic protocol Fakes. --live-config selects real Lume/Guest
boundaries with the same drive; no online accounts or websites are required.
"""
import argparse
from contextlib import contextmanager, nullcontext, closing
import hashlib
import json
from pathlib import Path
import selectors
import signal
import time
import urllib.error
import shutil
import sqlite3
import subprocess
import sys
import tempfile

from verify_computer import ComputerHarness, request
from verify_main import require


def selected_default_computer(h):
    # Observe the normal Host-owned bootstrap. Tests do not install, compose,
    # relax its policy or activate a second Computer-only graph.
    status = h.command('plugin', 'status')
    if status['epoch']==0 and not status['stopped']:
        # Read-only control commands do not prepare the runtime. Start the
        # ordinary idle worker once; it uses the same bootstrap as real work.
        h.command('chat', 'work')
        status = h.command('plugin', 'status')
    require(status['graph_revision'] and status['epoch']==1 and not status['stopped'], 'default feature composition was not selected')
    with closing(sqlite3.connect(Path(h.env['STEWARD_DATA_ROOT'])/'state.db')) as db:
        graph=json.loads(db.execute('SELECT graph_json FROM rsi_graph WHERE revision=?',(status['graph_revision'],)).fetchone()[0])
        packages={row[0] for row in db.execute("SELECT p.plugin_id FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest') WHERE g.revision=?",(status['graph_revision'],))}
    require(len(packages)==41 and {'steward.computer-use','steward.web-access','steward.web-extract','steward.mcp-bridge','steward.connected-apps','steward.tool-discovery','steward.apps-directory','steward.apps-accounts','steward.apps-logos','steward.apps-interface', 'steward.github-pr-followup', 'steward.artifact-workbench'}.issubset(packages), 'Computer replaced the complete default composition')
    binding=next(b for b in graph['bindings'] if b['instance_id']=='computer-use')
    source=Path(__file__).resolve().parents[2]
    upload=hashlib.sha256(b'computer-browser-upload-v1\0playwright-core@1.64.1\0'+(source/'internal/computer/macosvm/browser_upload_v1.mjs').read_bytes()+b'\0'+(source/'internal/computer/macosvm/transfer.py').read_bytes()).hexdigest()
    snapshot=hashlib.sha256(b'computer-browser-snapshot-v1\0playwright-core@1.64.1\0'+(source/'internal/computer/macosvm/browser_snapshot_v1.mjs').read_bytes()).hexdigest()
    require(binding['config']=={'browser_upload_driver':upload,'browser_snapshot_driver':snapshot}, 'default graph lost exact fixed resources')
    h.default_plugin_selection=status
    h.default_plugin_packages=sorted(packages)
    return status['graph_revision']


@contextmanager
def launch(binary, root, live_config=None, browser_peer=None, managed_plugins=False):
    if not managed_plugins:
        carrier=root/'native-carrier'/'steward'
        carrier.parent.mkdir(exist_ok=True)
        if carrier.exists():
            require(hashlib.sha256(carrier.read_bytes()).digest()==hashlib.sha256(binary.read_bytes()).digest(),'native carrier source changed during recovery')
        else:
            shutil.copy2(binary,carrier)
        binary=carrier
    source, vm = root / 'source', root / 'vm'
    server = None
    h = ComputerHarness(binary, root, fixture='computer')
    try:
        if live_config:
            computer = json.loads(Path(live_config).read_text())['computer']
            h.env['STEWARD_COMPUTER_GUEST_TEST_LOGIN'] = 'lume'
            computer['manifest']={'version':1,'applications':[],'artifact_refs':[]}
        else:
            source.mkdir(mode=0o700)
            vm.mkdir(mode=0o700)
            if browser_peer:
                (vm / 'browser-peer.json').write_text(json.dumps({'endpoint': browser_peer}))
            for name in ('disk.img', 'nvram.bin', 'config.json'):
                (source / name).write_text('fixture-' + name)
            (source / 'config.json').write_text(json.dumps({'macAddress': '02:00:00:00:00:01', 'machineIdentifier': 'fixture', 'cpuCount': 4}))
            h.env['STEWARD_COMPUTER_FIXTURE_PASSWORD'] = 'fixture-only'
            start = h.processes.start if h.processes else subprocess.Popen
            server = start([str(binary), '-test-computer-server', str(vm)], stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True, env=h.env, cwd=root)
            with selectors.DefaultSelector() as selector:
                selector.register(server.stdout, selectors.EVENT_READ)
                require(selector.select(10), 'doctor: fixture HTTP/SSH peer not ready')
            computer = json.loads(server.stdout.readline())
            if browser_peer:
                (vm / 'browser-peer.json').unlink()
        node = shutil.which('node')
        require(node, 'doctor: Node required')
        script = Path(__file__).resolve().parents[2] / 'runtime/javascript/src/sidecar.mjs'
        (root / 'config.yaml').write_text(json.dumps({'version': 1, 'javascript': {'command': node, 'script': str(script)}, 'computer': computer}))
        if not live_config:
            require(h.command('computer', 'prepare-base', str(source))['base'] == 'sealed', 'base not sealed')
        if not h.command('status')['agent_ingress_enabled']:
            # Full Worker schemas and retained GUI/approval receipts exceed the
            # generic 8K fixture. Declare the actual workload before first use;
            # never replace an existing selection during restart/recovery.
            h.setup()
        h.managed_computer_graph = selected_default_computer(h) if managed_plugins else None
        yield h
    finally:
        h.cleanup()
        if server:
            server.terminate()
            server.communicate(timeout=15)
            if h.processes:h.processes.retire(server)
            require(server.returncode == 0, 'fixture server did not join cleanly')


def collect_observations(h, report, live_root):
    """Read existing scoped Artifacts; no GUI input or service startup."""
    kind = 'browser' if report.get('real_guest_browser') else 'native'
    saved = []
    for check in report['checks']:
        for nested in check['nested']:
            result = nested.get('result') or {}
            if result.get('error'): continue
            try: value = json.loads(result.get('text', ''))
            except (ValueError, TypeError): continue
            if not isinstance(value, dict): continue
            shot = value.get('screenshot', value)
            if not isinstance(shot, dict) or not isinstance(shot.get('image'), dict): continue
            ref = shot['image'].get('artifactRef')
            if not ref: continue
            readback = subprocess.run(h.args('artifact', 'open', ref), env=h.env, capture_output=True, timeout=20)
            require(readback.returncode == 0 and readback.stdout.startswith(b'\x89PNG'), 'observation Artifact unreadable')
            name = f'computer-use-{kind}-observation-{len(saved)+1:02}.png'
            (live_root/'evidence'/name).write_bytes(readback.stdout)
            saved.append({'file':name, 'artifactRef':ref, 'sha256':hashlib.sha256(readback.stdout).hexdigest()})
    require(saved, 'no screenshot observations collected')
    report['screenshots'] = saved


def run(binary, live_config=None, live_root=None, work_root=None, native_only=False, provision=False, browser_live=False, managed_plugins=False):
    drive_nonce = str(time.time_ns())
    report = {'version': 1, 'scope': 'real Guest' if live_config else 'offline SSH/Cua/Playwright protocol peers; no real GUI claim', 'checks': [], 'cleanup': False}
    with (nullcontext(work_root) if work_root else nullcontext(tempfile.mkdtemp(prefix='steward25-cua-use-',dir=live_root)) if live_config else tempfile.TemporaryDirectory(prefix='steward-cua-use-')) as temporary:
        root = Path(temporary).resolve()
        with launch(binary, root, live_config, managed_plugins=managed_plugins) as h:
            if managed_plugins:
                report['managed_computer_graph'] = h.managed_computer_graph
                report['default_plugin_packages']=h.default_plugin_packages
            state = Path(h.env['STEWARD_DATA_ROOT']) / 'state.db'
            if browser_live:
                config=json.loads((root/'config.yaml').read_text())
                config['computer']['browser']={'node':'/Users/lume/steward25-runtime/node/bin/node','module':'/Users/lume/steward25-runtime/browser/node_modules/playwright-core/index.mjs','port':9222}
                (root/'config.yaml').write_text(json.dumps(config))
            # The unbundled carrier exercises historical native Computer Runs.
            # Its input is explicitly created by the native fixture, while the
            # managed variant keeps the ordinary production Artifact path.
            artifact_args = ('artifact', 'save', 'private-fixture.txt', 'text/plain')
            if not managed_plugins:
                artifact_args = ('-test-bridge', 'main', *artifact_args)
            artifact = h.command(*artifact_args, text='private fixture bytes')
            def rows():
                with closing(sqlite3.connect(state)) as db, db:
                    db.row_factory = sqlite3.Row
                    return [dict(r) for r in db.execute('SELECT * FROM tool_invocation ORDER BY created_at,rowid')]

            def execute(key, source, approve=True, refs=()):
                plan = json.dumps([{'name': 'builtin__codemode_execute', 'arguments': {'source': cell}} for cell in (source if isinstance(source,list) else [source])])
                key_label = key
                if live_config: key = key + ':' + drive_nonce
                # Computer belongs to a real Task Worker; Main delegates the plan.
                if refs:
                    h.command('chat', 'send-parts', key, text=json.dumps([{'type':'text','text':'Delegate capabilities: '+plan}, *[{'type':'file','artifact_ref':ref} for ref in refs]]))
                else:
                    h.send(key, 'Delegate capabilities: ' + plan)
                approvals = []
                for _ in range(40):
                    if live_config and key_label=='inventory-bind-observe-independent-revisions' and _==0:
                        release=root/'release-live-install'
                        release.unlink(missing_ok=True)
                        worker=h.hold('computer_ready',extra_flags=('-test-main-release',str(release)),timeout=240)
                        with closing(sqlite3.connect(state)) as db, db:
                            instance=json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
                        from lume_live import Live
                        live=Live(live_root,'product',instance['native_name'])
                        live.put_fixture('steward25Fixture')
                        plist='<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleIdentifier</key><string>com.steward.fixture</string><key>CFBundleName</key><string>Steward CUA Fixture</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>'
                        import shlex
                        directory='/Users/lume/Applications/StewardCUAFixture.app/Contents'
                        h.guest_command(live_root,'mkdir -p '+directory+'/MacOS && cp /Users/lume/steward24-fixtures/steward25Fixture '+directory+'/MacOS/Fixture && chmod 755 '+directory+'/MacOS/Fixture && printf %s '+shlex.quote(plist)+' > '+directory+'/Info.plist && /usr/bin/codesign --force --sign - '+directory+'/.. && /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f '+directory+'/..')
                        release.touch();worker.communicate(timeout=320);require(worker.returncode==0,'live initial worker failed')
                    elif browser_live and _==0:
                        release=root/'release-browser-doctor'
                        release.unlink(missing_ok=True)
                        worker=h.hold('computer_ready',extra_flags=('-test-main-release',str(release)),timeout=240)
                        with closing(sqlite3.connect(state)) as db, db: instance=json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
                        from lume_live import Live
                        live=Live(live_root,'product',instance['native_name'])
                        target=live_root/'fixtures/steward25-browser-doctor.sh'
                        require(not target.exists(),'refusing to replace an existing Doctor fixture')
                        shutil.copyfile(Path(__file__).resolve().parents[2]/'tests/fixtures/computer-use-v1/doctor_browser.sh',target)
                        try: live.put_fixture(target.name)
                        finally: target.unlink()
                        try: report['guest_browser_doctor']=live.guest('/bin/bash /Users/lume/steward24-fixtures/steward25-browser-doctor.sh',timeout=45)
                        finally: release.touch()
                        worker.communicate(timeout=320)
                        require(worker.returncode==0,'browser Doctor worker failed')
                    elif provision and key_label=='native-elements-secondary-selection-and-coordinates' and _==1:
                        release=root/'release-browser-provision'
                        release.unlink(missing_ok=True)
                        worker=h.hold('computer_ready',extra_flags=('-test-main-release',str(release)),timeout=240)
                        with closing(sqlite3.connect(state)) as db, db: instance=json.loads(db.execute('SELECT instance_json FROM computer').fetchone()[0])
                        from lume_live import Live
                        live=Live(live_root,'product',instance['native_name'])
                        sources={'steward25-package.json':'guest-browser/package.json','steward25-package-lock.json':'guest-browser/package-lock.json','steward25-transfer.py':'internal/computer/macosvm/transfer.py','steward25-site.mjs':'tests/fixtures/computer-use-v1/site.mjs','steward25-provision.sh':'tests/fixtures/computer-use-v1/provision_browser.sh'}
                        repository=Path(__file__).resolve().parents[2]
                        for name,path in sources.items():
                            target=live_root/'fixtures'/name
                            require(not target.exists(),'refusing to replace an existing provision fixture')
                            shutil.copyfile(repository/path,target)
                            try: live.put_fixture(name)
                            finally: target.unlink()
                        report['guest_browser_provision']=live.guest('/bin/bash /Users/lume/steward24-fixtures/steward25-provision.sh',timeout=180)
                        release.touch();worker.communicate(timeout=320);require(worker.returncode==0,'provision worker failed')
                    else:
                        h.command('chat', 'work')
                    pending = [a for a in h.command('approval', 'list') if a['status'] == 'pending']
                    if not pending: break
                    for a in pending:
                        before = rows()
                        with closing(sqlite3.connect(state)) as db, db:
                            target_id = db.execute('SELECT invocation_id FROM approval WHERE approval_id=?', (a['approval_id'],)).fetchone()[0]
                        target = next(i for i in before if i['invocation_id'] == target_id)
                        require(target['attempt_count'] == 0, 'effect dispatched before approval')
                        require(target['replay_policy'] == 'never', 'effect became safely retryable')
                        normalized = json.loads(a['action_parameters']) if a.get('action_parameters') else None
                        if managed_plugins and target['tool_name'].startswith('builtin__computer_') and json.loads(target['arguments_json']).get('method') in {'launchApp','createBrowserTab','click','scroll','drag','setValue','selectText','performSecondaryAction','typeText','pressKey','paste','goto','back','forward','reload','close','playwright.click','playwright.fill','playwright.download','playwright.upload','exportArtifact'}:
                            with closing(sqlite3.connect(state)) as db:
                                plan = db.execute('SELECT plan_json,plan_hash,graph_revision,request_hash FROM computer_action_plan WHERE invocation_id=?', (target_id,)).fetchone()
                            require(plan and json.loads(plan[0]) == normalized and hashlib.sha256(plan[0].encode()).hexdigest() == plan[1] and plan[2] == h.managed_computer_graph and plan[3] == target['request_hash'], 'approval is not bound to a complete immutable Computer parameter plan')
                        approvals.append({'tool': target['tool_name'], 'arguments': json.loads(target['arguments_json']), 'hash': a['frozen_arguments_hash'], 'action_parameters': normalized})
                        h.command('approval', 'approve' if approve else 'deny', a['approval_id'], a['frozen_arguments_hash'])
                else: raise AssertionError('drive did not settle')
                invocations = rows()
                parent = [r for r in invocations if r['tool_name'] == 'builtin__codemode_execute'][-1]
                nested = [r for r in invocations if r['parent_invocation_id'] == parent['invocation_id']]
                require(parent['status'] == 'succeeded', f'cell failed: {parent["result_json"]}; nested=' + json.dumps([dict(tool=r['tool_name'],status=r['status'],attempts=r['attempt_count'],result=(r['result_json'] or '')[:2000]) for r in nested]))
                with closing(sqlite3.connect(state)) as db:
                    require(db.execute("SELECT kind FROM run WHERE run_id=?", (parent['run_id'],)).fetchone()[0] == 'worker', 'Computer plan bypassed the actual Task Worker')
                if managed_plugins:
                    with closing(sqlite3.connect(state)) as db:
                        db.execute('BEGIN')
                        bindings = db.execute("SELECT b.graph_revision,b.managed_services_json FROM tool_invocation i JOIN rsi_run b USING(run_id) WHERE i.parent_invocation_id=? AND i.tool_name LIKE 'builtin__computer_%' AND i.attempt_count>0", (parent['invocation_id'],)).fetchall()
                        require(bindings and all(revision == h.managed_computer_graph and {'steward.computer-observation/1','steward.computer-wait-recipe/1','steward.computer-documentation/1','steward.computer-text-selection/1','steward.computer-inventory-order/1','steward.computer-target-match/1','steward.computer-action-parameters/1','steward.computer-playwright-parameters/1','steward.computer-transfer-presentation/1','steward.computer-browser-upload/1','steward.computer-browser-projection/1'} <= set(json.loads(services)) for revision,services in bindings), 'Computer call lost frozen managed observation/wait/documentation/selection/inventory/match/action-parameters composition')
                        require(db.execute("SELECT count(*) FROM rsi_call WHERE instance_id='computer-use'").fetchone()[0] == 0, 'pure Computer methods created a duplicate effect ledger')
                require(all(json.loads(r['conflict_keys']) for r in nested), 'read/action missed desktop barrier')
                output = json.loads(json.loads(parent['result_json'])['text'])
                report['checks'].append({'name': key, 'output': output, 'approvals': approvals,
                                         'nested': [{'tool': i['tool_name'], 'status': i['status'], 'attempts': i['attempt_count'], 'policy': json.loads(i['policy_json']), 'arguments': json.loads(i['arguments_json']), 'result': json.loads(i['result_json']) if i['result_json'] else None} for i in nested]})
                return output, approvals

            if not native_only and not browser_live:
                app = 'com.steward.fixture' if live_config else 'com.apple.TextEdit'
                output, _ = execute('inventory-bind-observe-independent-revisions', f'''
    const inventory=await cua.getState();
    {('await cua.computer.launchApp({app:"com.steward.fixture"});' if live_config else '')}
    const app=await cua.getApp({json.dumps(app)});
    const doc=await app.documentation();
    if(!doc.includes("interface Surface"))throw new Error("versioned surface documentation missing");
    const ax=await app.getAXState({{full:true}});
    const shot=await app.getScreenshot();
    image(shot.image);
    return {{computer:inventory.computerId, ax:ax.state, revision:ax.revision, screenshot:shot.revision}};
    ''')
                require(output[0]['type'] == 'image' and output[0]['mimeType'] == 'application/x-steward-artifact', 'explicit image reference output missing')
                image_ref = output[0]['data']
                image_bytes = subprocess.run(h.args('artifact', 'open', image_ref), env=h.env, capture_output=True, timeout=20)
                require(image_bytes.returncode == 0 and image_bytes.stdout.startswith(b'\x89PNG'), 'screenshot Artifact not readable')
                report['screenshot_sha256'] = hashlib.sha256(image_bytes.stdout).hexdigest()

                output, approvals = execute('action-observe-old-reference-and-wait', f'''
    {('await cua.computer.launchApp({app:"com.steward.fixture"});' if live_config else '')}
    const app=await cua.getApp({json.dumps(app)});
    const old=await app.getAXState();
    await app.typeText("approved fixture");
    const current=await app.getAXStateAndScreenshot();
    let stale;try{{await app.click({{observationRevision:old.revision,index:0}});}}catch(e){{stale=e.code;}}
    let wait;try{{await app.waitForChange({{sinceRevision:current.ax.revision,timeoutMs:1}});}}catch(e){{wait={{code:e.code,observation:e.details?.observation}};}}
    return {{state:current.ax.state,stale,wait}};
    ''')
                value = output[-1]['value']
                require(value['stale'] == 'STALE_ELEMENT_REFERENCE' and value['wait']['code'] == 'WAIT_TIMEOUT', 'stale/wait contract missing')
                require('approved fixture' in value['state'], 'post-action observation did not show input')
                require(approvals, 'input had no approval')

            if not live_config and not browser_live:
                output, approvals = execute('discovery-binding-reference-integrity', r'''
const apps=await cua.listApps();const windows=await cua.listWindows();
const browsers=await cua.listBrowsers();const tabs=await cua.listTabs({browser:browsers[0].id});
const byName=await cua.getApp(apps[0].name);const byWindow=await cua.getApp({windowId:windows[0].id,id:"ignored"});
const browser=await cua.getBrowser();const tab=await cua.getTab({tabId:tabs[0].id});
const observed=await byWindow.getAXState();
let missing;try{await cua.getApp("missing-app");}catch(error){missing=error.code;}
return {app:byName.ref.id,window:byWindow.ref.id,originalWindow:windows[0].id,browser:browser.ref.id,tab:tab.ref.id,
 originalTab:tabs[0].id,observed:!!observed.revision,missing,privateData:JSON.stringify([...apps,...windows,...browsers,...tabs]).includes('native')};
''')
                value=output[-1]['value']
                require(value['app']=='com.apple.TextEdit' and value['window']==value['originalWindow'] and value['tab']==value['originalTab'] and value['observed'] and value['missing']=='NOT_FOUND' and not value['privateData'], 'discovery/binding changed source identity, window precedence, privacy or missing-target behavior')
                require(not approvals, 'pure discovery/binding unexpectedly acquired action authority')
                output, approvals = execute('approved-parameters-and-native-key', r'''
const app=await cua.getApp("com.apple.TextEdit");
await app.pressKey("escape");
const current=await app.getAXState();
await app.scroll({observationRevision:current.revision,index:0},"down",2.5);
return {completed:true};
''')
                require(output[-1]['value']['completed'] and len(approvals)==2, 'key/scroll did not use their exact original approvals')
                if managed_plugins:
                    key=next(a for a in approvals if a['arguments'].get('method')=='pressKey')
                    scroll=next(a for a in approvals if a['arguments'].get('method')=='scroll')
                    require(key['action_parameters']=={'text':'escape','key_input':{'keys':['escape']}} and scroll['action_parameters']=={'direction':'down','amount':2.5}, 'actual CLI lost frozen keyboard decomposition or explicit fractional scroll amount')
                output, approvals = execute('native-selection-exact-approved-span', r'''
const app=await cua.getApp("com.apple.TextEdit");
const initial=await app.getAXState();
await app.setValue({observationRevision:initial.revision,index:0},"one target end");
const current=await app.getAXState();
await app.selectText({observationRevision:current.revision,index:0},"target");
const selected=await app.getAXState();
let stale;try{await app.selectText({observationRevision:current.revision,index:0},"target");}catch(error){stale=error.code;}
return {state:selected.state,stale};
''')
                value=output[-1]['value']
                require('selection=4:6' in value['state'] and value['stale']=='STALE_ELEMENT_REFERENCE', 'native selection recipe changed original span or reference freshness')
                selection_approvals=[entry for entry in approvals if entry['arguments'].get('method')=='selectText']
                require(len(selection_approvals)==2 and all(entry['arguments']['args'][1]=='target' for entry in selection_approvals), 'native selection lost exact original action approval')

            if live_config and not browser_live:
                output,_=execute('native-elements-secondary-selection-and-coordinates', r'''
await cua.computer.launchApp({app:"com.steward.fixture"});
const window=(await cua.listWindows()).find(w=>w.name==="Steward CUA Fixture");
if(!window)throw new Error("fixture window not discovered");
const app=await cua.getApp({windowId:window.id});
const field=ax=>Number(ax.state.match(/\[(\d+)\] AXTextField/)[1]);
const before=await app.getAXState();
await app.getScreenshot();
await app.setValue({observationRevision:before.revision,index:field(before)},"committed value");
const set=await app.getAXState();
await app.performSecondaryAction({observationRevision:set.revision,index:field(set)},"confirm");
const committed=await app.getAXState();
if(!committed.state.includes("committed=committed value")) throw new Error("commit readback failed");
await app.selectText({observationRevision:committed.revision,index:field(committed)},"committed");
const selected=await app.getAXStateAndScreenshot();
if(!selected.ax.state.includes("selection=0:9")) throw new Error("selection readback failed");
const button=Number(selected.ax.state.match(/\[(\d+)\] AXButton "Increment"/)[1]);
await app.click({observationRevision:selected.ax.revision,index:button});
const clicked=await app.getAXStateAndScreenshot();
if(!clicked.ax.state.includes("count=1")) throw new Error("element click readback failed");
await app.click([105,186]);
const pointer=await app.getAXStateAndScreenshot();
if(!pointer.ax.state.includes("count=2")) throw new Error("coordinate click readback failed");
image(pointer.screenshot.image);
text(pointer.ax.state.split("\n").slice(0,7).join("\n"));
return {committed:true,selected:"0:9",elementCount:1,pointerCount:2};
''')
                report['native_action_readback']=True
                selected=next(item for item in output if item['type']=='image')
                image_bytes=subprocess.run(h.args('artifact','open',selected['data']),env=h.env,capture_output=True,timeout=20)
                require(image_bytes.returncode==0,'native screenshot artifact unavailable')
                report['screenshot_sha256']=hashlib.sha256(image_bytes.stdout).hexdigest()
            if browser_live:
                output,approvals=execute('real-guest-browser-locators-transfers-and-expiry', r'''
const tabs=await cua.listTabs();const tab=await cua.getTab(tabs[0].id);
await tab.goto("http://127.0.0.1:18025/");
let observed=await tab.getAXStateAndScreenshot();
const input=Number(observed.ax.state.match(/\[(\d+)\] INPUT "Fixture input"/)[1]);
await tab.setValue({observationRevision:observed.ax.revision,index:input},"Guest core input");
observed=await tab.getAXStateAndScreenshot();
if(!observed.ax.state.includes("Guest core input"))throw new Error("core input readback");
await tab.capabilities.list();const pw=await tab.capabilities.get("playwright");
await pw.locator("#field").fill("Guest verified input");
observed=await tab.getAXStateAndScreenshot();
if(!observed.ax.state.includes("Guest verified input"))throw new Error("fill readback");
await pw.locator("#button").click();
observed=await tab.getAXStateAndScreenshot();
if(!observed.ax.state.includes("clicked"))throw new Error("click readback");
const download=await pw.locator("#download").download();
observed=await tab.getAXStateAndScreenshot();
const exported=await cua.files.exportArtifact({path:download.path,name:"guest-download.txt",mediaType:"text/plain"});
const file=await cua.files.importArtifact({artifactRef:SOURCE_REF,destination:UPLOAD_PATH});
await pw.locator("#upload").upload(file);
observed=await tab.getAXStateAndScreenshot();
if(!observed.ax.state.includes("guest-upload"))throw new Error("chooser readback");
await tab.reload();observed=await tab.getAXStateAndScreenshot();
let stale;try{await pw.locator("#field").count();}catch(e){stale=e.code;}
if(stale!=="STALE_CAPABILITY_HANDLE")throw new Error("navigation expiry");
image(observed.screenshot.image);return {exported,stale,coreInput:true,fill:true,click:true,chooser:true};
'''.replace('SOURCE_REF',json.dumps(artifact['artifact_ref'])).replace('UPLOAD_PATH',json.dumps('imports/guest-upload-'+drive_nonce)),refs=[artifact['artifact_ref']])
                exported=output[-1]['value']['exported']['artifactId']
                readback=subprocess.run(h.args('artifact','open',exported),env=h.env,capture_output=True,timeout=20)
                require(readback.returncode==0 and readback.stdout==b'Guest browser download fixture','real Guest download bytes differ')
                selected=next(item for item in output if item['type']=='image')
                image_bytes=subprocess.run(h.args('artifact','open',selected['data']),env=h.env,capture_output=True,timeout=20)
                require(image_bytes.returncode==0,'browser screenshot artifact unavailable')
                report['screenshot_sha256']=hashlib.sha256(image_bytes.stdout).hexdigest()
                report['real_guest_browser']=True
            if live_config:
                require(h.command('computer','status')['phase']=='checkpointed','live Computer was not released')
                report['screenshot_file']='computer-use-browser-live.png' if browser_live else 'computer-use-native-live.png'
                (live_root/'evidence'/report['screenshot_file']).write_bytes(image_bytes.stdout)
                collect_observations(h, report, live_root)
                report['cleanup']=True
                return report

            output, approvals = execute('browser-projection-original-indices-and-approved-input', r'''const tab=await cua.getTab("fixture-tab");
const first=await tab.getAXState({full:true});
await tab.typeText({observationRevision:first.revision,index:0},"bounded-dom-source");
const second=await tab.getAXState({full:true});
return {first:first.state,second:second.state,revisionChanged:first.revision!==second.revision};
''')
            require(len(approvals)==1 and output[-1]['value']['revisionChanged'] and 'bounded-dom-source' in output[-1]['value']['second'], 'original browser index/input/observation flow lost')
            if managed_plugins:
                require('[0] INPUT "Fixture"' in output[-1]['value']['second'], 'managed snapshot source was not reconstructed')

            output, approvals = execute('transfer-overwrite-export-and-upload-separate-consent', f'''
const file=await cua.files.importArtifact({{artifactRef:{json.dumps(artifact['artifact_ref'])},destination:"imports/fixture"}});
await cua.files.importArtifact({{artifactRef:{json.dumps(artifact['artifact_ref'])},destination:"imports/fixture",overwrite:true}});
const exported=await cua.files.exportArtifact({{path:file.path,name:"roundtrip.txt",mediaType:"text/plain"}});
const tab=await cua.getTab("fixture-tab");
await tab.capabilities.list();
const pw=await tab.capabilities.get("playwright");
await pw.locator("#upload").upload(file);
const download=await pw.locator("#download").download();
const downloaded=await cua.files.exportArtifact({{path:download.path,name:"download.txt",mediaType:"text/plain"}});
return {{exported,downloaded}};
''', refs=[artifact['artifact_ref']])
            require(len(approvals) == 6, f'import, overwrite, export, upload, download approvals were conflated: {len(approvals)} {output}')
            exported = output[-1]['value']['exported']['artifactId']
            readback = subprocess.run(h.args('artifact', 'open', exported), env=h.env, capture_output=True, timeout=20)
            require(readback.returncode == 0 and readback.stdout == b'private fixture bytes', 'transfer roundtrip bytes differ')

            output, approvals = execute('upload-presentation-original-bytes-and-separate-consent', f"""const file=await cua.files.importArtifact({{artifactRef:{json.dumps(artifact['artifact_ref'])},destination:"imports/upload-source.bin"}});
const tab=await cua.getTab("fixture-tab");await tab.capabilities.list();const pw=await tab.capabilities.get("playwright");
await pw.locator("#upload").upload(file);return {{observed:await pw.locator("#status").textContent()}};
""", refs=[artifact['artifact_ref']])
            digest=hashlib.sha256(b'private fixture bytes').hexdigest()
            require(output[-1]['value']['observed']==f'uploaded:upload-source.bin;media=application/octet-stream;sha256={digest}', 'upload metadata or original bytes changed')
            require(len(approvals)==2, 'import authorization became upload consent')
            if managed_plugins:
                upload=next(a for a in approvals if a['arguments'].get('method')=='playwright.upload')
                require(upload['action_parameters']=={'selector':'#upload','timeout_ms':3000,'name_from_source':True,'name':'upload-source.bin','media_type':'application/octet-stream'}, 'upload metadata was not concretely approved')

            output, approvals = execute('transfer-default-presentation-and-explicit-intent', f'''const file=await cua.files.importArtifact({{artifactRef:{json.dumps(artifact['artifact_ref'])},destination:"imports/metadata-source.txt"}});
const defaulted=await cua.files.exportArtifact({{path:file.path}});
const explicit=await cua.files.exportArtifact({{path:file.path,name:"explicit.txt",mediaType:"text/plain"}});
return {{defaulted,explicit}};''',refs=[artifact['artifact_ref']])
            require(len(approvals)==3,'import/export approvals changed')
            result=output[-1]['value']
            require(result['defaulted']['name']=='metadata-source.txt' and result['defaulted']['mediaType']=='application/octet-stream' and result['explicit']['name']=='explicit.txt' and result['explicit']['mediaType']=='text/plain','transfer display intent changed')
            for item in result.values():
                content=subprocess.run(h.args('artifact','open',item['artifactId']),env=h.env,capture_output=True,timeout=20)
                require(content.returncode==0 and content.stdout==b'private fixture bytes','presentation policy replaced exported bytes')
            if managed_plugins:
                exports=[a for a in approvals if a['arguments']['method']=='exportArtifact']
                require(exports[0]['action_parameters']=={'name_from_source':True,'name':'metadata-source.txt','media_type':'application/octet-stream'} and exports[1]['action_parameters']=={'name':'explicit.txt','media_type':'text/plain'},'concrete export metadata missing before original approval')

            output, approvals = execute('playwright-parameters-original-intent-and-reads', r'''const tab=await cua.getTab("fixture-tab");
await tab.capabilities.list();const pw=await tab.capabilities.get("playwright");
await pw.locator("#field").fill("exact browser fill");
const count=await pw.locator("#field").count();
await pw.locator("#field").waitFor({timeoutMs:7});
await pw.waitForURL("http://fixture.test/",{timeoutMs:11});
return {count,completed:true};''')
            require(len(approvals)==1 and output[-1]['value']=={'count':1,'completed':True},'Playwright read/action behavior changed')
            if managed_plugins:
                require(approvals[0]['action_parameters']=={'selector':'#field','timeout_ms':3000,'value':'exact browser fill'},'Playwright approval omitted exact normalized intent')
                with closing(sqlite3.connect(state)) as db:
                    plans=[json.loads(row[0]) for row in db.execute("SELECT plan_json FROM computer_action_plan WHERE method IN ('playwright.waitForSelector','playwright.waitForURL')")]
                require({'selector':'#field','timeout_ms':7} in plans and {'url':'http://fixture.test/','timeout_ms':11} in plans,'explicit read deadlines did not freeze into original domain plans')

            output,approvals=execute('registered-files-survive-cell-and-instance-replacement',[
                'return await cua.files.importArtifact({artifactRef:'+json.dumps(artifact['artifact_ref'])+',destination:"imports/cross-cell"});',
                'return await cua.files.exportArtifact({path:"imports/cross-cell",name:"cross-cell.txt",mediaType:"text/plain"});'
            ],refs=[artifact['artifact_ref']])
            require(len(approvals)==2 and output[-1]['value']['artifactId'],'file registration lost between cells')
            output,_=execute('known-file-path-is-not-cross-run-authority','try{await cua.files.exportArtifact({path:"imports/cross-cell"});}catch(e){return e.code;}')
            require(output[-1]['value']=='PERMISSION_DENIED','file scope expanded to another Run')

            output, _ = execute('navigation-expires-capability-and-screenshotless-pointer', '''
const tab=await cua.getTab("fixture-tab");await tab.capabilities.list();const pw=await tab.capabilities.get("playwright");
const doc=await pw.documentation();if(!doc.includes("interface Playwright"))throw new Error("versioned browser documentation missing");
await tab.goto("http://fixture.test/next");
let expired;try{await pw.locator("input").count();}catch(e){expired=e.code;}
let pointer;try{await tab.click([1,1]);}catch(e){pointer=e.code;}
return {expired,pointer};
''')
            require(output[-1]['value'] == {'expired': 'STALE_CAPABILITY_HANDLE', 'pointer': 'SCREENSHOT_REQUIRED'}, 'navigation/capture fence missing')
            output, _ = execute('denied-input-does-not-dispatch', f'''const app=await cua.getApp({json.dumps(app)});try{{await app.typeText("must not dispatch");}}catch(e){{return e.code;}}''', approve=False)
            require(output[-1]['value'] == 'APPROVAL_DENIED', 'denied input dispatched')
            output, _ = execute('guest-path-traversal-rejected', '''try{await cua.files.exportArtifact({path:"../private"});}catch(e){return e.code;}''')
            require(output[-1]['value'] == 'PERMISSION_DENIED', 'path traversal accepted')
            with closing(sqlite3.connect(state)) as db, db:
                requests=[json.loads(row[0]) for row in db.execute('SELECT request_json FROM model_generation')]
                selected={ref for req in requests for message in req['messages'] for ref in message.get('image_refs',[])}
                expected_image_ref = image_ref if image_ref.startswith('artifact:') else 'artifact:' + image_ref
                require(selected=={expected_image_ref},'unselected observation entered model image context: selected='+str(sorted(selected))+' expected='+expected_image_ref)
                require(all('screenshot_png_b64' not in json.dumps(req) for req in requests),'binary screenshot entered model ledger')
                artifacts=db.execute("SELECT count(*) FROM artifact WHERE media_type='image/png'").fetchone()[0]
                require(artifacts>len(selected),'nested screenshots were not retained separately')
                report['output_selection']={'model_image_refs':list(selected),'internal_screenshots':artifacts,'binary_in_ledger':False}
            require(h.command('computer', 'status')['phase'] == 'checkpointed', 'idle did not release Computer')
            if managed_plugins:
                require(h.command('plugin','status')==h.default_plugin_selection,'restart or business work replaced operator selection')
        report['cleanup'] = True
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--live-config', type=Path)
    parser.add_argument('--live-root', type=Path)
    parser.add_argument('--resume-root', type=Path)
    parser.add_argument('--native-actions-only', action='store_true')
    parser.add_argument('--provision-browser', action='store_true')
    parser.add_argument('--live-browser', action='store_true')
    parser.add_argument('--managed-plugins', action='store_true', help='exercise Computer through the complete normal default composition')
    parser.add_argument('--collect-observations', type=Path, help='read an existing live report and its scoped screenshot Artifacts without starting services')
    args = parser.parse_args()
    if args.collect_observations:
        require(args.resume_root and args.live_root and args.resume_root.parent.resolve()==args.live_root.resolve() and args.resume_root.name.startswith('steward25-cua-use-'), 'collection requires the owned live evidence root')
        report=json.loads(args.collect_observations.read_text())
        require(report.get('scope')=='real Guest' and report.get('cleanup'), 'collection requires a completed live report')
        h=ComputerHarness(args.binary.resolve(),args.resume_root)
        collect_observations(h,report,args.live_root)
        args.evidence.parent.mkdir(parents=True,exist_ok=True)
        args.evidence.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
        print(json.dumps({'evidence':str(args.evidence),'screenshots':len(report['screenshots']),'read_only':True}))
        sys.exit(0)
    server=None
    log=None
    compiled=None
    try:
        if args.live_config:
            require(args.live_root is not None,'--live-root is required with --live-config')
            if args.resume_root:
                require(args.resume_root.parent.resolve()==args.live_root.resolve() and args.resume_root.name.startswith('steward25-cua-use-'),'resume must use the owned evidence directory')
            from lume_live import Live
            live=Live(args.live_root,'product')
            live.doctor()
            fixture=Path(__file__).resolve().parents[2]/'tests/fixtures/computer-use-v1/Fixture.swift'
            compiled=args.live_root/'fixtures/steward25Fixture'
            require(not compiled.exists(),'refusing to replace an existing live fixture')
            subprocess.run(['xcrun','swiftc','-target','arm64-apple-macos15.0','-framework','AppKit',str(fixture),'-o',str(compiled)],check=True,timeout=60)
            log=(args.live_root/'logs/steward25-server.log').open('w')
            server=subprocess.Popen([sys.executable,str(Path(__file__).with_name('lume_live.py')),'--root',str(args.live_root),'--profile','product','serve'],stdout=log,stderr=log)
            endpoint=json.loads(args.live_config.read_text())['computer']['endpoint']
            for _ in range(100):
                try: request(endpoint,'/lume/config');break
                except (urllib.error.URLError,ConnectionError): time.sleep(.1)
            else: raise RuntimeError('live Lume API did not start')
        report = run(args.binary.resolve(), args.live_config, args.live_root,args.resume_root,args.native_actions_only,args.provision_browser,args.live_browser,args.managed_plugins)
    finally:
        if server is not None:
            server.send_signal(signal.SIGINT)
            server.wait(timeout=45)
        if log is not None: log.close()
        if compiled is not None and compiled.exists(): compiled.unlink()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'evidence': str(args.evidence), 'checks': len(report['checks']), 'cleanup': report['cleanup']}))
