import importlib.util
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import tempfile
import unittest

source = Path(__file__).resolve().parents[2] / 'scripts/sandbox_merchant.py'
spec = importlib.util.spec_from_file_location('sandbox_merchant', source)
merchant = importlib.util.module_from_spec(spec)
spec.loader.exec_module(merchant)


class SandboxMerchantTest(unittest.TestCase):
    def test_ceiling_currency_and_single_use_even_with_concurrent_dispatch(self):
        with tempfile.TemporaryDirectory() as temp:
            ledger = Path(temp) / 'orders.db'
            merchant.quote(ledger,'order-A','CNY',1299)
            args = dict(merchant=merchant.MERCHANT,order='order-A',currency='CNY',max_minor=1500)
            for changed in (dict(currency='USD'),dict(max_minor=1298),dict(max_minor=True),
                            dict(merchant='other-merchant'),dict(order='order-B')):
                self.assertIn('error',merchant.dispatch(ledger,'pay_order',{**args,**changed}))
            self.assertEqual(merchant.dispatch(ledger,'get_order',{'order':'order-A'})['status'],'quoted')
            with ThreadPoolExecutor(max_workers=2) as pool:
                results = list(pool.map(lambda _: merchant.dispatch(ledger,'pay_order',args),range(2)))
            paid, = [r for r in results if 'receipt' in r]
            self.assertEqual([r['error'] for r in results if 'error' in r],['ALREADY_PAID_USE_GET_ORDER'])
            self.assertEqual((paid['currency'],paid['amount_minor'],paid['approved_max_minor']),('CNY',1299,1500))
            self.assertEqual(merchant.dispatch(ledger,'get_order',{'order':'order-A'}),paid)
