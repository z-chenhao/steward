#!/usr/bin/env python3
"""Steward-owned, stdio MCP validator for disposable memory learning cases.

Plans are validated against an operator fixture; no import, email or login is
performed. The peer has only its sandbox directory and no network or credentials.
"""
import argparse
import json
from pathlib import Path
import sys


SCHEMA = {
    "type": "object",
    "properties": {
        "operation": {"type": "string", "enum": ["ledger_import", "expense_draft", "device_login"]},
        "plan": {"type": "object", "properties": {
            "encoding": {"type": "string"},
            "steps": {"type": "array", "items": {"type": "string"}},
            "device": {"type": "string"},
            "reuse_callback": {"type": "boolean"},
            "send_email": {"type": "boolean"},
            "modify_production": {"type": "boolean"},
        }, "additionalProperties": False},
    },
    "required": ["operation", "plan"], "additionalProperties": False,
}


def validate(policy, operation, plan):
    expected = policy[operation]
    passed = set(plan) == set(expected) and all(key in plan and type(plan[key]) is type(value) and plan[key] == value
                 for key, value in expected.items())
    # Failure is deterministic feedback from the test environment, not a model
    # judge. An empty plan is a harmless probe, never a real-world operation.
    return {"operation": operation, "passed": passed,
            "policy_version": policy["version"], "simulated": True,
            "feedback": "Validated in the isolated fixture." if passed else "Plan rejected; test this required plan before recording it as verified.",
            **({} if passed else {"required_plan": expected})}


def serve(root, input_stream, output):
    for line in input_stream:
        request = json.loads(line)
        if "id" not in request:
            continue
        method = request["method"]
        if method == "initialize":
            result = {"protocolVersion": "2025-03-26", "capabilities": {"tools": {}},
                      "serverInfo": {"name": "steward-memory-learning", "version": "1"}}
        elif method == "tools/list":
            result = {"tools": [{"name": "validate", "description":
                "Validate a plan in the isolated personal workflow simulator. No external operation occurs. Failure returns test feedback; a passing call confirms the plan for the current policy version.",
                "inputSchema": SCHEMA}]}
        elif method == "tools/call":
            params = request["params"]
            if params["name"] != "validate":
                raise ValueError("unknown fixture tool")
            args = params["arguments"]
            policy = json.loads((root / "policy.json").read_text())
            observation = validate(policy, args["operation"], args["plan"])
            with (root / "attempts.jsonl").open("a") as log:
                log.write(json.dumps({"arguments": args, "observation": observation}) + "\n")
            result = {"content": [{"type": "text", "text": json.dumps(observation)}], "isError": False}
        else:
            raise ValueError("unsupported fixture method")
        output.write(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}) + "\n")
        output.flush()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("root", type=Path)
    args = parser.parse_args()
    serve(args.root, sys.stdin, sys.stdout)
