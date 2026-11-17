"""Fixed CAS/atomic file commit. The Host supplies the approved change."""


def file_change(root, change):
    global started
    operation, path = change['operation'], change['path']
    if operation not in ('edit', 'write'):
        raise ValueError()
    os.makedirs(root, mode=0o700, exist_ok=True)
    owner = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    directory, temporary = None, None
    try:
        fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
        directory, name = parent(root, path, operation == 'write')
        data, mode = None, 0o600
        try:
            fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                         dir_fd=directory)
        except FileNotFoundError:
            if operation != 'write' or 'before_hash' in change:
                raise
        else:
            with os.fdopen(fd, 'rb') as file:
                regular(file.fileno())
                mode = stat.S_IMODE(os.fstat(file.fileno()).st_mode) & 0o777
                data = file.read(MAX_FILE + 1)
                if len(data) > MAX_FILE:
                    raise PermissionError()
            if change.get('before_hash') != digest(data):
                raise PermissionError()
        if operation == 'edit':
            data.decode('utf-8')
            old = change['old_text'].encode('utf-8')
            start = change['start']
            if (not old or type(start) is not int or start < 0 or
                    data.count(old) != 1 or data[start:start + len(old)] != old):
                raise ValueError()
            replacement = data[:start] + change['new_text'].encode('utf-8') + data[start + len(old):]
        else:
            replacement = change['content'].encode('utf-8')
        if len(replacement) > MAX_FILE:
            raise ValueError()
        temporary = '.steward-write-' + os.urandom(16).hex()
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                     mode, dir_fd=directory)
        with os.fdopen(fd, 'wb') as file:
            file.write(replacement)
            file.flush()
            os.fsync(file.fileno())
        started = True
        if data is None:
            os.link(temporary, name, src_dir_fd=directory, dst_dir_fd=directory,
                    follow_symlinks=False)
            os.unlink(temporary, dir_fd=directory)
        else:
            os.rename(temporary, name, src_dir_fd=directory, dst_dir_fd=directory)
        temporary = None
        os.fsync(directory)
        return dict(path=path, hash=digest(replacement), bytes=len(replacement))
    finally:
        if temporary is not None:
            os.unlink(temporary, dir_fd=directory)
        if directory is not None:
            os.close(directory)
        os.close(owner)


try:
    request = json.load(sys.stdin)
    print(json.dumps({'value': file_change(request['root'], request['change'])},
                     ensure_ascii=True))
except BlockingIOError:
    print(json.dumps({'error': 'RESOURCE_LIMIT'}))
except FileNotFoundError:
    print(json.dumps({'error': 'UNKNOWN_SIDE_EFFECT' if started else 'NOT_FOUND'}))
except (ValueError, UnicodeError, KeyError):
    print(json.dumps({'error': 'UNKNOWN_SIDE_EFFECT' if started else 'INVALID_ARGUMENT'}))
except BaseException:
    print(json.dumps({'error': 'UNKNOWN_SIDE_EFFECT' if started else 'PERMISSION_DENIED'}))
