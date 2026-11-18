"""Compare the active default composition with the reviewed Owner inventory."""
from collections import Counter
import hashlib
import json
from pathlib import Path

INVENTORY = Path(__file__).resolve().parents[1] / 'fixtures/plugins/default-owner-inventory.json'


def case_identity(case):
    request = {key: value for key, value in case.items() if key != 'expected'}
    raw = json.dumps(request, sort_keys=True, ensure_ascii=False, separators=(',', ':'))
    # Go's JSON encoder always escapes these two Unicode separators.
    raw = raw.replace('\u2028', r'\u2028').replace('\u2029', r'\u2029')
    digest = hashlib.sha256(raw.encode()).hexdigest()
    return f"{case['instance_id']}/{case.get('contract', 'hook')} {digest}"


def expected_inventory():
    return json.loads(INVENTORY.read_text())


def check_default_owner(graph, policy):
    expected = expected_inventory()
    actual = {
        'instances': Counter(binding['instance_id'] for binding in graph['bindings']),
        'cases': Counter(map(case_identity, policy['cases'])),
    }
    for key in ('instances', 'cases'):
        wanted = Counter(expected[key])
        missing = sorted((wanted - actual[key]).elements())
        unexpected = sorted((actual[key] - wanted).elements())
        if missing or unexpected:
            raise RuntimeError(f'default Owner {key} differ from {INVENTORY.name}: '
                               f'missing={missing}, unexpected={unexpected}')
    return len(policy['cases'])


def check_default_owner_policy(db, graph, policy_id):
    raw = db.execute('SELECT policy_json FROM rsi_policy WHERE policy_id=?', (policy_id,)).fetchone()[0]
    return check_default_owner(graph, json.loads(raw))
