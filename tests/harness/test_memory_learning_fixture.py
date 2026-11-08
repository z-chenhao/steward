"""Exercise deterministic feedback independently of any language-model answer."""
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

PATH = Path(__file__).resolve().parents[1] / "fixtures/memory_learning_mcp.py"
SPEC = importlib.util.spec_from_file_location("memory_learning_fixture", PATH)
FIXTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIXTURE)


class MemoryLearningFixtureTests(unittest.TestCase):
    def test_rejected_hypothesis_then_verified_correction_and_changed_policy(self):
        policy = {"version": 1, "ledger_import": {"encoding": "utf-8-no-bom", "modify_production": False}}
        failed = FIXTURE.validate(policy, "ledger_import", {})
        self.assertFalse(failed["passed"])
        self.assertTrue(FIXTURE.validate(policy, "ledger_import", failed["required_plan"])["passed"])
        self.assertFalse(FIXTURE.validate(policy, "ledger_import", {**failed["required_plan"], "send_email": False})["passed"])
        policy["ledger_import"]["encoding"] = "utf-16-le"
        self.assertFalse(FIXTURE.validate(policy, "ledger_import", {"encoding": "utf-8-no-bom", "modify_production": False})["passed"])

    def test_stdio_uses_same_validator_and_records_only_sandbox_attempts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "policy.json").write_text(json.dumps({"version": 1, "expense_draft": {"send_email": False}}))
            request = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
                "name": "validate", "arguments": {"operation": "expense_draft", "plan": {"send_email": False}}}}
            output = io.StringIO()
            FIXTURE.serve(root, io.StringIO(json.dumps(request) + "\n"), output)
            body = json.loads(json.loads(output.getvalue())["result"]["content"][0]["text"])
            self.assertTrue(body["passed"])
            self.assertEqual(sorted(p.name for p in root.iterdir()), ["attempts.jsonl", "policy.json"])
