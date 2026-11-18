"""Bounded interleaving model of one approved project ToolInvocation.

Two ownership epochs, one immutable plan, one command effect and one cleanup.
SQL transactions are atomic. Engine responses are nondeterministic observations;
this model does not prove shell behavior, timers, OS isolation or liveness.
"""
from collections import deque
from dataclasses import asdict, dataclass, replace


@dataclass(frozen=True)
class State:
    epoch: int = 0
    owner: int = 0
    authorized: bool = True
    approved: bool = False
    status: str = 'queued'
    plan: bool = False
    dispatched: bool = False
    result: bool = False
    closed: bool = False
    effects: int = 0
    invalid_receipt: bool = False


def transitions(s, stale_receipt=False):
    current = s.owner == s.epoch and s.epoch > 0 and s.authorized
    if s.epoch == 0:
        yield 'claim', replace(s, epoch=1)
    if s.epoch == 1:
        # Abstract lease expiry/release followed by a new real claim.
        yield 'takeover', replace(s, epoch=2)
    if s.authorized:
        yield 'revoke', replace(s, authorized=False)
    if s.authorized and not s.approved:
        yield 'approve', replace(s, approved=True)
    if s.status == 'queued' and s.epoch and s.approved and s.authorized:
        yield 'start', replace(s, status='running', owner=s.epoch)
    if s.status == 'running' and current and not s.plan:
        yield 'persist_plan', replace(s, plan=True)
    if s.status == 'running' and current and s.plan and not s.dispatched and not s.closed:
        yield 'dispatch', replace(s, dispatched=True, effects=s.effects + 1)
    if s.dispatched and not s.result:
        yield 'observe_result', replace(s, result=True)
    if s.plan and not s.closed and (s.result or not current):
        yield 'confirm_cleanup', replace(s, closed=True)
    if s.status == 'running' and not current:
        # Recovery settles uncertainty. There is no transition back to queued,
        # no replay of dispatch, and cleanup never writes a success receipt.
        yield 'recover_unknown', replace(s, status='unknown')
    if (s.status == 'running' and s.result and s.closed and s.authorized
            and (current or stale_receipt)):
        yield 'accept_receipt', replace(s, status='succeeded', invalid_receipt=not current)


def violation(s):
    if s.invalid_receipt:
        return 'receipt_requires_current_authority'
    if s.effects > 1:
        return 'unknown_effect_is_never_replayed'
    if s.status == 'succeeded' and not (s.plan and s.result and s.closed):
        return 'success_requires_plan_result_and_cleanup'
    return None


def explore(stale_receipt=False):
    initial = State()
    paths = {initial: []}
    queue = deque([initial])
    edges = 0
    while queue:
        state = queue.popleft()
        for action, following in transitions(state, stale_receipt):
            edges += 1
            if following in paths:
                continue
            path = paths[state] + [dict(action=action, state=asdict(following))]
            paths[following] = path
            failed = violation(following)
            if failed:
                return dict(valid=False, states=len(paths), transitions=edges,
                            property=failed, counterexample=path)
            queue.append(following)
    return dict(valid=True, states=len(paths), transitions=edges, counterexample=None)
