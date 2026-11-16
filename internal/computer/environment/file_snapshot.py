"""Fixed Host file-snapshot driver; private input for pure file methods."""


def file_snapshot(root, path):
    os.makedirs(root, mode=0o700, exist_ok=True)
    owner = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            directory, name = parent(root, path)
        except FileNotFoundError:
            return dict(exists=False)
        try:
            try:
                fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=directory)
            except FileNotFoundError:
                return dict(exists=False)
            with os.fdopen(fd, 'rb') as file:
                regular(file.fileno())
                data = file.read(MAX_FILE + 1)
                if len(data) > MAX_FILE:
                    raise PermissionError()
            return dict(exists=True, data=base64.b64encode(data).decode('ascii'),
                        hash=digest(data))
        finally:
            os.close(directory)
    finally:
        os.close(owner)


try:
    request = json.load(sys.stdin)
    print(json.dumps({'value': file_snapshot(request['root'], request['path'])},
                     ensure_ascii=True))
except BlockingIOError:
    print(json.dumps({'error': 'RESOURCE_LIMIT'}))
except (ValueError, UnicodeError, KeyError):
    print(json.dumps({'error': 'INVALID_ARGUMENT'}))
except BaseException:
    print(json.dumps({'error': 'PERMISSION_DENIED'}))
