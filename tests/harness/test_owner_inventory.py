import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import owner_inventory as inventory


class DefaultOwnerInventoryTest(unittest.TestCase):
    def test_same_size_replacement_fails_with_case_identity(self):
        graph = {'bindings': [{'instance_id': 'coding-workspace'}]}
        first = {'instance_id': 'coding-workspace', 'contract': 'steward.coding-check-limits/1',
                 'input': {'timeout_seconds': 60}, 'expected': {'allowed': True}}
        second = {**first, 'input': {'timeout_seconds': 120}}
        wanted, duplicate = (inventory.case_identity(case) for case in (first, second))
        with tempfile.TemporaryDirectory() as tmp:
            manifest = Path(tmp) / 'expected.json'
            manifest.write_text(json.dumps({'instances': ['coding-workspace'], 'cases': [wanted, duplicate]}))
            with patch.object(inventory, 'INVENTORY', manifest):
                self.assertEqual(inventory.check_default_owner(graph, {'cases': [first, second]}), 2)
                with self.assertRaises(RuntimeError) as result:
                    inventory.check_default_owner(graph, {'cases': [second, second]})
                message = str(result.exception)
                self.assertIn(f'missing={[wanted]}', message)
                self.assertIn(f'unexpected={[duplicate]}', message)
