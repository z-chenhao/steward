from codemode_peer import project as project_codemode, wrap as wrap_codemode
"""Shared offline model HTTP peer for deterministic system scenarios.

Owns only the HTTP protocol; gate launch, evidence and cleanup stay with callers.
"""
import base64
import hashlib
from http.server import BaseHTTPRequestHandler
import json
import time

from verify import require


class Endpoint(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.server.requests.append({"method": "GET", "auth": self.headers.get("Authorization")})
        if self.server.mode == "manual":
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        data = json.dumps({"data": [{"id": "fixture-model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.requests.append({"method": "POST", "auth": self.headers.get("Authorization"),
                                     "session": self.headers.get("session_id"), "body": body})
        if self.server.mode == "auth":
            self.send_response(401)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if self.server.mode == "slow" and body["messages"][-1].get("content") == "Reply with OK.":
            # Actual cold-start regression: longer than the former entire Probe deadline.
            time.sleep(11)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Connection", "close")
        self.end_headers()

        body, code_only = project_codemode(body)
        if self.server.mode == "intelligent-ui-stream":
            code_only = False # Exercise the actual direct declarations and raw JSON deltas.

        def chunk(delta, finish=None, usage=None):
            if code_only:
                delta = wrap_codemode(delta)
            value = {"id": "fixture", "object": "chat.completion.chunk", "model": body["model"],
                     "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
            if usage:
                value["usage"] = usage
            self.wfile.write(("data: " + json.dumps(value) + "\n\n").encode())
            self.wfile.flush()

        try:
            text = str(body["messages"][-1].get("content", ""))
            images = sum(p.get("type") == "image_url" for m in body["messages"]
                         if isinstance(m.get("content"), list) for p in m["content"])
            if "until cancelled" in text:
                for _ in range(100):
                    chunk({"content": "streaming "})
                    time.sleep(.01)
            elif "dominant color of this image" in text:
                chunk({"content": "blue" if images == 1 and self.server.mode != "reject-images" else "unseen"})
                chunk({}, "stop")
            elif self.server.mode == "user-images" and "USER_IMAGE_INPUT" in text:
                payloads = [p['image_url']['url'] for m in body['messages'] if isinstance(m.get('content'),list)
                            for p in m['content'] if p.get('type') == 'image_url']
                hashes = []
                for value in payloads:
                    require(value.startswith('data:image/jpeg;base64,'),'user image did not enter the provider envelope')
                    data = base64.b64decode(value.split(',',1)[1],validate=True)
                    require(data.startswith(b'\xff\xd8\xff') and len(data)>100,'provider did not receive actual JPEG bytes')
                    hashes.append(hashlib.sha256(data).hexdigest())
                self.server.images.append(hashes)
                if self.server.hold_images:
                    self.server.received_image.set()
                    require(self.server.release_image.wait(15),'image recovery peer was not released')
                chunk({'content':'USER_IMAGE_PIXELS_RECEIVED' if images else 'USER_IMAGE_REFERENCE_ONLY'})
                chunk({},'stop')
            elif self.server.mode == "ocr" and any("OCR_INPUT" in str(m.get("content", "")) for m in body["messages"]):
                require(images == 0, "text-only OCR unexpectedly sent image pixels to the model")
                marker = max(i for i, m in enumerate(body["messages"]) if "OCR_INPUT" in str(m.get("content", "")))
                outputs = [m for m in body["messages"][marker+1:] if m["role"] == "tool"]
                if not outputs:
                    chunk({"tool_calls": [{"index": 0, "id": "read-image-text", "type": "function", "function": {
                        "name": "builtin__image_ocr", "arguments": json.dumps({"artifactRef": self.server.ocr_ref})}}]})
                    chunk({}, "tool_calls")
                else:
                    require("OCR 12345" in str(outputs[-1]["content"]), "actual OCR text did not reach the model")
                    self.server.received_ocr.set()
                    if self.server.hold_ocr:
                        require(self.server.release_ocr.wait(15), "OCR recovery peer was not released")
                    chunk({"content": "OCR_TEXT_RECEIVED"})
                    chunk({}, "stop")
            elif self.server.mode == "screenshots" and any(t["function"]["name"] == "builtin__codemode_execute" for t in body.get("tools", [])):
                self.server.images.append(images)
                count = sum(m["role"] == "tool" for m in body["messages"])
                if count < 2:
                    source = 'const app=await cua.getApp("com.apple.TextEdit");const shot=await app.getScreenshot();'
                    source += 'image(shot.image);' if count == 1 else 'text("Observed without selecting pixels.");'
                    chunk({"tool_calls": [{"index": 0, "id": f"screenshot-{count}", "type": "function", "function": {"name": "builtin__codemode_execute", "arguments": json.dumps({"source": source})}}]})
                    chunk({}, "tool_calls")
                else:
                    chunk({"content": "Screenshot selection verified."})
                    chunk({}, "stop")
            elif self.server.mode == "screenshots" and any(t["function"]["name"] == "builtin__task_manage" for t in body.get("tools", [])):
                runtime = any("Runtime task update (untrusted Worker data" in str(m.get("content", "")) for m in body["messages"])
                if not runtime and not any(m["role"] == "tool" for m in body["messages"]):
                    arguments = {"operation": "create", "task": "Observe two screenshots; explicitly select only the second image."}
                    chunk({"tool_calls": [{"index": 0, "id": "screenshot-task", "type": "function", "function": {"name": "builtin__task_manage", "arguments": json.dumps(arguments)}}]})
                    chunk({}, "tool_calls")
                else:
                    chunk({"content": "Screenshot Task observed."})
                    chunk({}, "stop")
            elif self.server.mode == "intelligent-ui-stream":
                mode = self.server.preview_path
                if body['messages'][-1]['role'] != 'tool':
                    if mode == 'direct':
                        remainders = []
                        for index, widget in enumerate(self.server.stream_widgets):
                            raw = json.dumps(widget, ensure_ascii=False)
                            marker = ', ["B"' if index == 0 else 'Script stays off'
                            cut = raw.index(marker) if index == 0 else raw.index(marker) + len(marker)
                            remainders.append(raw[cut:])
                            chunk({'tool_calls':[dict(index=index,id=f'stream-direct-{index}',type='function',function=dict(name='builtin__intelligent_ui_show',arguments=raw[:cut]))]})
                        self.server.preview_ready.set()
                        require(self.server.preview_advance.wait(35), 'incremental UI preview was never requested')
                        tail = remainders[1]
                        boundary = tail.index(', "jsFunctions"')
                        chunk({'tool_calls':[dict(index=1,function=dict(arguments=tail[:boundary]))]})
                        remainders[1] = tail[boundary:]
                        require(self.server.preview_release.wait(40), 'direct UI preview was never released')
                        for index, tail in enumerate(remainders):
                            chunk({'tool_calls':[dict(index=index,function=dict(arguments=tail))]})
                    else:
                        source = '\n'.join('await tools.builtin__intelligent_ui_show('+json.dumps(widget)+');' for widget in self.server.stream_widgets)
                        chunk({'tool_calls':[dict(index=0,id='stream-code',type='function',function=dict(name='builtin__codemode_execute',arguments=json.dumps(dict(source=source))))]})
                    chunk({},'tool_calls')
                else:
                    if mode == 'code':
                        self.server.preview_ready.set()
                        require(self.server.preview_release.wait(40), 'Code Mode UI preview was never released')
                    chunk({'content': 'Streamed '+mode+' answer.'})
                    chunk({},'stop')
            elif self.server.mode == "intelligent-ui":
                has_tools = any(m['role'] == 'tool' for m in body['messages'])
                names = [c.get('function',{}).get('name') for m in body['messages'] for c in m.get('tool_calls',[])]
                has_search = 'builtin__skill_list' in names
                has_view = 'builtin__skill_view' in names
                has_widget = 'builtin__intelligent_ui_show' in names
                if not has_tools and 'SHOW_INTELLIGENT_UI' in text:
                    system = '\n'.join(m.get('content','') for m in body['messages'] if m['role']=='system')
                    require('<skills>' not in system and '<skill_index>' not in system and
                            'Present a compact native component' not in system,
                            'Skill catalog was preloaded into system')
                    chunk({'tool_calls':[dict(index=0,id='ui-search',type='function',function=dict(name='builtin__skill_list',arguments=json.dumps(dict(query='interactive visual',limit=1))))]})
                    chunk({},'tool_calls')
                elif has_search and not has_view:
                    listing = json.loads(next(m['content'] for m in reversed(body['messages']) if m['role']=='tool'))
                    require(len(listing)==1 and set(listing[0])=={'name','description'} and listing[0]['name']=='intelligent_ui',
                            'Skill search did not return the selected method without internal metadata')
                    chunk({'tool_calls':[dict(index=0,id='ui-skill',type='function',function=dict(name='builtin__skill_view',arguments=json.dumps(dict(name=listing[0]['name']))))]})
                    chunk({},'tool_calls')
                elif has_tools and not has_widget:
                    chunk({'tool_calls': [dict(index=i,id='widget-'+str(i),type='function',function=dict(name='builtin__intelligent_ui_show',arguments=json.dumps(args))) for i,args in enumerate(self.server.widgets)]})
                    chunk({},'tool_calls')
                else:
                    chunk({'content':'The interactive answer and its text alternatives are ready.'})
                    chunk({},'stop',{'prompt_tokens':7,'completion_tokens':8,'total_tokens':15})
            elif self.server.mode == "ui-interactions":
                latest = next((str(m.get('content','')) for m in reversed(body['messages']) if m['role']=='user' and not str(m.get('content','')).startswith('Runtime ')), '')
                if latest == 'Refresh catalog' and body['messages'][-1]['role'] != 'tool':
                    code_only = False  # tool search is a declared model-only direct tool.
                    request = next(m['content'] for m in reversed(body['messages']) if str(m.get('content','')).startswith('Runtime ui_action_request'))
                    requested_args = json.loads(request.split('\n',1)[1])['content']['arguments']
                    chunk({'tool_calls':[dict(index=0,id='refresh-catalog',type='function',function=dict(name='builtin__tool_search',arguments=json.dumps(requested_args)))]});chunk({},'tool_calls')
                elif latest == 'SHOW_UI_INTERACTIONS' and body['messages'][-1]['role'] != 'tool':
                    chunk({'tool_calls':[dict(index=0,id='ui-extensions',type='function',function=dict(name='builtin__intelligent_ui_show',arguments=json.dumps(self.server.widgets[0]))) ]});chunk({},'tool_calls')
                else:
                    chunk({'content':'Updated.'});chunk({},'stop')
            elif self.server.mode == "tasks":
                names = [item["function"]["name"] for item in body.get("tools", [])]
                root = any("general Worker executing" in str(m.get("content", "")) for m in body["messages"])
                runtime = any("Runtime task update (untrusted Worker data" in str(m.get("content", "")) for m in body["messages"])
                has_result = any(m["role"] == "tool" for m in body["messages"])
                if root and not has_result:
                    time.sleep(getattr(self.server, 'task_root_delay', 0))
                if runtime and not root:
                    chunk({"content": getattr(self.server, 'task_followup', "Main Task follow-up through Pi.")})
                elif not has_result and (root or "builtin__task_manage" in names):
                    name = getattr(self.server, 'task_root_tool', "builtin__artifact_manage" if code_only else "builtin__artifact_search") if root else "builtin__task_manage"
                    root_arguments = dict(operation='save',name='task-fixture.txt',mediaType='text/plain',text='Synthetic Task evidence.') if code_only else {}
                    arguments = getattr(self.server, 'task_root_arguments', root_arguments) if root else {"operation": "create", "task": "Prepare a safe independent report. Return evidence; perform no external actions."}
                    if not root and getattr(self.server, 'task_title', ''):
                        arguments.update(title=self.server.task_title, task=self.server.task_description)
                    chunk({"tool_calls": [{"index": 0, "id": "task_call", "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}]})
                else:
                    chunk({"content": "Root business result through Pi." if root else "Main Task accepted through Pi."})
                chunk({}, "stop", {"prompt_tokens": 7, "completion_tokens": 8, "total_tokens": 15})
            elif self.server.mode == "memory-disabled":
                if not any(m["role"] == "tool" for m in body["messages"]):
                    requests = [("builtin__memory_manage", {"operation": "remember", "content": "I prefer tea"}),
                                ("builtin__memory_search", {"query": "tea"})]
                    chunk({"tool_calls": [{"index": i, "id": f"disabled-{i}", "type": "function",
                                           "function": {"name": name, "arguments": json.dumps(arguments)}}
                                          for i, (name, arguments) in enumerate(requests)]})
                    chunk({}, "tool_calls")
                else:
                    chunk({"content": "Memory requires configuration."})
                    chunk({}, "stop")
            elif self.server.mode == "partial":
                chunk({"content": "partial"})
                return
            elif self.server.mode == "web-stream":
                def await_browser(name):
                    deadline = time.monotonic() + 15
                    while not (self.server.stream_gates / name).exists():
                        require(time.monotonic() < deadline, 'browser did not observe stream before releasing ' + name)
                        time.sleep(.02)

                if "STREAM_FAILURE" in text:
                    chunk({"content": "Interrupted draft only."})
                    await_browser('fail')
                    return
                require("STREAM_SUCCESS" in text, 'unexpected streaming test input')
                chunk({"content": "First chunk <img src=x onerror=unsafe()>\n\n**增长趋势：**A在增长。\n\n**正在解释"})
                await_browser('second')
                chunk({"content": "，继续说明**\n\nSecond chunk 中文."})
                await_browser('finish')
                chunk({"content": "\nFinal tail.\n"})
                chunk({}, "stop")
            elif body.get("tools") and not any(m["role"] == "tool" for m in body["messages"]):
                if self.server.mode == "no-tools":
                    chunk({"content": "I cannot call tools"})
                    chunk({}, "stop")
                else:
                    names = [item["function"]["name"] for item in body["tools"]]
                    name = "builtin__artifact_search" if "builtin__artifact_search" in names else "builtin__artifact_manage" if "builtin__artifact_manage" in names else names[0]
                    # Exercise a valid Run-scoped operation without adding an
                    # Artifact that separately schedules background extraction.
                    arguments = {} if name == "builtin__artifact_search" else dict(operation='publish',artifactRefs=[]) if name == "builtin__artifact_manage" else {"value": "OK"}
                    chunk({"tool_calls": [{"index": 0, "id": "call_echo", "type": "function",
                                          "function": {"name": name, "arguments": json.dumps(arguments)}}]})
                    chunk({}, "tool_calls")
            else:
                chunk({"content": "PROBE_RESULT_OK" if any(m["role"] == "tool" for m in body["messages"])
                       else "ABCDEFGHIJKLMNOPQRSTUVWXYZ" if "long output" in text else "OK"})
                chunk({}, "stop", {"prompt_tokens": 7, "completion_tokens": 8, "total_tokens": 15})
            self.wfile.write(b"data: [DONE]\n\n")
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            self.close_connection = True
