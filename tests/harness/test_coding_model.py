"""Safety interleavings and an independently broken claim fence."""
import unittest

from coding_verification_model import explore


class CodingVerificationModelTests(unittest.TestCase):
    def test_all_bounded_interleavings_preserve_receipt_and_non_replay(self):
        result = explore()
        self.assertTrue(result['valid'], result)
        self.assertGreater(result['states'], 50, 'empty or sequential exploration is insufficient')
        self.assertGreater(result['transitions'], result['states'])

    def test_ignoring_ownership_finds_a_late_receipt_counterexample(self):
        result = explore(stale_receipt=True)
        self.assertFalse(result['valid'])
        self.assertEqual(result['property'], 'receipt_requires_current_authority')
        actions = [step['action'] for step in result['counterexample']]
        self.assertIn('dispatch', actions)
        self.assertIn('takeover', actions)
        self.assertEqual(actions[-1], 'accept_receipt')
        final = result['counterexample'][-1]['state']
        self.assertNotEqual(final['owner'], final['epoch'])
        self.assertTrue(final['closed'], 'cleanup alone cannot authorize a receipt')
