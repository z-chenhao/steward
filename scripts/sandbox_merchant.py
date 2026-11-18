#!/usr/bin/env python3
"""A local, opt-in MCP test merchant. Prices are set by the operator before admission."""
import argparse
from contextlib import closing
import json
from pathlib import Path
import re
import sqlite3
import sys
import uuid

MERCHANT = 'steward-test-merchant'
MAX_MINOR = 100_000_000
ORDER = re.compile(r'[A-Za-z0-9][A-Za-z0-9_-]{0,63}')
DENOMINATIONS = ('USD', 'CNY')
ORDER_FIELD = {'type': 'string', 'minLength': 1, 'maxLength': 64}
MONEY = {'type': 'integer', 'minimum': 1, 'maximum': MAX_MINOR}


def tool(name, description, fields):
    return {'name': name, 'description': description, 'inputSchema': {
        'type': 'object', 'properties': fields, 'required': list(fields), 'additionalProperties': False}}


TOOLS = [
    tool('pay_order', 'Pay one prequoted sandbox order up to the approved amount in minor units. '
         'Requires a separate approval for this exact order, currency and ceiling. After uncertainty use get_order.',
         {'merchant': {'type': 'string', 'enum': [MERCHANT]}, 'order': ORDER_FIELD,
          'currency': {'type': 'string', 'enum': list(DENOMINATIONS)}, 'max_minor': MONEY}),
    tool('get_order', 'Read a sandbox order, original quote and final receipt by its original order ID.',
         {'order': ORDER_FIELD}),
]


def quote(database, order, currency, amount):
    if not ORDER.fullmatch(order) or currency not in DENOMINATIONS or not 0 < amount <= MAX_MINOR:
        raise ValueError('Invalid order, currency or quoted price')
    with closing(sqlite3.connect(database)) as db, db:
        db.execute('''CREATE TABLE IF NOT EXISTS orders (
            order_id TEXT PRIMARY KEY, currency TEXT NOT NULL, amount INTEGER NOT NULL,
            receipt TEXT UNIQUE, approved_max INTEGER)''')
        db.execute('INSERT INTO orders(order_id,currency,amount) VALUES(?,?,?)', (order, currency, amount))


def dispatch(database, name, args):
    expected = {'order'} if name == 'get_order' else {'merchant', 'order', 'currency', 'max_minor'}
    if name not in ('get_order', 'pay_order') or not isinstance(args, dict) or set(args) != expected:
        return {'error': 'INVALID_ARGUMENT'}
    order = args['order']
    if not isinstance(order, str) or not ORDER.fullmatch(order):
        return {'error': 'INVALID_ORDER'}
    payment = name == 'pay_order'
    if payment and (args['merchant'] != MERCHANT or args['currency'] not in DENOMINATIONS
                    or type(args['max_minor']) is not int or not 0 < args['max_minor'] <= MAX_MINOR):
        return {'error': 'INVALID_APPROVAL_SCOPE'}
    mode = 'rw' if payment else 'ro'
    with closing(sqlite3.connect(database.resolve().as_uri() + '?mode=' + mode, uri=True)) as db, db:
        db.execute('BEGIN IMMEDIATE' if payment else 'BEGIN')
        row = db.execute('SELECT currency,amount,receipt,approved_max FROM orders WHERE order_id=?',
                         (order,)).fetchone()
        if row is None:
            return {'error': 'ORDER_NOT_FOUND'}
        currency, amount, receipt, ceiling = row
        if payment:
            if receipt is not None:
                return {'error': 'ALREADY_PAID_USE_GET_ORDER'}
            if args['currency'] != currency or amount > args['max_minor']:
                return {'error': 'QUOTE_OUTSIDE_APPROVAL'}
            receipt = 'sandbox-' + uuid.uuid4().hex
            ceiling = args['max_minor']
            db.execute('UPDATE orders SET receipt=?,approved_max=? WHERE order_id=? AND receipt IS NULL',
                       (receipt, ceiling, order))
        result = {'merchant': MERCHANT, 'order': order, 'currency': currency, 'amount_minor': amount,
                  'status': 'paid' if receipt else 'quoted'}
        if receipt:
            result.update(receipt=receipt, approved_max_minor=ceiling)
        return result


def serve(database, source, output):
    for line in source:
        q = json.loads(line)
        if 'id' not in q:
            continue
        reply = {'jsonrpc': '2.0', 'id': q['id']}
        method = q.get('method')
        if method == 'initialize':
            reply['result'] = {'protocolVersion': '2025-03-26', 'capabilities': {'tools': {}},
                               'serverInfo': {'name': MERCHANT, 'version': '1.0.0'}}
        elif method == 'tools/list':
            reply['result'] = {'tools': TOOLS}
        elif method == 'tools/call':
            params = q.get('params', {})
            result = dispatch(database, params.get('name'), params.get('arguments'))
            reply['result'] = {'content': [{'type': 'text', 'text': json.dumps(result, sort_keys=True)}],
                               'isError': 'error' in result}
        else:
            reply['error'] = {'code': -32601, 'message': 'Method not found'}
        print(json.dumps(reply), file=output, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ledger', type=Path, required=True)
    commands = parser.add_subparsers(dest='command', required=True)
    seed = commands.add_parser('quote')
    seed.add_argument('order')
    seed.add_argument('currency', choices=DENOMINATIONS)
    seed.add_argument('amount_minor', type=int)
    commands.add_parser('serve')
    args = parser.parse_args()
    if args.command == 'quote':
        quote(args.ledger, args.order, args.currency, args.amount_minor)
    else:
        serve(args.ledger, sys.stdin, sys.stdout)


if __name__ == '__main__':
    main()
