#!/usr/bin/env python3
"""SC-04/05 M2 boundaries through Main tools, PTY, SIGKILL and SQLite readback."""
import argparse
import hashlib
import json
from pathlib import Path
import tempfile

from terminal_driver import Terminal
from verify_main import MainHarness, require
from verify_tasks import state, select_managed_tasks


def stopped(value, status, reason):
    workers = [r for r in value["runs"] if r["kind"] == "worker"]
    require(value["tasks"][0]["status"] == status and value["tasks"][0]["task_epoch"] == 2,
            "stop status/epoch missing")
    require(len(workers) == 1 and workers[0]["status"] == "cancelled" and workers[0]["error_code"] == reason,
            "old Root did not settle with the exact reason")
    require(not value["updates"] and not value["inbox"], "stop woke Main with a stale result")
    require(all(g["status"] != "pending" for g in value["generations"]), "stopped generation remains pending")
    require(not any(t["run_id"] == workers[0]["run_id"] for t in value["tools"]), "stopped Root started a new Tool")


def continued(before, after):
    old = next(r for r in before["runs"] if r["kind"] == "worker")
    require(next(r for r in after["runs"] if r["run_id"] == old["run_id"]) == old, "old Root revived or changed")
    workers = [r for r in after["runs"] if r["kind"] == "worker"]
    require(len(workers) == 2 and len({r["run_id"] for r in workers}) == 2, "continue did not create a new Root")
    require(after["tasks"][0]["status"] == "completed" and after["tasks"][0]["task_epoch"] == 3,
            "continue did not reactivate with a new epoch")
    require(len(after["instructions"]) == 1 and len(after["updates"]) == len(after["inbox"]) == 1,
            "missing instruction or duplicate followup")
    require(after["tasks"][0]["anchor_task"] == before["tasks"][0]["anchor_task"], "anchor overwritten")


def run(binary):
    report = {"version": 1, "scope": "M2 SC-04/05; synthetic tree/unknown contracts in Go; M3 external effects, M4 Child admission, M5 Durable Replay and semantic scores remain not_applicable", "checks": [], "crashes": []}
    with tempfile.TemporaryDirectory(prefix="steward-task-control-") as directory:
        root = Path(directory)

        def scenario(name, drive):
            folder = root / name
            folder.mkdir()
            h = MainHarness(binary, folder, fixture="tasks")
            try:
                h.setup()
                select_managed_tasks(h)
                return drive(h)
            finally:
                h.cleanup()

        def live(h, operation):
            with Terminal(h.args("tui"), h.env, h.root) as t:
                t.expect("You> ")
                t.send("Delegate: Hold worker Wedding checklist with venue dependencies.")
                t.expect("Main accepted the work.")
                # The independent peer audit proves dispatch beyond Host queue
                # admission. Stream previews intentionally are not durable trace.
                def worker_accepted(value):
                    workers = {r['run_id'] for r in value['runs'] if r['kind'] == 'worker'}
                    pending = {g['generation_id'] for g in value['generations']
                               if g['run_id'] in workers and g['status'] == 'pending'}
                    audit = Path(h.env['STEWARD_DATA_ROOT']) / 'fake-provider-calls.jsonl'
                    if not audit.exists():
                        return False
                    return any(json.loads(line)['generation_id'] in pending
                               for line in audit.read_text().splitlines(keepends=True)
                               if line.endswith('\n'))
                h.wait_state(worker_accepted, "Worker peer accepted generation")
                active = state(h)
                t.send("Complete: Wedding")
                t.expect("Main accepted the work.")
                rejected = state(h)
                require(rejected["tasks"] == active["tasks"], "complete stopped/changed live work")
                require(any(json.loads(i["arguments_json"]).get("operation") == "complete"
                            and json.loads(i["result_json"]).get("error", {}).get("code") == "INVALID_ARGUMENT"
                            for i in rejected["tools"] if i["result_json"]), "live completion did not fail closed")
                t.send(operation + ": Wedding")
                t.expect("Main accepted the work."); t.expect("Steward: idle")
                before = state(h)
                stopped(before, "paused" if operation == "Pause" else "cancelled", "task_paused" if operation == "Pause" else "task_cancelled")
                t.send("Continue: Wedding | Continue with the new date.")
                t.expect("Main follow-up:"); t.expect("Steward: idle")
                after = state(h); continued(before, after)
                t.send("/status"); t.expect("completed")
                require(t.stop() == 0, "PTY did not exit cleanly")
                transcript = t.transcript()
            h.command("chat", "work")
            require(state(h) == after, "repeated drain changed terminal state")
            return {"transcript": transcript, "stopped": before, "continued": after}

        for operation in ("Pause", "Cancel"):
            report[operation.lower()] = scenario(operation.lower(), lambda h: live(h, operation))
        report["checks"].append("Live PTY Main search/manage -> provider generation cancellation -> stopped Root; append creates new Root/epoch; no stale followup or terminal replay")

        for boundary in ("before_stop_commit", "after_stop_commit", "before_stop_settle_commit"):
            def crash(h):
                h.send("create", "Delegate: Hold worker Wedding plan.")
                worker = h.hold("before_worker_generation")
                worker.kill(); worker.communicate(timeout=10)
                h.send("pause", "Pause: Wedding")
                stopper = h.hold(boundary)
                stopper.kill(); stopper.communicate(timeout=10)
                before = state(h)
                h.command("chat", "work")
                paused = state(h); stopped(paused, "paused", "task_paused")
                h.send("continue", "Continue: Wedding | Continue with the new date.")
                h.command("chat", "work")
                after = state(h); continued(paused, after)
                h.command("chat", "work")
                require(state(h) == after, "restart redelivered completed work")
                return {"boundary": boundary, "durable_state_after_kill": before, "paused_after_restart": paused, "continued": after}
            report["crashes"].append(scenario(boundary, crash))
        report["checks"].append("Actual SIGKILL before/after stop acceptance and before stop settlement; persisted intent recovered without old Root execution; new Root after append")
    report["cleanup"] = "all owned PTYs/processes reaped; disposable state removed"
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
    print(json.dumps({"checks": report["checks"], "crashes": len(report["crashes"]), "evidence": str(args.evidence)}))


if __name__ == "__main__":
    main()
