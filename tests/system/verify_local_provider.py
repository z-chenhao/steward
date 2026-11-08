#!/usr/bin/env python3
"""Owned HTTP endpoint -> actual Go CLI -> actual pinned Node/Pi Bridge.

Launch/doctor/drive/evidence/cleanup are one foreground, disposable operation.
"""
import argparse
import base64
import struct
import zlib
from contextlib import closing
from http.server import ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile
import threading
import time
from verify import STATE_SCHEMA_VERSION, require
from provider_endpoint import Endpoint
from codemode_peer import runtime_yaml, MAIN_DIRECT, WORKER_DIRECT

def verify(binary, script):
    require(shutil.which("node") is not None, "Node is missing")
    server = ThreadingHTTPServer(("127.0.0.1", 0), Endpoint)
    server.daemon_threads = True
    server.requests, server.mode = [], "success"
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    report = {"bridge": "pi-ai-1.3.0-v1", "checks": []}
    try:
        with tempfile.TemporaryDirectory(prefix="steward-local-") as directory:
            root = Path(directory)
            endpoint = f"http://127.0.0.1:{server.server_port}/v1"
            env = {k: v for k, v in os.environ.items() if not k.startswith("STEWARD_")}
            env["STEWARD_DATA_ROOT"] = str(root / "data")
            env["STEWARD_FIXTURE_KEY"] = "SYNTHETIC_LOCAL_KEY_NEVER_PERSIST"
            config = root / "config.yaml"
            config.write_text("version: 1\nllm:\n  provider_bridge_command: node\n  provider_bridge_script: " + json.dumps(str(script)) + "\n  provider_bridge_version: pi-ai-1.3.0-v1\n")
            if '\njavascript:' not in config.read_text():
                config.write_text(config.read_text()+runtime_yaml())

            def command(*args, ok=True, text=None):
                p = subprocess.run([str(binary), "-config", str(config), *args], cwd=root, env=env,
                                   input=text, text=True, capture_output=True, timeout=20)
                require((p.returncode == 0) == ok, f"CLI {args[0:2]} unexpected result: {p.stderr}")
                require("SYNTHETIC_LOCAL_KEY_NEVER_PERSIST" not in p.stdout + p.stderr, "credential in CLI output")
                return json.loads(p.stdout) if ok else p.stderr

            require(command("status")["readiness"] == "setup_required", "implicit model")
            require(command("model", "discover", endpoint)[0]["model_id"] == "fixture-model", "catalog")
            profile = command("model", "add", endpoint, "fixture-model")
            id_ = profile["profile_id"]
            command("model", "select", id_, "1", "1", ok=False)
            require(command("status")["readiness"] == "setup_required", "unverified activation")
            proof = command("model", "test", id_)
            require(all(proof["result"].values()), "probe did not exercise every behavior")
            require(command("model", "select", id_, "1", "1")["readiness"] == "ready", "select")
            require(command("status")["readiness"] == "ready", "restart readiness")
            command("chat", "send", "local-main", text="actual local conversation")
            command("chat", "work")
            history = command("chat", "history")
            require([m["role"] for m in history] == ["user", "assistant"], "local Main history")
            require(history[-1]["content"][0]["text"] == "PROBE_RESULT_OK", "local Main did not continue after Direct Tool")
            require(any(m["role"] == "system" for r in server.requests if r.get("body") for m in r["body"].get("messages", [])), "Main System Prompt missing from actual wire")
            report["main_history"] = history
            surface = next(r["body"]["tools"] for r in reversed(server.requests)
                           if any(t["function"]["name"] == "builtin__codemode_execute"
                                  for t in r.get("body", {}).get("tools", [])))
            require({t["function"]["name"] for t in surface} == MAIN_DIRECT,
                    "production model differs from the frequent direct tool surface")
            with closing(sqlite3.connect(root / "data/state.db")) as db:
                frozen = json.loads(db.execute("SELECT tools_json FROM capability_snapshot WHERE role='main' ORDER BY created_at LIMIT 1").fetchone()[0])
            names = {spec['name'] for spec in frozen}
            require({'builtin__artifact_search','builtin__artifact_manage','builtin__memory_search',
                     'builtin__memory_manage','builtin__image_ocr'} <= names, 'internal tool authority disappeared')
            require(all(spec.get('codemode_only') for spec in frozen if spec['name'] not in MAIN_DIRECT),
                    'production authority permits a direct tool bypass')
            report["production_surface"] = surface

            # Real PNG -> scoped source -> offline Tesseract -> Tool result ->
            # text-only Pi HTTP. Kill after OCR commit, while the model waits.
            picture = Path(__file__).resolve().parents[2] / "internal/media/ocr/testdata/text.png"
            saved = subprocess.run([str(binary), "-config", str(config), "artifact", "save", "scan.png", "image/png"],
                                   input=picture.read_bytes(), capture_output=True, env=env, cwd=root, timeout=20)
            require(saved.returncode == 0, "OCR source upload failed")
            server.ocr_ref = json.loads(saved.stdout)["artifact_ref"]
            server.received_ocr, server.release_ocr = threading.Event(), threading.Event()
            server.mode, server.hold_ocr = "ocr", True
            command("chat", "send-parts", "local-ocr", text=json.dumps([
                {"type": "text", "text": "OCR_INPUT: read the words in this scan."},
                {"type": "image", "artifact_ref": server.ocr_ref}]))
            child = subprocess.Popen([str(binary), "-config", str(config), "chat", "work"],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, cwd=root)
            try:
                require(server.received_ocr.wait(15), "OCR text never reached actual text-only provider")
                child.kill(); child.communicate(timeout=10)
                with closing(sqlite3.connect(root / "data/state.db")) as db:
                    before = db.execute("SELECT invocation_id,attempt_count,result_json FROM tool_invocation WHERE tool_name='builtin__image_ocr'").fetchall()
                    pending = db.execute("SELECT generation_id,request_hash,request_json FROM model_generation WHERE status='pending'").fetchall()
                require(len(before) == 1 and before[0][1] == 1 and pending, "missing committed OCR or held generation")
                server.hold_ocr = False; server.release_ocr.set()
                command("chat", "work")
                with closing(sqlite3.connect(root / "data/state.db")) as db:
                    after = db.execute("SELECT invocation_id,attempt_count,result_json FROM tool_invocation WHERE tool_name='builtin__image_ocr'").fetchall()
                    recovered = db.execute("SELECT generation_id,request_hash,request_json FROM model_generation WHERE generation_id=?", (pending[0][0],)).fetchall()
                    require(after == before and recovered == pending, "restart repeated OCR or changed the frozen model request")
                    result = json.loads(after[0][2]); preview = json.loads(result["text"])
                    require(preview["method"] == "ocr" and preview["source_ref"] == server.ocr_ref and "OCR 12345" in preview["text"], "OCR source or transcription lost")
                    require(preview["document_ref"] in result["artifact_refs"] and len(result["artifact_refs"]) == 2, "full OCR artifact missing")
                    require(db.execute("SELECT count(*) FROM artifact_publication").fetchone()[0] == 0, "OCR auto-published a file")
                history = command("chat", "history")
                require(history[-1]["content"] == [{"type": "text", "text": "OCR_TEXT_RECEIVED"}], "OCR Main did not finish")
                report["ocr"] = {"preview": preview, "reused_invocation": before[0][0], "reused_generation": pending[0][0]}
                report["checks"].append("actual offline OCR enters text-only Pi context; SIGKILL reuses committed OCR and frozen generation without publishing")
            finally:
                server.release_ocr.set()
                if child.poll() is None: child.kill()
                child.communicate(timeout=10)

            server.mode = "memory-disabled"
            command("chat", "send", "disabled-memory", text="Remember that I prefer tea and search for tea.")
            command("chat", "work")
            with closing(sqlite3.connect(root / "data/state.db")) as db:
                failures = db.execute("SELECT tool_name,status,error_json FROM tool_invocation WHERE tool_name LIKE 'builtin__memory_%' ORDER BY tool_name").fetchall()
                require(len(failures) == 2 and all(row[1] == "failed" and json.loads(row[2])["code"] == "PROVIDER_UNAVAILABLE" for row in failures), "unconfigured Memory falsely succeeded")
                require(db.execute("SELECT count(*) FROM memory_mutation").fetchone()[0] == 0, "disabled memory queued a mutation")
            report["unconfigured_memory"] = failures
            report["checks"].append("production manage/search schemas; fixture echo absent; discoverable unconfigured Memory fails without mutation")
            server.mode = "success"
            report["checks"].append("actual CLI Main Actor/Loop/Direct Tool/Conversation through local Node/Pi with System Prompt")
            require(all(r["auth"] is None for r in server.requests), "fabricated Authorization")
            report["checks"] += ["catalog", "manual CLI add", "unverified rejection", "real five-part probe", "atomic select", "restart ready", "no fabricated key"]

            server.mode = "tasks"
            task_start = len(server.requests)
            command("chat", "send", "local-task", text="Delegate an independent report in the background.")
            command("chat", "work")
            task_history = command("chat", "history")
            require(task_history[-1]["content"][0]["text"] == "Main Task follow-up through Pi.", "actual Pi Task follow-up missing")
            task_requests = [r["body"] for r in server.requests[task_start:] if r.get("body")]
            roots = [r for r in task_requests if any("general Worker executing" in str(m.get("content", "")) for m in r["messages"])]
            require(roots and all(not any(t["function"]["name"].startswith("builtin__task_") or t["function"]["name"] == "builtin__memory_manage" for t in r["tools"]) for r in roots), "Root Task or Memory control exposed on actual Pi wire")
            require(all({t["function"]["name"] for t in r["tools"]} == WORKER_DIRECT for r in roots), "Root model bypasses Code Mode")
            require(all("actual local conversation" not in json.dumps(r["messages"]) for r in roots), "Root inherited Main Conversation")
            require(any(any(m["role"] == "user" and "Runtime task update (untrusted Worker data" in str(m.get("content", "")) for m in r["messages"]) for r in task_requests), "runtime framing lost at actual Pi boundary")
            with closing(sqlite3.connect(root / "data/state.db")) as db:
                frozen_worker = json.loads(db.execute("SELECT tools_json FROM capability_snapshot WHERE role='worker' ORDER BY created_at LIMIT 1").fetchone()[0])
                worker_manage = [spec for spec in frozen_worker if spec['name'] == 'builtin__memory_manage']
                require(any(spec['name']=='builtin__memory_search' and spec['permission']=='read_only' for spec in frozen_worker) and
                        len(worker_manage) == 1 and worker_manage[0]['version'] == 6 and
                        worker_manage[0]['permission'] == 'memory_control' and
                        worker_manage[0]['replay_policy'] == 'safe', 'Worker layered memory capability changed')
                require({'list', 'read', 'write', 'retire'} <= set(json.loads(worker_manage[0]['input_schema'])['properties']['operation']['enum']),
                        'Worker layered document operations missing')
                require(db.execute("SELECT count(*) FROM task WHERE status='completed'").fetchone()[0] == 1, "actual Pi Task missing")
                require(db.execute("SELECT count(*) FROM main_inbox").fetchone()[0] == 1, "actual Pi Reporter dedup missing")
            report["task_requests"] = task_requests
            report["task_history"] = task_history
            report["checks"].append("actual Node/Pi Task create -> Root echo/result -> runtime-framed Reporter -> one Main follow-up; role/context isolation")

            # A failed retest must revoke proof and close ingress after endpoint failure.
            server.mode = "no-tools"
            require("MODEL_INCOMPATIBLE" in command("model", "test", id_, ok=False), "missing tools accepted")
            require(command("status")["readiness"] == "degraded", "failed retest left ingress open")
            report["checks"].append("failed retest revokes active proof")

            server.mode = "manual"
            require("MODEL_NOT_FOUND" in command("model", "discover", endpoint, ok=False), "missing catalog")
            manual = command("model", "add", endpoint, "manual-id")["profile_id"]
            require(all(command("model", "test", manual)["result"].values()), "manual model probe")
            report["checks"].append("manual ID independent of catalog")

            server.mode = "success"
            auth = command("model", "login", "STEWARD_FIXTURE_KEY")
            keyed = command("model", "add", endpoint, "fixture-model", auth["connection_id"])["profile_id"]
            start = len(server.requests)
            command("model", "catalog", keyed)
            command("model", "test", keyed)
            require(all(r["auth"] == "Bearer " + env["STEWARD_FIXTURE_KEY"] for r in server.requests[start:]), "key missing/wrong")
            del env["STEWARD_FIXTURE_KEY"]
            require("AUTH_REQUIRED" in command("model", "test", keyed, ok=False), "missing key not rejected")
            report["checks"] += ["explicit environment credential", "missing credential rejection"]

            server.mode = "partial"
            require("INVALID_PROVIDER_RESPONSE" in command("model", "test", manual, ok=False), "partial stream completion")
            report["checks"].append("partial stream is not completion")

            with closing(sqlite3.connect(root / "data/state.db")) as db:
                require(db.execute("pragma user_version").fetchone()[0] == STATE_SCHEMA_VERSION, "schema")
                rows = db.execute("SELECT snapshot_id,run_id,input_tokens,output_tokens,latency_ms,error_code,snapshot_json FROM provider_usage").fetchall()
                require(rows and any(r[2:4] == (7, 8) for r in rows), "usage not recorded")
                require(any(r[5] == "GENERATION_CANCELLED" and r[2] is None for r in rows), "unknown cancellation usage fabricated")
                require(any(r[5] == "INVALID_PROVIDER_RESPONSE" for r in rows), "failed usage missing")
                require(all(r[4] >= 0 and json.loads(r[6])["snapshot_id"] == r[0] for r in rows), "snapshot attribution")
                require("SYNTHETIC_LOCAL_KEY_NEVER_PERSIST" not in "\n".join(db.iterdump()), "credential in SQLite")
                report["usage"] = [{"snapshot_id": r[0], "run_id": r[1], "input_tokens": r[2], "output_tokens": r[3], "latency_ms": r[4], "error_code": r[5]} for r in rows]
            for file in (root / "data/logs").glob("*"):
                require(b"SYNTHETIC_LOCAL_KEY_NEVER_PERSIST" not in file.read_bytes(), "credential in log")
            report["checks"] += ["SQLite usage and failure readback", "no credentials in DB or logs"]
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=3)
    report["cleanup"] = "all CLI children reaped; endpoint stopped; disposable state removed"
    return report


def verify_screenshot_projection(binary, script):
    """Normal CLI Test/Select -> Run -> explicit image -> actual Pi HTTP.

    Uses the existing Computer fixture for Guest mechanics; no database edits
    can inject capabilities into the profile or its frozen snapshot.
    """
    from verify_computer_use import launch
    server = ThreadingHTTPServer(("127.0.0.1", 0), Endpoint)
    server.requests, server.images, server.mode = [], [], "screenshots"
    server.daemon_threads = True
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    report = {"checks": [], "cleanup": False}
    try:
        for enabled in (True, False):
            server.images.clear()
            with tempfile.TemporaryDirectory(prefix="steward-image-profile-") as temporary:
                with launch(binary, Path(temporary).resolve()) as h:
                    config = json.loads((h.root / "config.yaml").read_text())
                    config["llm"] = {"provider_bridge_command": "node", "provider_bridge_script": str(script), "provider_bridge_version": "pi-ai-1.3.0-v1"}
                    (h.root / "config.yaml").write_text(json.dumps(config))
                    h.args = lambda *a: [str(h.binary), "-config", str(h.root / "config.yaml"), *a]
                    metadata = {"name": "Screenshot profile", "provider_kind": "openai_compatible", "endpoint": f"http://127.0.0.1:{server.server_port}/v1", "wire_api": "openai_completions", "model_id": "fixture-model", "context_window": 65536, "max_output_tokens": 4096, "compatibility": {"tool_result_format": "openai"}, "capabilities": {"text_input": True, "image_input": enabled}}
                    profile = h.setup(metadata=metadata)
                    h.send("screenshot-profile", "Observe two screenshots; explicitly select only the second image.")
                    h.command("chat", "work")
                    with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db:
                        requests = [json.loads(r[0]) for r in db.execute("SELECT g.request_json FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='worker' ORDER BY g.created_at")]
                        main_requests = [json.loads(r[0]) for r in db.execute("SELECT g.request_json FROM model_generation g JOIN run r USING(run_id) WHERE r.kind='main' ORDER BY g.created_at")]
                        require(main_requests and all({t['name'] for t in r['tools']} == MAIN_DIRECT for r in main_requests), "Main model bypassed Code Mode")
                        main_frozen = [json.loads(row[0]) for row in db.execute("SELECT tools_json FROM capability_snapshot WHERE role='main'")]
                        require(all(not any(s['name'].startswith('builtin__computer_') or s['name'] in ['builtin__read','builtin__bash','builtin__edit','builtin__write'] for s in specs) for specs in main_frozen), "Main obtained screenshot execution authority")
                        require(len(requests) == 3, "screenshot Worker exchanges")
                        require(all(r["model_snapshot"]["capabilities"]["image_input"] == enabled for r in requests), "normally selected image capability lost or gained")
                        selected = [sum(len(m.get("image_refs", [])) for m in r["messages"]) for r in requests]
                        require(selected == [0, 0, 1], "explicit screenshot selection not persisted")
                        require("base64" not in json.dumps(requests), "pixels entered durable generation requests")
                    require(server.images == ([0, 0, 1] if enabled else [0, 0, 0]), "selected screenshot pixels missing or text-only fallback leaked pixels")
                    report["checks"].append({"image_input": enabled, "selected_refs": selected, "provider_image_counts": list(server.images)})
                    if enabled:
                        server.mode = "reject-images"
                        failed = h.command("model", "test", profile, ok=False)
                        require("MODEL_INCOMPATIBLE" in failed.stderr, "failed image probe admitted")
                        require(h.command("status")["readiness"] == "degraded", "failed image retest left ingress open")
                        server.mode = "screenshots"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=3)
    report["cleanup"] = True
    return report


def verify_user_images(binary, script, evidence):
    """Actual Web/CLI inputs -> scoped Artifact -> frozen request -> Pi HTTP."""
    from verify_channels import ChannelHarness, Sandbox
    from event_broker import Broker
    report = {'checks': [], 'scenarios': {}}
    def png_chunk(kind, data):
        return struct.pack('>I',len(data))+kind+data+struct.pack('>I',zlib.crc32(kind+data)&0xffffffff)
    picture = b'\x89PNG\r\n\x1a\n'+png_chunk(b'IHDR',struct.pack('>IIBBBBB',2,2,8,2,0,0,0))
    picture += png_chunk(b'IDAT',zlib.compress((b'\0'+b'\xff\0\0'*2)*2))+png_chunk(b'IEND',b'')
    server = ThreadingHTTPServer(('127.0.0.1',0),Endpoint)
    server.daemon_threads = True
    server.requests,server.images,server.mode,server.hold_images = [],[],'user-images',False
    server.received_image,server.release_image = threading.Event(),threading.Event()
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    try:
        for mode in ('vision','text','recovery'):
            with tempfile.TemporaryDirectory(prefix='steward-user-image-') as temp, Sandbox() as platform, Broker(Path(temp)) as broker:
                h=ChannelHarness(binary,Path(temp),platform)
                try:
                    h.config.write_text(h.config.read_text()+'llm:\n  provider_bridge_command: node\n  provider_bridge_script: '+json.dumps(str(script))+'\n  provider_bridge_version: pi-ai-1.3.0-v1\nnats:\n  url: '+json.dumps(broker.url)+'\n')
                    if '\njavascript:' not in h.config.read_text():
                        h.config.write_text(h.config.read_text()+runtime_yaml())
                    h.args=lambda *a:[str(binary),'-config',str(h.config),*a]
                    metadata={'name':'User images','provider_kind':'openai_compatible','endpoint':f'http://127.0.0.1:{server.server_port}/v1',
                              'wire_api':'openai_completions','model_id':'fixture-model','context_window':65536,'max_output_tokens':4096,
                              'compatibility':{'tool_result_format':'openai'},'capabilities':{'text_input':True,'image_input':mode!='text'}}
                    h.setup(metadata=metadata)
                    start=len(server.images)
                    if mode!='recovery':
                        h.env['STEWARD_TEST_PICTURE']=base64.b64encode(picture).decode()
                        h.env['STEWARD_TEST_VISION']='yes' if mode=='vision' else 'no'
                        h.start()
                        h.wait_state(lambda status:status['readiness']=='ready','image runtime readiness',lambda _:h.http('/api/status'))
                        h.browser(evidence.parent/('user-image-'+mode),'image');h.stop()
                    else:
                        run=h.processes.run if h.processes is not None else subprocess.run
                        saved=run(h.args('artifact','save','user-picture.png','image/png'),input=picture,capture_output=True,env=h.env,cwd=h.root,timeout=20)
                        require(saved.returncode==0,'CLI image save failed')
                        ref=json.loads(saved.stdout)['artifact_ref']
                        h.command('chat','send-parts','user-image-recovery',text=json.dumps([{'type':'text','text':'USER_IMAGE_INPUT: inspect this upload.'},{'type':'image','artifact_ref':ref}]))
                        server.hold_images=True
                        start_process=h.processes.start if h.processes is not None else subprocess.Popen
                        child=start_process(h.args('chat','work'),stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=h.env,cwd=h.root)
                        h.children.append(child)
                        require(server.received_image.wait(15),'CLI image never reached actual provider')
                        child.kill();child.communicate(timeout=10)
                        with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                            frozen=db.execute('SELECT generation_id,request_hash,request_json FROM model_generation').fetchone()
                        server.hold_images=False;server.release_image.set()
                        h.command('chat','work')
                        with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                            recovered=db.execute('SELECT generation_id,request_hash,request_json FROM model_generation').fetchone()
                        require(recovered==frozen,'image recovery changed the accepted generation or hash')
                    with closing(sqlite3.connect(h.root/'data/state.db')) as db:
                        requests=[json.loads(r[0]) for r in db.execute('SELECT request_json FROM model_generation ORDER BY created_at')]
                        require(requests and all(sum(len(m.get('image_refs',[])) for m in r['messages'])==1 for r in requests),'user input image reference missing')
                        require('base64' not in json.dumps(requests),'pixels entered durable request')
                        if mode=='text':require('Image pixels are unavailable:' in json.dumps(requests),'text-only fallback was not explicit')
                    captured=server.images[start:]
                    require(captured and all(len(p)==(0 if mode=='text' else 1) for p in captured),'wrong actual provider pixel selection')
                    if mode=='recovery':require(len(captured)==2 and captured[0]==captured[1],'recovery changed selected pixels')
                    report['scenarios'][mode]={'provider_image_hashes':captured,'requests':requests}
                finally:
                    server.hold_images=False;server.release_image.set();h.cleanup()
        report['checks']=['Authenticated browser uploads reach actual Pi HTTP with scoped pixels; text-only profiles send no pixels and state the limitation',
                          'CLI save/send-parts followed by SIGKILL reuses the frozen generation id, hash and selected image bytes']
    finally:
        server.release_image.set();server.shutdown();server.server_close();thread.join(timeout=3)
    report['cleanup']=True
    return report


def verify_live_probe(binary, script, endpoint, model, context_tokens, output_tokens):
    """Opt-in local Live Shadow. Never selects a profile or opens user state."""
    from urllib.parse import urlsplit
    url = urlsplit(endpoint)
    require(url.scheme in ("http", "https") and url.hostname in ("127.0.0.1", "localhost", "::1")
            and not (url.username or url.password or url.query or url.fragment), "live probe requires a local endpoint without credentials")
    with tempfile.TemporaryDirectory(prefix="steward-live-probe-") as directory:
        root = Path(directory)
        env = {k: v for k, v in os.environ.items() if not k.startswith("STEWARD_")}
        env["STEWARD_DATA_ROOT"] = str(root / "data")
        config = root / "config.yaml"
        config.write_text("version: 1\nllm:\n  provider_bridge_command: node\n  provider_bridge_script: " + json.dumps(str(script)) + "\n  provider_bridge_version: pi-ai-1.3.0-v1\n")
        if '\njavascript:' not in config.read_text():
            config.write_text(config.read_text()+runtime_yaml())

        def command(*args, text=None):
            return subprocess.run([str(binary), "-config", str(config), *args], cwd=root, env=env,
                                  input=text, text=True, capture_output=True, timeout=800)

        require(json.loads(command("status").stdout)["readiness"] == "setup_required", "live probe state isolation failed")
        catalog = command("model", "discover", endpoint)
        require(catalog.returncode == 0 and any(m["model_id"] == model for m in json.loads(catalog.stdout)), "live model not discovered")
        profile = command("model", "add", text=json.dumps({"name": model, "provider_kind": "openai_compatible",
            "endpoint": endpoint, "wire_api": "openai_completions", "model_id": model,
            "context_window": context_tokens, "max_output_tokens": output_tokens,
            "compatibility": {"tool_result_format": "openai"}, "capabilities": {"text_input": True}}))
        require(profile.returncode == 0, "live profile metadata rejected")
        started = time.monotonic()
        probe = command("model", "test", json.loads(profile.stdout)["profile_id"])
        elapsed = time.monotonic() - started
        # Only normalized Host errors/proof and content-free usage; never model output.
        with closing(sqlite3.connect(root / "data/state.db")) as db:
            usage = [dict(zip(("latency_ms", "error_code"), row)) for row in db.execute("SELECT latency_ms,error_code FROM provider_usage ORDER BY rowid")]
            require(db.execute("SELECT count(*) FROM run").fetchone()[0] == 0, "probe created Agent work")
        report = {"model": model, "context_tokens": context_tokens, "output_tokens": output_tokens,
                  "catalog": "discovered", "elapsed_seconds": round(elapsed, 3), "exit_code": probe.returncode,
                  "proof": json.loads(probe.stdout) if probe.returncode == 0 else None,
                  "error": probe.stderr.strip() if probe.returncode else None, "usage": usage,
                  "readiness": json.loads(command("status").stdout)["readiness"]}
    report["cleanup"] = "CLI children reaped; isolated state removed; no active profile selected"
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="bin/steward")
    parser.add_argument("--evidence", default="evidence/local-provider.json")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--live-endpoint', help='opt-in local Live Shadow; isolated Probe only')
    parser.add_argument('--live-model')
    parser.add_argument('--context-tokens', type=int, default=262144)
    parser.add_argument('--output-tokens', type=int, default=32768)
    args = parser.parse_args()
    Path(args.evidence).unlink(missing_ok=True)
    if args.live_endpoint:
        require(args.live_model, '--live-model is required')
        result = verify_live_probe(Path(args.binary).resolve(), Path('provider-bridge/bridge.mjs').resolve(),
                                   args.live_endpoint, args.live_model, args.context_tokens, args.output_tokens)
    else:
        result = verify(Path(args.binary).resolve(), Path('provider-bridge/bridge.mjs').resolve())
        result['screenshot_projection'] = verify_screenshot_projection(Path(args.binary).resolve(), Path('provider-bridge/bridge.mjs').resolve())
        result['user_images'] = verify_user_images(Path(args.binary).resolve(), Path('provider-bridge/bridge.mjs').resolve(),Path(args.evidence))
    Path(args.evidence).parent.mkdir(parents=True, exist_ok=True)
    Path(args.evidence).write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"checks": len(result.get("checks", [])), "usage_rows": len(result["usage"]), "cleanup": result["cleanup"]}))
