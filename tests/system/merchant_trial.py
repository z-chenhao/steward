"""Test an owned sandbox merchant behind the usual MCP discovery, approvals and recovery."""
from contextlib import closing
import json
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys

from verify_main import require


def configure(root, repo):
    peer = root / 'merchant'
    peer.mkdir()
    script = peer / 'sandbox_merchant.py'
    shutil.copyfile(repo / 'scripts/sandbox_merchant.py', script)
    ledger = peer / 'orders.db'
    python = '/usr/bin/python3'
    if sys.platform == 'darwin':
        python = subprocess.check_output(['xcrun', '--find', 'python3'], text=True).strip()
    for order in ('success', 'lost'):
        subprocess.run([python, str(script), '--ledger', str(ledger), 'quote', order, 'CNY', '1299'], check=True)
    # The test edge drops the acknowledgement after the merchant commits. Audit each
    # dispatch independently so merchant-side dedup cannot hide a forbidden replay.
    edge = peer / 'lose_ack.py'
    edge.write_text("""import os
import sys
from pathlib import Path
sys.path.insert(0,str(Path(__file__).parent))
import sandbox_merchant as merchant
dispatch = merchant.dispatch
def observed(db,name,args):
    with (db.parent/'requests').open('a') as log: print(name,args.get('order'),file=log,flush=True)
    result=dispatch(db,name,args)
    if name=='pay_order' and args.get('order')=='lost' and 'receipt' in result: os._exit(0)
    return result
merchant.dispatch=observed
merchant.main()
""")
    return {'name': 'test_merchant', 'command': python, 'directory': str(peer),
            'args': [str(edge), '--ledger', str(ledger), 'serve'],
            'tools': {'get_order': 'read_only', 'pay_order': 'external_effect'}}


def verify(h, state, root):
    ledger = root / 'merchant/orders.db'

    def record(order):
        with closing(sqlite3.connect(ledger.resolve().as_uri() + '?mode=ro', uri=True)) as db:
            return db.execute('SELECT currency,amount,approved_max,receipt FROM orders WHERE order_id=?', (order,)).fetchone()

    for order in ('success', 'lost'):
        approved = {'merchant': 'steward-test-merchant', 'order': order, 'currency': 'CNY', 'max_minor': 1500}
        received = h.send('merchant-pay-'+order, 'MCP_MERCHANT_PAY '+json.dumps(approved))
        h.command('chat', 'work')
        approvals = [a for a in h.command('approval', 'list') if a['status'] == 'pending']
        require(len(approvals) == 1 and approvals[0]['tool'] == 'mcp__test_merchant__pay_order'
                and record(order)[3] is None, 'merchant processed an order before exact consent')
        decision = approvals[0]
        with state(h) as db:
            frozen = db.execute("SELECT arguments_json FROM tool_invocation WHERE run_id=? AND tool_name=?",
                                (received['run_id'], decision['tool'])).fetchone()
            require(frozen and json.loads(frozen[0]) == approved, 'approval lost the currency, ceiling or order')
        h.command('approval', 'approve', decision['approval_id'],decision['frozen_arguments_hash'])
        h.command('chat', 'work')
        with state(h) as db:
            status, result = db.execute("SELECT status,result_json FROM tool_invocation WHERE run_id=? AND tool_name=?",
                                        (received['run_id'], decision['tool'])).fetchone()
            require(status == ('unknown' if order == 'lost' else 'succeeded'), 'unexpected payment status '+status)
        currency, amount, ceiling, receipt = record(order)
        require((currency,amount,ceiling) == ('CNY',1299,1500) and receipt.startswith('sandbox-'),
                'merchant receipt exceeded the approved scope')
        if order == 'success':
            require(receipt in result, 'approved checkout did not return the final receipt')
        h.command('chat','work')
        lookup = h.send('merchant-query-'+order, 'MCP_MERCHANT_GET '+json.dumps({'order':order}))
        h.command('chat','work')
        with state(h) as db:
            found = db.execute("SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name='mcp__test_merchant__get_order'",
                               (lookup['run_id'],)).fetchone()
            require(found and receipt in found[0], 'lookup after restart lost original merchant receipt')
        calls = (root/'merchant/requests').read_text().splitlines()
        require(calls.count('pay_order '+order) == 1 and calls.count('get_order '+order) == 1,
                'unknown recovery repeated payment or queried another order')
