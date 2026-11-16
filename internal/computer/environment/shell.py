"""Fixed shell transport. Only the Host supplies command and accepted limits."""

try:
    request = json.load(sys.stdin)
    limits = request['limits']
    timeout, output = limits['timeout_seconds'], limits['output_bytes']
    if (type(timeout) is not int or not 1 <= timeout <= 120 or
            type(output) is not int or not 1 <= output <= 48000):
        raise ValueError()
    MAX_OUTPUT = output
    args = {'command': request['command'], 'timeout': timeout}
    result = run({'root': request['root'], 'method': 'bash', 'args': args})
    print(json.dumps({'value': result}, ensure_ascii=True))
except BlockingIOError:
    print(json.dumps({'error': 'RESOURCE_LIMIT'}))
except (ValueError, UnicodeError, KeyError):
    print(json.dumps({'error': 'UNKNOWN_SIDE_EFFECT' if started else 'INVALID_ARGUMENT'}))
except BaseException:
    print(json.dumps({'error': 'UNKNOWN_SIDE_EFFECT' if started else 'PERMISSION_DENIED'}))
