#!/usr/bin/env python3
"""Task launch/doctor/drive/evidence/cleanup through the existing CLI and PTY."""
import argparse
import hashlib
import json
from pathlib import Path
import signal
from contextlib import closing
import sqlite3
import tempfile

from terminal_driver import Terminal
from verify_main import MainHarness, require


def state(h):
    value = h.state()
    with closing(sqlite3.connect(Path(h.env["STEWARD_DATA_ROOT"]) / "state.db")) as db, db:
        db.row_factory = sqlite3.Row
        for key, query in {
            "tasks": "SELECT * FROM task ORDER BY created_at,task_id",
            "feedbacks": "SELECT * FROM task_feedback ORDER BY rowid",
            "instructions": "SELECT * FROM task_instruction ORDER BY sequence",
            "updates": "SELECT * FROM task_update ORDER BY occurred_at,event_id",
            "inbox": "SELECT * FROM main_inbox ORDER BY sequence",
            "reporter_dedup": "SELECT * FROM event_inbox WHERE consumer='task_reporter' ORDER BY event_id",
            "task_requests": "SELECT * FROM task_request ORDER BY invocation_id",
            "task_invocations": "SELECT * FROM task_invocation ORDER BY invocation_id",
            "worker_results": "SELECT * FROM worker_result ORDER BY created_at,exchange_id",
            "task_events": "SELECT event_id,type,payload_json,published_at FROM event_outbox WHERE type LIKE '%task%' ORDER BY occurred_at,event_id",
            "compactions": "SELECT run_id,step_index,messages_json,provenance_json FROM context_compaction ORDER BY run_id,step_index",
        }.items():
            value[key] = [dict(row) for row in db.execute(query)]
    return value


def frozen_generation_contracts(value):
    runs = {r['run_id']: r for r in value['runs']}
    snapshots = {s['snapshot_id'] for s in value['snapshots']}
    caps = {c['snapshot_id']: c for c in value['capabilities']}
    preparations = {(p['run_id'], p['step_index']): p for p in value['compactions']}
    models, schemas = {}, {}
    if not all(r['model_snapshot_id'] in snapshots and r['capability_snapshot_id'] in caps for r in runs.values()):
        return False
    for generation in value['generations']:
        request = json.loads(generation['request_json'])
        run = runs.get(request['run_id'])
        if not run or generation['run_id'] != run['run_id'] or request['model_snapshot']['snapshot_id'] != run['model_snapshot_id']:
            return False
        if models.setdefault(run['run_id'], request['model_snapshot']) != request['model_snapshot']:
            return False
        prepared = preparations.get((run['run_id'], generation['step_index']))
        if prepared and json.loads(prepared['messages_json']) != request['messages']:
            return False
        provenance = json.loads(prepared['provenance_json']) if prepared else {}
        tools = request.get('tools', [])
        if provenance.get('stage') == 'summary_request' and provenance.get('compaction'):
            # The same frozen model summarizes without declaring executable
            # tools. This is not a change to the Run's ordinary tool schema.
            if tools:
                return False
            continue
        frozen = json.loads(caps[run['capability_snapshot_id']]['tools_json'])
        if [(t['name'], t['input_schema']) for t in tools] != [(t['name'], t['input_schema']) for t in frozen if not t.get('internal')]:
            return False
        if schemas.setdefault(run['run_id'], tools) != tools:
            return False
    return True


def assertions(value, tasks, roots, updates):
    workers = [r for r in value["runs"] if r["kind"] == "worker"]
    replies = [m for m in value["conversation"] if m["role"] == "assistant"]
    requests = [json.loads(g["request_json"]) for g in value["generations"]]
    caps = {c["snapshot_id"]: c for c in value["capabilities"]}
    return {
        "task_count": len(value["tasks"]) == tasks,
        "root_count_and_no_child": len(workers) == roots and all(r["task_id"] and r["parent_run_id"] is None for r in workers),
        "one_completion_per_root": len(value["updates"]) == updates and len({u["root_run_id"] for u in value["updates"]}) == updates,
        "durable_report_dedup": len(value["inbox"]) == updates == len(value["reporter_dedup"]),
        "no_worker_task_controls": all(not any((s["name"].startswith("builtin__task_") or s["name"] == "builtin__automation_manage") for s in json.loads(caps[r["capability_snapshot_id"]]["tools_json"])) for r in workers),
        "main_cannot_spawn": all(not c["can_spawn_agents"] and not any(s["name"] == "builtin__agent_run" for s in json.loads(c["tools_json"])) for c in caps.values() if c["role"] == "main"),
        "runtime_origin_and_named_details": all(json.loads(b["message_json"])["origin"] == "runtime" and set(json.loads(b["message_json"])["details"]) <= {"task_ref", "kind", "artifact_refs", "title"} for b in value["inbox"]),
        "runtime_never_system": all(m["role"] != "system" for req in requests for m in req["messages"] if m.get("origin") == "runtime"),
        "worker_not_in_conversation": all(m["role"] in ("user", "assistant") and m["source"] != "runtime" for m in value["conversation"]),
        "frozen_snapshots_and_schemas": frozen_generation_contracts(value),
        "one_durable_delivery_per_main_reply": len(value["deliveries"]) == len(replies) == len({d["message_id"] for d in value["deliveries"]}),
        "atomic_provider_acceptance": len(value["usage"]) == len({g["generation_id"] for g in value["generations"] if g["error_code"] != "GENERATION_CANCELLED"}) and {u["generation_id"] for u in value["usage"]} == {g["generation_id"] for g in value["generations"] if g["error_code"] != "GENERATION_CANCELLED"},
        "ordered_semantic_history": [m["sequence"] for m in value["conversation"]] == list(range(1, len(value["conversation"]) + 1)),
        "no_failed_task_state": all(t["status"] in ("active", "paused", "completed", "cancelled") for t in value["tasks"]),
    }


def check(value, tasks=1, roots=1, updates=1):
    checks = assertions(value, tasks, roots, updates)
    require(all(checks.values()), "Task invariants: " + repr([k for k, v in checks.items() if not v]))
    return checks


def run(binary):
    report = {"version": 1, "scope": "Task/Root/Reporter implementation; controlled stop evidence is in task-control reports; Child, Artifact and delivery evidence is maintained in their dedicated harnesses", "checks": [], "crashes": []}
    with tempfile.TemporaryDirectory(prefix="steward-tasks-") as directory:
        root = Path(directory)

        def scenario(name, action):
            folder = root / name
            folder.mkdir()
            h = MainHarness(binary, folder, fixture="tasks")
            try:
                h.setup()
                return action(h)
            finally:
                h.cleanup()

        def terminal(h):
            with Terminal(h.args("tui"), h.env, h.root) as t:
                t.expect("State: ready"); t.expect("You> ")
                t.send("Delegate: Wedding checklist with venue dependencies.")
                t.expect("Main follow-up:"); t.expect("Steward: idle")
                t.send("Continue: Wedding | Use the new date.")
                t.expect("Main follow-up:"); t.expect("Steward: idle")
                t.send("/status"); t.expect("completed")
                require(t.stop() == 0, "terminal did not stop cleanly")
                transcript = t.transcript()
            value = state(h)
            check(value, roots=2, updates=2)
            require(value["tasks"][0]["task_epoch"] == 2 and len(value["instructions"]) == 1, "continue did not append/new epoch")
            return {"transcript": transcript, "state": value}

        report["terminal"] = scenario("terminal", terminal)
        report["checks"].append("PTY -> Main create/search/append -> independent Root -> durable Reporter -> Main reply; new epoch and immutable anchor")

        def ongoing(h):
            h.send("create", "Ongoing: Wedding checklist with venue dependencies.")
            h.command("chat", "work")
            first = state(h); check(first)
            require(first["tasks"][0]["status"] == "active" and first["tasks"][0]["completion_policy"] == "ongoing"
                    and first["tasks"][0]["completed_at"] is None, "ordinary Root ended ongoing Task")
            h.command("chat", "work")
            require(state(h) == first, "idle ongoing Task started unsolicited work")
            h.send("complete", "Complete: Wedding"); h.command("chat", "work")
            completed = state(h); check(completed)
            require(completed["tasks"][0]["status"] == "completed" and completed["tasks"][0]["task_epoch"] == 1
                    and completed["tasks"][0]["completed_at"] is not None
                    and completed["tasks"][0]["version"] == first["tasks"][0]["version"] + 1, "explicit completion transition incorrect")
            require(completed["updates"] == first["updates"] and completed["inbox"] == first["inbox"], "complete emitted synthetic update/follow-up")
            require(len(completed["task_requests"]) == len(first["task_requests"]) + 1
                    and len(completed["task_invocations"]) == len(first["task_invocations"]) + 2, "complete duplicate did not reuse receipt")
            h.send("complete-again", "Complete: Wedding"); h.command("chat", "work")
            rejected = state(h)
            require(rejected["tasks"] == completed["tasks"] and rejected["task_requests"] == completed["task_requests"], "new completion accepted on completed Task")
            h.send("append", "Continue: Wedding | Update the guest count.")
            h.command("chat", "work")
            appended = state(h); check(appended, roots=2, updates=2)
            require(appended["tasks"][0]["status"] == "active" and appended["tasks"][0]["task_epoch"] == 2,
                    "append lost ongoing policy/epoch")
            require(next(r for r in appended["runs"] if r["run_id"] == first["updates"][0]["root_run_id"])
                    == next(r for r in first["runs"] if r["run_id"] == first["updates"][0]["root_run_id"]), "old Root changed")
            h.send("pause", "Pause: Wedding"); h.command("chat", "work")
            paused = state(h)
            require(paused["tasks"][0]["status"] == "paused" and paused["tasks"][0]["task_epoch"] == 3,
                    "ongoing pause failed")
            h.send("resume", "Continue: Wedding | Continue with the new date."); h.command("chat", "work")
            resumed = state(h); check(resumed, roots=3, updates=3)
            require(resumed["tasks"][0]["status"] == "active" and resumed["tasks"][0]["task_epoch"] == 4,
                    "ongoing continuation failed")
            h.send("cancel", "Cancel: Wedding"); h.command("chat", "work")
            cancelled = state(h)
            require(cancelled["tasks"][0]["status"] == "cancelled" and cancelled["tasks"][0]["task_epoch"] == 5,
                    "ongoing cancellation failed")
            h.command("chat", "work")
            require(state(h) == cancelled, "restart changed cancelled ongoing Task")
            return {"first": first, "completed": completed, "rejected": rejected, "appended": appended, "paused": paused, "resumed": resumed, "cancelled": cancelled}

        report["ongoing"] = scenario("ongoing", ongoing)
        report["checks"].append("Explicit ongoing create -> Root stays active -> complete and duplicate/rejection -> append/new Root -> pause/continue/cancel; no synthetic update")

        def feedback(h):
            h.send("feedback-create", "Ongoing: Report scope")
            h.command("chat", "work")
            first = state(h)
            ref = first["tasks"][0]["task_ref"]
            h.send("question", f"Question: {ref} | Which reporting period?")
            h.command("chat", "work")
            page = h.command("task", "feedbacks", ref)
            require(len(page["items"]) == 1 and page["items"][0]["kind"] == "input", "question not visible through owner CLI")
            question = page["items"][0]
            require(question["task_ref"] == ref and question["source_message_id"], "question lost exact Task/message linkage")
            before = state(h)
            receipt = h.command("task", "respond", question["feedback_ref"], text="Use the last full quarter.")
            require(h.command("task", "respond", question["feedback_ref"], text="Use the last full quarter.") == receipt,
                    "retry/restart changed response receipt")
            accepted = state(h)
            require(len([r for r in accepted["runs"] if r["kind"] == "worker"]) == 1 and accepted["instructions"] == before["instructions"],
                    "owner response directly started work")
            h.command("chat", "work")
            continued = state(h)
            check(continued, roots=2, updates=2)
            require(continued["tasks"][0]["task_ref"] == ref and len(continued["instructions"]) == 1,
                    "Main did not continue original Task exactly once")
            require(h.command("task", "feedbacks")["items"] == [], "answered feedback remains actionable")
            conflict = h.command("task", "respond", question["feedback_ref"], text="Different answer", ok=False)
            require(conflict.returncode != 0, "different duplicate answer accepted")
            h.send("stale-question", f"Question: {ref} | Confirm the date?")
            h.command("chat", "work")
            pending = h.command("task", "feedbacks", ref)["items"][0]
            h.send("stop-feedback", "Pause: Report scope")
            h.command("chat", "work")
            stale = h.command("task", "respond", pending["feedback_ref"], text="Continue", ok=False)
            require(stale.returncode != 0 and h.command("task", "feedbacks")["items"] == [], "paused question remained actionable")
            return {"question": question, "receipt": receipt, "accepted": accepted, "continued": continued,
                    "paused": state(h), "stale_error": stale.stderr}

        report["feedback"] = scenario("feedback", feedback)
        report["checks"].append("Main saves exact Task/message question -> owner CLI list/respond across processes -> dedup -> Main append -> same Task Root; paused stale response rejected")

        def artifact_review(h):
            h.send("create", "Ongoing: Report review")
            h.command("chat", "work")
            ref = state(h)["tasks"][0]["task_ref"]
            artifact = h.command("artifact", "save", "report.txt", "text/plain", text="Report revision A")
            parts = [{"type": "text", "text": f"Review: {ref} | {artifact['artifact_ref']} | Review the introduction."},
                     {"type": "file", "artifact_ref": artifact["artifact_ref"]}]
            h.command("chat", "send-parts", "review-request", text=json.dumps(parts))
            h.command("chat", "work")
            review = h.command("task", "feedbacks", ref)["items"][0]
            require(review["kind"] == "review" and review["artifact_refs"] == [artifact["artifact_ref"]], "review lost exact artifact revision")
            h.command("task", "respond", review["feedback_ref"], text="Clarify the introduction.")
            require(h.command("approval", "list") == [], "artifact review became an action approval")
            h.command("chat", "work")
            result = state(h)
            check(result, roots=2, updates=2)
            require(len(result["instructions"]) == 1 and result["tasks"][0]["task_ref"] == ref
                    and json.loads(result["instructions"][0]["artifact_refs"]) == [artifact["artifact_ref"]],
                    "review did not continue exact original task/revision")
            require(h.command("artifact", "describe", artifact["artifact_ref"])["content_hash"] == artifact["content_hash"],
                    "review changed artifact revision")
            return {"review": review, "artifact": artifact, "continued": result}

        report["artifact_review"] = scenario("artifact-review", artifact_review)
        report["checks"].append("Uploaded exact ArtifactRef -> Main saved review -> owner feedback -> Main continuation; action approval stays separate")

        for boundary in ("before_task_feedback_response_commit", "after_task_feedback_response_commit"):
            def feedback_crash(h):
                h.send("create", "Ongoing: Report feedback recovery")
                h.command("chat", "work")
                ref = state(h)["tasks"][0]["task_ref"]
                h.send("question", f"Question: {ref} | Which reporting period?")
                h.command("chat", "work")
                item = h.command("task", "feedbacks", ref)["items"][0]
                child = h.hold(boundary, command=("task", "respond", item["feedback_ref"]), input_text="Use the last quarter.")
                child.kill(); child.wait(timeout=5)
                killed = state(h)
                expected = "pending" if boundary.startswith("before") else "answered"
                require(killed["feedbacks"][0]["status"] == expected, "response transaction crossed its kill boundary")
                receipt = h.command("task", "respond", item["feedback_ref"], text="Use the last quarter.")
                require(h.command("task", "respond", item["feedback_ref"], text="Use the last quarter.") == receipt,
                        "response recovery lost dedup")
                h.command("chat", "work")
                recovered = state(h)
                check(recovered, roots=2, updates=2)
                require(len(recovered["instructions"]) == 1 and recovered["feedbacks"][0]["status"] == "answered",
                        "response recovery repeated continuation")
                return {"boundary": boundary, "killed": killed, "recovered": recovered}
            report["crashes"].append(scenario(boundary, feedback_crash))
        report["checks"].append("SIGKILL before/after atomic feedback response -> reopen/retry -> one Main input and one continuation")

        for boundary in ("before_task_commit", "after_task_commit", "before_result_commit", "after_result_commit"):
            def ongoing_crash(h):
                h.send("ongoing", "Ongoing: Wedding restart checklist.")
                child = h.hold(boundary)
                child.kill(); child.communicate(timeout=10)
                before = state(h)
                h.command("chat", "work")
                after = state(h); check(after)
                require(after["tasks"][0]["status"] == "active" and after["tasks"][0]["completion_policy"] == "ongoing",
                        "restart lost explicit ongoing policy")
                require(all(r["status"] == "completed" for r in after["runs"]), "restart stranded ongoing Root")
                prior = {g["generation_id"]: g for g in before["generations"] if g["status"] == "completed"}
                require(all(g["attempt_count"] == prior[g["generation_id"]]["attempt_count"] and g["request_hash"] == prior[g["generation_id"]]["request_hash"]
                            for g in after["generations"] if g["generation_id"] in prior), "ongoing restart replayed completed model call")
                h.command("chat", "work")
                require(state(h) == after, "ongoing restart repeated result/started Root")
                return {"boundary": boundary, "before_kill": before, "recovered": after}
            report["crashes"].append(scenario("ongoing-" + boundary, ongoing_crash))
        report["checks"].append("Explicit ongoing policy survives SIGKILL before/after Task creation and Root result; completed calls/results reused")

        for boundary in ("before_task_complete_commit", "after_task_complete_commit"):
            def complete_crash(h):
                h.send("create", "Ongoing: Wedding completion restart."); h.command("chat", "work")
                initial = state(h)
                receipt = h.send("complete", "Complete: Wedding")
                require(receipt == h.send("complete", "Complete: Wedding"), "duplicate completion ingress drift")
                child = h.hold(boundary)
                child.kill(); child.communicate(timeout=10)
                before = state(h)
                require(before["tasks"][0]["status"] == ("active" if boundary.startswith("before_") else "completed"), "completion boundary not atomic")
                h.command("chat", "work")
                after = state(h); check(after)
                require(after["tasks"][0]["status"] == "completed" and after["tasks"][0]["task_epoch"] == 1
                        and after["tasks"][0]["completion_policy"] == "ongoing"
                        and after["tasks"][0]["version"] == initial["tasks"][0]["version"] + 1, "completion replay changed policy/epoch or repeated update")
                require(after["updates"] == initial["updates"] and after["inbox"] == initial["inbox"], "recovered complete emitted synthetic follow-up")
                require(len(after["task_requests"]) == 2 and len(after["task_invocations"]) == 3, "completion receipt duplicate/omission")
                prior = {g["generation_id"]: g for g in before["generations"] if g["status"] == "completed"}
                require(all(g["attempt_count"] == prior[g["generation_id"]]["attempt_count"] and g["request_hash"] == prior[g["generation_id"]]["request_hash"]
                            for g in after["generations"] if g["generation_id"] in prior), "completion replay repeated completed model call")
                h.command("chat", "work"); require(state(h) == after, "completed Task changed on restart")
                return {"boundary": boundary, "before_kill": before, "recovered": after}
            report["crashes"].append(scenario(boundary, complete_crash))
        report["checks"].append("SIGKILL before/after explicit completion transaction: status/timestamp/receipt recover atomically, duplicate invocations reuse acceptance, no synthetic Worker follow-up")

        def responsive(h):
            with Terminal(h.args("-test-main-hold", "before_worker_generation", "tui"), h.env, h.root) as t:
                t.expect("You> ")
                t.send("Delegate: Hold this long report until restart.")
                t.expect_all(['"boundary":"before_worker_generation"', "Main accepted the work."])
                t.send("Urgent synchronous question")
                t.expect("Main synchronous answer.")
                before = state(h)
                require(any(r["kind"] == "worker" and r["status"] == "running" for r in before["runs"]), "Root was not still held")
                t.stop(signal.SIGKILL)
                transcript = t.transcript()
            h.command("chat", "work")
            after = state(h); check(after)
            return {"transcript": transcript, "before_kill": before, "recovered": after}

        report["responsive"] = scenario("responsive", responsive)
        report["checks"].append("Committed Main acceptance and new answer visible on PTY while Root is held; actual kill/restart completes it")

        def ambiguous(h):
            h.send("two", "Delegate many: Travel Kyoto itinerary. | Travel Osaka itinerary.")
            h.command("chat", "work")
            h.send("ambiguous", "Continue: Travel | Change the dates.")
            h.command("chat", "work")
            value = state(h); check(value, tasks=2, roots=2, updates=2)
            require(not value["instructions"] and any("Please clarify" in p.get("text", "") for m in value["conversation"] for p in m["content"]), "ambiguous search changed a Task")
            return value

        report["ambiguous"] = scenario("ambiguous", ambiguous)
        report["checks"].append("Ambiguous Task search preserves both candidates and asks once without appending")

        def failure(h):
            h.send("failure", "Delegate: Fail worker: report unavailable.")
            h.command("chat", "work")
            value = state(h); check(value)
            require(value["tasks"][0]["status"] == "active" and value["updates"][0]["kind"] == "failed", "Root failure failed/completed Task")
            notices = [m for m in value["conversation"] if m["source"] == "task"]
            require(len(notices) == 1 and "did not finish" in json.dumps(notices[0]), "Root failure lifecycle notice missing or duplicated")
            return value

        report["failure"] = scenario("failure", failure)
        report["checks"].append("Failed Root persists one failed update; Task stays active and Main receives it")

        for boundary in ("before_progress_commit", "after_progress_commit"):
            def progress_crash(h):
                h.send("progress", "Delegate: Compare sources and prepare the recommendation.")
                child = h.hold(boundary)
                child.kill(); child.communicate(timeout=10)
                before = state(h)
                summary = "Requirements reviewed; checking the supporting evidence."
                expected = summary if boundary.startswith("after_") else ""
                require(before["tasks"][0]["progress_summary"] == expected, "progress/checkpoint boundary not atomic")
                require(not before["updates"] and not before["inbox"], "ordinary progress woke Main")
                h.command("chat", "work")
                after = state(h); check(after)
                require(after["tasks"][0]["progress_summary"] == summary, "recovery lost semantic progress")
                prior = {g["generation_id"]: g for g in before["generations"] if g["status"] == "completed"}
                require(all(g["attempt_count"] == prior[g["generation_id"]]["attempt_count"] and g["request_hash"] == prior[g["generation_id"]]["request_hash"]
                            for g in after["generations"] if g["generation_id"] in prior), "progress recovery regenerated a completed turn")
                require(sum(r["kind"] == "main" for r in after["runs"]) == 2, "progress triggered an extra Main run")
                h.command("chat", "work"); require(state(h) == after, "progress recovery churned terminal state")
                return {"boundary": boundary, "before_kill": before, "recovered": after}
            report["crashes"].append(scenario(boundary, progress_crash))
        report["checks"].append("F-005: Root stage prose commits atomically with its checkpoint; SIGKILL before/after preserves progress, reuses accepted model turns and creates no extra Main work")

        for boundary in ("before_task_commit", "after_task_commit", "before_result_commit", "after_result_commit",
                         "before_report_commit", "after_report_commit", "before_followup_commit", "after_followup_commit"):
            def crash(h):
                receipt = h.send("duplicate", "Duplicate: Recover the wedding checklist.")
                require(receipt == h.send("duplicate", "Duplicate: Recover the wedding checklist."), "duplicate ingress receipt drifted")
                child = h.hold(boundary)
                child.kill(); child.communicate(timeout=10)
                before = state(h)
                h.command("chat", "work")
                after = state(h); check(after)
                require(all(r["status"] == "completed" for r in after["runs"]), "recovery left nonterminal work")
                require(len(after["task_requests"]) == 1 and len(after["task_invocations"]) == 2, "duplicate source/Tool created another Task")
                prior = {g["generation_id"]: g for g in before["generations"] if g["status"] == "completed"}
                require(all(g["attempt_count"] == prior[g["generation_id"]]["attempt_count"] and g["request_hash"] == prior[g["generation_id"]]["request_hash"] for g in after["generations"] if g["generation_id"] in prior), "completed model call was replayed")
                h.command("chat", "work")
                require(state(h) == after, "duplicate/restart drain changed terminal state")
                return {"boundary": boundary, "before_kill": before, "recovered": after}
            report["crashes"].append(scenario(boundary, crash))
        report["checks"].append("Eight SIGKILL boundaries around atomic Task/Root, result, Reporter Inbox and batched Main admission; completed calls reused")
    report["cleanup"] = "owned PTYs/children reaped; disposable databases removed"
    return report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args(); args.evidence.unlink(missing_ok=True)
    binary = args.binary.resolve()
    report = run(binary)
    report["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"checks": report["checks"], "crash_boundaries": len(report["crashes"]), "evidence": str(args.evidence)}))


if __name__ == "__main__":
    main()
