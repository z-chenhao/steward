import unittest
import copy
import json

import model_task_control as model
from verify_tasks import frozen_generation_contracts


class FrozenTaskGenerationTests(unittest.TestCase):
    def fixture(self):
        tools = [dict(name='fixture', input_schema='{}', description='Frozen tool')]
        messages = [dict(role='user', text='Source')]
        requests = [dict(run_id='run', model_snapshot=dict(snapshot_id='model', model_id='frozen'), messages=messages, tools=tools),
                    dict(run_id='run', model_snapshot=dict(snapshot_id='model', model_id='frozen'), messages=messages),
                    dict(run_id='run', model_snapshot=dict(snapshot_id='model', model_id='frozen'), messages=messages, tools=tools)]
        return dict(runs=[dict(run_id='run',model_snapshot_id='model',capability_snapshot_id='caps')],
                    snapshots=[dict(snapshot_id='model')],
                    capabilities=[dict(snapshot_id='caps',tools_json=json.dumps(tools))],
                    generations=[dict(run_id='run',step_index=i,request_json=json.dumps(r)) for i,r in enumerate(requests)],
                    compactions=[dict(run_id='run',step_index=1,messages_json=json.dumps(messages),
                                      provenance_json=json.dumps(dict(stage='summary_request',compaction=dict(history_artifact='artifact:original'))))])

    def test_summary_uses_the_frozen_model_without_granting_tools(self):
        value = self.fixture()
        self.assertTrue(frozen_generation_contracts(value))
        for changed in ('summary_tools', 'unmarked_summary', 'ordinary_schema', 'model', 'prepared_messages'):
            with self.subTest(changed=changed):
                broken = copy.deepcopy(value)
                index = 1 if changed == 'summary_tools' else 2
                request = json.loads(broken['generations'][index]['request_json'])
                if changed == 'summary_tools':
                    request['tools'] = [dict(name='forged',input_schema='{}')]
                elif changed == 'unmarked_summary':
                    broken['compactions'] = []
                elif changed == 'ordinary_schema':
                    request['tools'][0]['input_schema'] = '{"forged":true}'
                elif changed == 'model':
                    request['model_snapshot']['model_id'] = 'switched'
                else:
                    broken['compactions'][0]['messages_json'] = '[]'
                broken['generations'][index]['request_json'] = json.dumps(request)
                self.assertFalse(frozen_generation_contracts(broken))

    def test_compact_model_schema_is_frozen_separately_from_execution(self):
        value = self.fixture()
        compact = '{"type":"object","description":"Frozen direct declaration"}'
        frozen = json.loads(value['capabilities'][0]['tools_json'])
        frozen[0]['input_schema'] = '{"type":"object","required":["execution_field"]}'
        frozen[0]['model_input_schema'] = compact
        value['capabilities'][0]['tools_json'] = json.dumps(frozen)
        for index in (0, 2):
            request = json.loads(value['generations'][index]['request_json'])
            request['tools'][0]['input_schema'] = compact
            value['generations'][index]['request_json'] = json.dumps(request)
        self.assertTrue(frozen_generation_contracts(value))
        for replacement in (frozen[0]['input_schema'], '{}', '{"changed":true}'):
            broken = copy.deepcopy(value)
            request = json.loads(broken['generations'][2]['request_json'])
            request['tools'][0]['input_schema'] = replacement
            broken['generations'][2]['request_json'] = json.dumps(request)
            self.assertFalse(frozen_generation_contracts(broken))


class TaskControlModelTests(unittest.TestCase):
    def test_one_shot_completion_uses_root_outcome_after_child_drains(self):
        # Paired with TestTaskControlModelCompletionUsesRootOutcome in SQLite.
        for root_status in ("completed", "failed"):
            for child_status in ("completed", "failed"):
                for child_first in (False, True):
                    with self.subTest(root=root_status, child=child_status,
                                      child_first=child_first):
                        state = model.State()
                        trace = ["claim root0", "admit child owner1", "claim child"]
                        ends = [f"{root_status} root0", f"{child_status} child"]
                        trace += list(reversed(ends)) if child_first else ends
                        for index, action in enumerate(trace):
                            state, violation = next(
                                (n, v) for step, n, v in model.transitions(state)
                                if step == action)
                            self.assertIsNone(violation)
                            model.invariant(state)
                            if index < len(trace) - 1:
                                self.assertEqual(state.task, "active")
                        expected = "completed" if root_status == "completed" else "active"
                        self.assertEqual(state.task, expected)

    def test_exhaustive_safety_and_recovery_suffixes(self):
        report = model.explore()
        self.assertEqual(report["result"], "PASS")
        self.assertTrue(report["exhaustive"])
        self.assertGreater(report["recovery_suffixes"], 0)
        self.assertGreater(report["max_depth"], 3)

    def test_missing_fence_has_shortest_counterexample(self):
        report = model.explore(unsafe=True)
        self.assertEqual(report["result"], "COUNTEREXAMPLE")
        self.assertEqual(report["violation"], "stale business write")
        self.assertEqual(report["trace"], ["claim root0", "commit pause",
                                          "late result owner1 root0 (accept)"])
        self.assertFalse(report["exhaustive"])


if __name__ == "__main__":
    unittest.main()
