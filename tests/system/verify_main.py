#!/usr/bin/env python3
"""Launch, doctor, drive, read back and clean up the actual Main CLI.

No online services. SIGKILL points are opt-in Host hooks, reached by the same
Actor/Loop/Tool/Conversation path as an ordinary configured provider.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
from contextlib import closing
import sqlite3
import subprocess
import tempfile
import time

from fixture_pool import Scope, current


def require(value, message):
    if not value:
        raise RuntimeError(message)


class MainHarness:
    def __init__(self, binary, root, fixture="main"):
        self.binary = binary
        self.root = root
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("STEWARD_")}
        self.env["STEWARD_DATA_ROOT"] = str(root / "data")
        self.children = []
        self.fixture = fixture
        self.processes = current()
        self.hold_observation = None

    def args(self, *args):
        return [str(self.binary), "-test-bridge", self.fixture, *args]

    def command(self, *args, text=None, ok=True, timeout=20):
        run = self.processes.run if self.processes is not None else subprocess.run
        result = run(self.args(*args), input=text, text=True, capture_output=True,
                                env=self.env, cwd=self.root, timeout=timeout)
        require(not ok or result.returncode == 0, f"{args}: {result.stderr}")
        return json.loads(result.stdout) if ok else result

    def setup(self, model="fixture-model", metadata=None):
        status = self.command("status")
        profile = (self.command("model", "add", text=json.dumps(metadata)) if metadata is not None else
                   self.command("model", "add", "http://127.0.0.1:1/v1", model))
        self.command("model", "test", profile["profile_id"])
        status = self.command("model", "select", profile["profile_id"], str(profile["version"]),
                              str(status["config_version"]))
        require(status["agent_ingress_enabled"], "doctor: selected fixture not ready")
        return profile["profile_id"]

    def send(self, key, text):
        return self.command("chat", "send", key, text=text)

    def wait_state(self, predicate, description, reader=None, timeout=10):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.processes is not None:
                self.processes.check()
            value = reader(self) if reader else self.state()
            if predicate(value):
                return value
            time.sleep(0.025)
        raise RuntimeError("state boundary not reached: " + description)

    def hold(self, boundary, extra_flags=(), timeout=15, command=("chat", "work"), input_text=None):
        started = time.monotonic()
        start = self.processes.start if self.processes is not None else subprocess.Popen
        child = start(self.args("-test-main-hold", boundary, *extra_flags, *command),
                                 stdin=subprocess.PIPE if input_text is not None else None,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                 env=self.env, cwd=self.root)
        self.children.append(child)
        if input_text is not None:
            child.stdin.write(input_text)
            child.stdin.close()
            child.stdin = None
        self.hold_observation = dict(boundary=boundary, timeout_seconds=timeout,
                                     pid=child.pid, reached=False)
        output, errors, pending = bytearray(), bytearray(), bytearray()
        counts = dict(stdout=0, stderr=0)
        deadline = started + timeout
        reason = 'waiting'
        try:
            with selectors.DefaultSelector() as streams:
                streams.register(child.stdout, selectors.EVENT_READ)
                streams.register(child.stderr, selectors.EVENT_READ)
                while streams.get_map():
                    if self.processes is not None:
                        self.processes.check()
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        reason = 'timeout'
                        raise RuntimeError(f"boundary never reached: {boundary}")
                    events = streams.select(min(.025, remaining))
                    for key, _ in sorted(events, key=lambda event: event[0].fileobj is child.stdout):
                        name = 'stdout' if key.fileobj is child.stdout else 'stderr'
                        chunk = os.read(key.fileobj.fileno(), 65536)
                        if not chunk:
                            streams.unregister(key.fileobj)
                            if name == 'stdout':
                                reason = 'stdout_closed'
                                raise RuntimeError(f"exited before boundary {boundary}")
                            continue
                        counts[name] += len(chunk)
                        tail = output if name == 'stdout' else errors
                        tail.extend(chunk)
                        del tail[:-8000]
                        if name == 'stderr':
                            continue
                        pending.extend(chunk)
                        reason = 'invalid_record'
                        require(len(pending) <= 8000, f"oversized boundary record: {boundary}")
                        if b'\n' in pending:
                            line, _, _ = pending.partition(b'\n')
                            value = json.loads(line)
                            require(isinstance(value, dict) and value.get('boundary') == boundary,
                                    f"unexpected boundary record: {boundary}")
                            reason = 'reached'
                            self.hold_observation['reached'] = True
                            return child
                        reason = 'waiting'
                reason = 'streams_closed'
                raise RuntimeError(f"exited before boundary {boundary}")
        finally:
            # Never readline()/read() an incomplete record or an unclosed pipe.
            # The owning fixture still joins the process through cleanup().
            self.hold_observation.update(reason=reason, returncode=child.poll(),
                elapsed_seconds=time.monotonic() - started, bytes_read=counts,
                stdout=output.decode(errors='replace'), stderr=errors.decode(errors='replace'))
            if reason != 'reached':
                try:
                    self.hold_observation['process_usage'] = subprocess.check_output(
                        ['ps', '-p', str(child.pid), '-o', 'pid=,ppid=,stat=,time=,pcpu='], text=True, timeout=.5)
                except (OSError, subprocess.SubprocessError) as error:
                    self.hold_observation['process_usage_error_type'] = type(error).__name__

    def drive_boundaries(self, barriers, observe, progress, timeout=20):
        """Drive one CLI call with independent, explicitly released test barriers.

        Both pipes are drained while callbacks observe committed state. The
        original command deadline and owned-session cleanup remain in force.
        """
        flags = [part for name, path in barriers.items()
                 for part in ('-test-main-barrier', name + '=' + str(path))]
        owner = self.processes or Scope()
        child = owner.start(self.args(*flags, 'chat', 'work'), stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, env=self.env, cwd=self.root)
        self.children.append(child)
        output, errors, pending = bytearray(), bytearray(), bytearray()
        deadline = time.monotonic() + timeout
        try:
            with selectors.DefaultSelector() as streams:
                streams.register(child.stdout, selectors.EVENT_READ)
                streams.register(child.stderr, selectors.EVENT_READ)
                while streams.get_map():
                    owner.check()
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise subprocess.TimeoutExpired(child.args, timeout)
                    for key, _ in streams.select(min(.025, remaining)):
                        chunk = os.read(key.fileobj.fileno(), 65536)
                        if not chunk:
                            streams.unregister(key.fileobj)
                            continue
                        if key.fileobj is child.stderr:
                            errors.extend(chunk)
                            continue
                        output.extend(chunk)
                        pending.extend(chunk)
                        while b'\n' in pending:
                            line, _, rest = pending.partition(b'\n')
                            pending = bytearray(rest)
                            value = json.loads(line)
                            if 'boundary' in value:
                                observe(value['boundary'])
                    progress()
            child.wait(timeout=max(.001, deadline - time.monotonic()))
            require(not pending, 'incomplete CLI boundary record')
            return subprocess.CompletedProcess(child.args, child.returncode,
                                               output.decode(), errors.decode())
        except BaseException:
            owner.cancel()
            child.communicate(timeout=10)
            raise
        finally:
            owner.retire(child)
            if self.processes is None:
                owner.finish()

    def state(self):
        result = {}
        with closing(sqlite3.connect(Path(self.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
            db.row_factory = sqlite3.Row
            for name, query in {
                "runs": "SELECT run_id,kind,task_id,parent_run_id,root_run_id,task_epoch,depth,status,desired_state,model_snapshot_id,capability_snapshot_id,checkpoint_json,error_code,version FROM run ORDER BY created_at",
                "generations": "SELECT generation_id,run_id,step_index,request_hash,request_json,status,result_json,error_code,attempt_count FROM model_generation ORDER BY created_at",
                "tools": "SELECT invocation_id,run_id,provider_exchange_id,provider_call_id,tool_name,arguments_json,request_hash,replay_policy,status,result_json,delivery_seq,attempt_count FROM tool_invocation ORDER BY created_at,delivery_seq",
                "deliveries": "SELECT delivery_id,message_id,status,idempotency_key FROM delivery_request ORDER BY created_at",
                "usage": "SELECT u.generation_id,u.snapshot_id,u.error_code FROM provider_usage u JOIN model_generation g USING(generation_id) ORDER BY g.created_at",
                "snapshots": "SELECT * FROM model_snapshot ORDER BY created_at",
                "capabilities": "SELECT * FROM capability_snapshot ORDER BY created_at",
                "inputs": "SELECT sequence,request_key,message_json FROM main_input ORDER BY sequence",
                "trace": "SELECT run_id,sequence,event_json,occurred_at FROM run_trace ORDER BY run_id,sequence",
            }.items():
                result[name] = [dict(row) for row in db.execute(query)]
        result["conversation"] = self.history()
        return result

    def history(self):
        return self.command("chat", "history")

    def cleanup(self):
        for child in self.children:
            if child.poll() is None:
                child.kill()
            child.communicate(timeout=10)
            if self.processes is not None:
                self.processes.retire(child)

    def failure_state(self):
        """Bounded read-only evidence from disposable fixtures; no claim tokens."""
        path = Path(self.env['STEWARD_DATA_ROOT']) / 'state.db'
        if not path.exists():
            return {'database': 'not created'}
        with closing(sqlite3.connect(path)) as db, db:
            db.row_factory = sqlite3.Row
            queries = {
                'runs': 'SELECT run_id,kind,depth,status,desired_state,wait_reason,error_code FROM run ORDER BY created_at DESC LIMIT 40',
                'claims': 'SELECT run_id,epoch,token IS NOT NULL AS owned,expires_at FROM run_claim ORDER BY expires_at DESC LIMIT 40',
                'tools': 'SELECT invocation_id,tool_name,status,error_json,result_json FROM tool_invocation ORDER BY created_at DESC LIMIT 16',
                'models': 'SELECT generation_id,status,request_json,result_json FROM model_generation ORDER BY created_at DESC LIMIT 3',
                'code': 'SELECT execution_id,state,desired_state,execution_epoch,claim_expires_at FROM code_execution_state ORDER BY updated_at DESC LIMIT 8',
            }
            result = {name: [dict(row) for row in db.execute(query)] for name, query in queries.items()}
            if db.execute("SELECT 1 FROM sqlite_schema WHERE name='computer'").fetchone():
                result['computer'] = [dict(row) for row in db.execute('SELECT computer_id,instance_epoch,phase,execution_id,execution_epoch,version FROM computer')]
                result['computer_operations'] = [dict(row) for row in db.execute('SELECT execution_id,execution_epoch,kind,status FROM computer_operation ORDER BY rowid DESC LIMIT 12')]
            return result


def assert_final(state, users, replies=1):
    messages = state["conversation"]
    require(len(messages) == users + replies, "duplicate or missing semantic messages")
    require([m["sequence"] for m in messages] == list(range(1, len(messages) + 1)), "message order")
    require(len({m["message_id"] for m in messages}) == len(messages), "duplicate semantic IDs")
    require(sum(m["role"] == "user" for m in messages) == users, "user message lost")
    require(sum(m["role"] == "assistant" for m in messages) == replies, "assistant count")
    require(all(p["type"] == "text" for m in messages for p in m["content"]), "Tool trace leaked to history")
    require(all(r["status"] == "completed" for r in state["runs"]), "Run failed or stayed nonterminal")
    require(len(state["deliveries"]) == replies, "delivery intent duplicated/missing")
    require(all(g["status"] == "completed" for g in state["generations"]), "generation incomplete")
    require(len(state["usage"]) == len(state["generations"]), "completion and usage not atomic")
    require(all(t["status"] == "succeeded" for t in state["tools"]), "Tool failed/incomplete")


def run(binary):
    report = {"version": 1, "scope": "M1 Main slice; no M3 Security or M5 JavaScript replay claim",
              "checks": [], "crashes": []}
    with tempfile.TemporaryDirectory(prefix="steward-main-") as temp:
        root = Path(temp)

        def scenario(name, action):
            folder = root / name
            folder.mkdir()
            h = MainHarness(binary, folder)
            try:
                return action(h)
            finally:
                h.cleanup()

        def concurrent(h):
            rejected = h.command("chat", "send", "unconfigured", text="hello", ok=False)
            require(rejected.returncode != 0 and "setup_required" in rejected.stderr, "setup ingress opened")
            h.setup()
            with ThreadPoolExecutor(max_workers=8) as executor:
                receipts = list(executor.map(lambda i: h.send(f"input-{i}", f"message {i}"), range(16)))
            require(len({r["run_id"] for r in receipts}) == 1, "concurrent ingress created multiple Main Runs")
            require(h.send("input-0", "message 0") == receipts[0], "ingress retry not idempotent")
            unconfigured = subprocess.run([str(h.binary), "chat", "work"], text=True, capture_output=True,
                                          env=h.env, cwd=h.root, timeout=10)
            require(unconfigured.returncode != 0 and "setup_required" in unconfigured.stderr and "panic" not in unconfigured.stderr,
                    "restart without the explicitly configured Bridge must fail closed")
            drift = h.command("chat", "send", "input-0", text="changed", ok=False)
            require(drift.returncode != 0, "request drift accepted")
            h.command("chat", "work")
            result = h.state()
            assert_final(result, 16, 16)
            turns = [generation for generation in result["generations"] if json.loads(generation["request_json"]).get("tools")]
            require(len(turns) == 32 and len(result["tools"]) == 32, "wrong model/Tool path")
            expected = ["Processed: " + json.loads(item["message_json"])["content"][0]["text"] for item in result["inputs"]]
            replies = [part["text"] for message in result["conversation"] if message["role"] == "assistant" for part in message["content"]]
            require(replies == expected, "concurrent inputs did not receive individual ordered replies: " + repr(dict(expected=expected, actual=replies)))
            h.command("chat", "work")
            require(h.state() == result, "draining terminal work changed state")
            return result

        report["concurrent"] = scenario("concurrent", concurrent)
        report["checks"].append("16 simultaneous CLI inputs; one Main; ordered history; idempotency and drift rejection")

        def responsive(h, crash):
            h.setup()
            receipt = h.send("long", "F003:long")
            # Recover an actual pre-policy checkpoint: legacy Main retains its
            # frozen steering behavior even while a direct tool remains held.
            with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
                changed = db.execute("UPDATE run SET status='running',checkpoint_json=json_remove(checkpoint_json,'$.serial_inputs'),version=version+1 WHERE run_id=?", (receipt["run_id"],))
                require(changed.rowcount == 1, "legacy recovery fixture missing")
            release = h.root / "release-tool"
            seen, observed = set(), {}

            def observe(boundary):
                seen.add(boundary)
                if "sent_at" not in observed:
                    observed["sent_at"] = time.monotonic()
                    h.send("ping", "F003:ping")

            def progress():
                if "sent_at" not in observed or "response_seconds" in observed:
                    return
                state = h.state()
                if not any(m["role"] == "assistant" and any(
                        p.get("text") == "F003:pong" for p in m["content"])
                        for m in state["conversation"]):
                    return
                checkpoint = json.loads(state["runs"][0]["checkpoint_json"])
                if not checkpoint.get("pending_tools"):
                    # state.db is sampled before the separate history read;
                    # a newly committed reply can race that first snapshot.
                    return
                observed["response_seconds"] = time.monotonic() - observed["sent_at"]
                require(observed["response_seconds"] < 5, "foreground input did not respond while tool held")
                require(len(state["runs"]) == 1, "responsive input opened another Main")
                require(any(t["status"] == "running" for t in state["tools"]), "tool finished before foreground response")
                observed["before_release"] = state
                if crash:
                    h.children[-1].kill()
                else:
                    release.touch()

            result = h.drive_boundaries({"after_tool_execution_start": release}, observe, progress)
            require("after_tool_execution_start" in seen and "response_seconds" in observed,
                    "responsive scenario did not reach foreground response")
            require(result.returncode != 0 if crash else result.returncode == 0, result.stderr)
            if crash:
                h.command("chat", "work")
            state = h.state()
            assert_final(state, 2, 2)
            require(len(state["tools"]) == 2, "recovery created another logical Tool call")
            require(all(t["status"] == "succeeded" for t in state["tools"]), "deferred Tools did not settle")
            finals = [m for m in state["conversation"] if m["role"] == "assistant"]
            require(any("F003:finished" in p.get("text", "") for p in finals[-1]["content"]),
                    "original result was not delivered after foreground response")
            observed["after"] = state
            return observed

        report["responsive"] = scenario("responsive", lambda h: responsive(h, False))
        report["responsive_restart"] = scenario("responsive-restart", lambda h: responsive(h, True))
        report["checks"].append("legacy frozen Main: held Tool -> second CLI input -> reply before release; durable deferred results; SIGKILL and recovery")

        for boundary in ("after_compaction_commit", "before_generation_dispatch", "before_generation_commit", "after_generation_commit",
                         "before_conversation_append", "after_conversation_append", "before_reply_checkpoint",
                         "after_reply_checkpoint", "before_tool_commit", "after_tool_commit"):
            def crash(h):
                h.setup()
                h.send("one", "recover me")
                child = h.hold(boundary)
                child.kill()
                child.communicate(timeout=10)
                steered = boundary in ("after_compaction_commit", "after_generation_commit")
                if steered:
                    h.send("late", "late crash steering")
                frozen = None
                if boundary == "after_compaction_commit":
                    with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
                        frozen = db.execute("SELECT input_hash,messages_json FROM context_compaction WHERE step_index=0").fetchone()
                        require(frozen is not None and db.execute("SELECT count(*) FROM model_generation").fetchone()[0] == 0,
                                "kill did not separate compaction and generation admission")
                # Read state directly before recovery; history readback may
                # itself fill an immutable Conversation intent, by design.
                before = h.state()
                accepted = {g["generation_id"]: g for g in before["generations"] if g["status"] == "completed"}
                accepted_tools = {t["invocation_id"]: t for t in before["tools"] if t["status"] == "succeeded"}
                h.command("chat", "work")
                after = h.state()
                assert_final(after, 2 if steered else 1, 2 if steered else 1)
                require(len(after["generations"]) == (4 if steered else 2) and len(after["tools"]) == (4 if steered else 2), "crash repeated a logical generation/Tool")
                for g in after["generations"]:
                    if g["generation_id"] in accepted:
                        require(g == accepted[g["generation_id"]], "completed generation replayed or drifted")
                for t in after["tools"]:
                    if t["invocation_id"] in accepted_tools:
                        require(t == accepted_tools[t["invocation_id"]], "completed Tool replayed")
                if frozen is not None:
                    with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
                        require(db.execute("SELECT input_hash,messages_json FROM context_compaction WHERE step_index=0").fetchone() == frozen,
                                "steering changed the frozen pre-admission context")
                return {"boundary": boundary, "signal": "SIGKILL", "before": before, "after": after}
            report["crashes"].append(scenario(boundary, crash))

        def steering(h):
            old_profile = h.setup()
            first = h.send("first", "original")
            # Hold after the native tool batch is checkpointed. Holding the
            # generation instead lets the 200 ms responsive-tool window race
            # steering, legitimately changing model rounds and reply counts.
            child = h.hold("after_turn_end")
            before = h.state()
            checkpoint = json.loads(before["runs"][0]["checkpoint_json"])
            require(checkpoint["phase"] == "model" and checkpoint["step"] == 1 and
                    not checkpoint.get("pending_tools"), "steering did not hold the native tool boundary")
            require(len(before["generations"]) == 1 and len(before["tools"]) == 2 and
                    all(t["status"] == "succeeded" for t in before["tools"]) and
                    not before["deliveries"], "steering boundary has incomplete tools or a stale reply")
            for key, text in (("second", "steer one"), ("third", "steer two")):
                require(h.send(key, text)["run_id"] == first["run_id"], "steering spawned Main")
            new_profile = h.setup("second-model")
            child.send_signal(signal.SIGTERM)
            child.communicate(timeout=15)
            with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
                require(db.execute("SELECT status FROM run").fetchone()[0] == "running", "shutdown cancelled/lost business work")
            h.command("chat", "work")
            state = h.state()
            assert_final(state, 3, 3)
            require(len(state["generations"]) == 6, "queued inputs did not retain separate model turns")
            requests = [json.loads(g["request_json"]) for g in state["generations"]]
            require(all(r["model_snapshot"]["profile_id"] == old_profile for r in requests), "hot model switch")
            require([requests[i]["messages"][-1]["text"] for i in (2, 4)] == ["steer one", "steer two"], "queued inputs not separate/ordered")
            for index, reply in ((2, "Processed: original"), (4, "Processed: steer one")):
                require(any(m["role"] == "assistant" and m.get("text") == reply for m in requests[index]["messages"]), "later input overtook an uncommitted reply")
            h.send("fourth", "new run")
            h.command("chat", "work")
            final = h.state()
            assert_final(final, 4, 4)
            require(json.loads(final["generations"][-1]["request_json"])["model_snapshot"]["profile_id"] == new_profile, "new Run missed profile switch")
            return final
        report["steering"] = scenario("steering", steering)
        report["checks"].append("SIGTERM preserves business work; separate steering at stable boundary; frozen model and new-Run switch")
        report["checks"].append("SIGKILL at ten compaction/model/Tool/Conversation/checkpoint boundaries; frozen pre-admission context recovered before steering; completed results reused; missing writes filled")
    report["checks"].append("cleanup: all owned processes reaped and disposable databases removed")
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    args.evidence.unlink(missing_ok=True)
    report = run(args.binary.resolve())
    report["binary_sha256"] = hashlib.sha256(args.binary.read_bytes()).hexdigest()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"checks": report["checks"], "crash_boundaries": len(report["crashes"]), "evidence": str(args.evidence)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
