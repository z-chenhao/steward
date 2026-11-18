"""Bounded independent fixtures with owned process groups and joined cleanup."""
from concurrent.futures import ThreadPoolExecutor
import json
import signal
import subprocess
import threading
import time

from runner import group_alive, signal_group

_local = threading.local()


def current():
    return getattr(_local, 'scope', None)


class Scope:
    def __init__(self):
        self.stopped = threading.Event()
        self.lock = threading.Lock()
        self.children = set()

    def check(self):
        if self.stopped.is_set():
            raise RuntimeError('fixture cancelled')

    def start(self, *args, **kwargs):
        with self.lock:
            self.check()
            if 'start_new_session' in kwargs:
                raise ValueError('fixture scope owns process sessions')
            child = subprocess.Popen(*args, **kwargs, start_new_session=True)
            self.children.add(child)
            return child

    def retire(self, child):
        with self.lock:
            if child not in self.children:
                return
        if child.poll() is None:
            raise RuntimeError('fixture attempted to retire a live process')
        signal_group(child.pid, signal.SIGKILL)
        deadline = time.monotonic() + 2
        while group_alive(child.pid):
            if time.monotonic() >= deadline:
                raise RuntimeError('fixture process group did not drain')
            time.sleep(.02)
        with self.lock:
            self.children.discard(child)

    def run(self, *args, input=None, capture_output=False, timeout=None, check=False, **kwargs):
        if input is not None:
            if 'stdin' in kwargs:
                raise ValueError('input and stdin are exclusive')
            kwargs['stdin'] = subprocess.PIPE
        if capture_output:
            if 'stdout' in kwargs or 'stderr' in kwargs:
                raise ValueError('capture_output and output streams are exclusive')
            kwargs.update(stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        child = self.start(*args, **kwargs)
        try:
            with child:
                try:
                    stdout, stderr = child.communicate(input, timeout=timeout)
                except BaseException:
                    # Descendants can retain captured pipes after the leader
                    # exits. Kill the owned session before waiting for EOF.
                    signal_group(child.pid, signal.SIGKILL)
                    child.communicate()
                    raise
                result = subprocess.CompletedProcess(child.args, child.returncode, stdout, stderr)
                if check:
                    result.check_returncode()
                return result
        finally:
            self.retire(child)

    def cancel(self):
        with self.lock:
            if self.stopped.is_set():
                return
            self.stopped.set()
            children = list(self.children)
        for child in children:
            with self.lock:
                if child not in self.children:
                    continue
                signal_group(child.pid, signal.SIGTERM)
        deadline = time.monotonic() + 2
        while any(group_alive(c.pid) for c in children) and time.monotonic() < deadline:
            time.sleep(.02)
        for child in children:
            with self.lock:
                if child not in self.children:
                    continue
                signal_group(child.pid, signal.SIGKILL)
            child.wait(timeout=2)

    def finish(self):
        with self.lock:
            remaining = list(self.children)
        if remaining:
            self.cancel()
            raise RuntimeError('fixture did not retire every owned process')


def run_cases(items, action, jobs):
    if type(jobs) is not int or jobs not in (1, 2, 4):
        raise ValueError('fixture jobs must be 1, 2 or 4')
    parent = current()
    if parent is not None and jobs != 1:
        raise ValueError("nested fixture pools must stay serial")
    scope = parent or Scope()
    def drive(item):
        previous = current()
        _local.scope = scope
        try:
            scope.check()
            return action(item)
        except BaseException:
            scope.cancel()
            raise
        finally:
            _local.scope = previous
    pool = None
    try:
        if jobs == 1:
            result = [drive(item) for item in items]
        else:
            pool = ThreadPoolExecutor(max_workers=jobs)
            result = list(pool.map(drive, items))
        if parent is None:
            scope.finish()
        return result
    except BaseException:
        scope.cancel()
        raise
    finally:
        if pool is not None:
            pool.shutdown(wait=True, cancel_futures=True)


def run(*args, **kwargs):
    scope = current()
    return scope.run(*args, **kwargs) if scope is not None else subprocess.run(*args, **kwargs)


def run_json_lines(command, log, *, cwd, env, timeout):
    """Retain diagnostics separately from the command's JSON event stream."""
    with log.open('w') as output, log.with_suffix('.stderr.log').open('w') as diagnostics:
        result, = run_cases([command], lambda argv: run(argv, cwd=cwd, env=env,
                            stdout=output, stderr=diagnostics, timeout=timeout), 1)
    return result.returncode, [json.loads(line) for line in log.read_text().splitlines()]
