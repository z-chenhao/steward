#!/usr/bin/env python3
"""Explicit owner preparation of an immutable, offline Linux coding image.

Network is used only during preparation. No host mounts, sockets or credentials
enter the image; original source is removed after locked dependencies are cached.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
PACKAGES = ('provider-bridge', 'runtime/javascript', 'guest-browser', 'web')


def digest(path):
    with path.open('rb') as source:
        value = hashlib.sha256()
        for chunk in iter(lambda: source.read(1 << 20), b''):
            value.update(chunk)
        return value.hexdigest()



def source_paths():
    names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
    return sorted(name for name in names if name and ((ROOT / name).exists() or (ROOT / name).is_symlink()))


def snapshot_source(source):
    source.mkdir()
    inventory = []
    for name in source_paths():
        original, target = ROOT / name, source / name
        if original.is_symlink() or not original.is_file():
            raise RuntimeError('source inventory requires regular files: ' + name)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(original, target)
        copied = digest(target)
        if copied != digest(original):
            raise RuntimeError('source changed while copying: ' + name)
        inventory.append(dict(path=name, sha256=copied, executable=bool(original.stat().st_mode & 0o111)))
    return inventory


def prepare(evidence, cache, socket):
    evidence.mkdir(parents=True, exist_ok=False)
    cache.mkdir(parents=True, exist_ok=True)
    report = dict(outcome='failed', steps=[], checkpoints=[])
    started = time.monotonic()
    cid, docker = None, None
    config = tempfile.TemporaryDirectory(prefix='steward-image-docker-')
    environment = dict(os.environ)
    for name in ('DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH', 'DOCKER_AUTH_CONFIG', 'DOCKER_CUSTOM_HEADERS'):
        environment.pop(name, None)

    def run(name, command, timeout=180, env=None, cwd=ROOT, capture=False):
        step = dict(name=name, command=command, outcome='failed')
        report['steps'].append(step)
        at = time.monotonic()
        print('coding image: ' + name, flush=True)
        try:
            with (evidence / (name + '.log')).open('w') as log:
                with subprocess.Popen(command, cwd=cwd, env=env or environment, stdout=subprocess.PIPE if capture else log,
                                      stderr=log, text=True, start_new_session=True) as child:
                    try:
                        output, _ = child.communicate(timeout=timeout)
                    finally:
                        try:
                            os.killpg(child.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        child.wait()
                    step['returncode'] = child.returncode
                    if capture:
                        log.write(output or '')
                    if child.returncode:
                        raise RuntimeError('coding image stage failed: ' + name)
            step['outcome'] = 'passed'
            return (output or '').strip() if capture else None
        finally:
            step['seconds'] = time.monotonic() - at

    def download(name, url, destination, expected=None, size=None):
        if not destination.exists() or expected is None or digest(destination) != expected:
            run(name, ['curl', '-q', '--fail', '--location', '--silent', '--show-error', '--max-time', '600', url, '-o', str(destination)], timeout=610)
        actual = digest(destination)
        if expected is not None and actual != expected or size is not None and destination.stat().st_size != size:
            raise RuntimeError('artifact checksum/size mismatch: ' + name)
        return dict(url=url, sha256=actual, bytes=destination.stat().st_size)

    def checkpoint(label):
        image = run('checkpoint-' + label, docker + ['commit', cid], capture=True)
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', image):
            raise RuntimeError('missing immutable image identity')
        report['checkpoints'].append(dict(stage=label, image=image))
        (evidence / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')
        return image


    try:
        if not Path(socket).is_absolute() or str(Path(socket)) != socket:
            raise ValueError('a canonical local Unix socket is required')
        client = Path(shutil.which('docker') or '/missing-docker').resolve(strict=True)
        docker = [str(client), '--config', config.name, '--host', 'unix://' + socket]
        report['cli'] = dict(path=str(client), sha256=digest(client))
        version = json.loads(run('docker-version', docker + ['version', '--format', '{{json .}}'], capture=True))
        report['docker_version'] = version
        arch = version['Server']['Arch']
        if version['Server']['Os'] != 'linux' or arch not in ('arm64', 'amd64'):
            raise ValueError('supported local Linux Engine required')
        pinned = json.loads((ROOT / 'scripts/coding-image-inputs.json').read_text())['debian'][arch]
        rootfs = cache / ('debian-' + pinned['sha256'] + '.tar.gz')
        report['debian'] = download('debian-rootfs', pinned['url'], rootfs, pinned['sha256'], pinned['bytes'])
        base = run('import-base', docker + ['image', 'import', '--platform', 'linux/' + arch, str(rootfs)], capture=True)
        if not re.fullmatch(r'sha256:[0-9a-f]{64}', base):
            raise RuntimeError('missing immutable base image identity')
        report['base'] = base
        node_version = (ROOT / '.node-version').read_text().strip()
        go_version = re.search(r'^go ([0-9.]+)$', (ROOT / 'go.mod').read_text(), re.M)[1]
        go_root = Path(run('host-go-root', ['go', 'env', 'GOROOT'], capture=True))
        go = str(go_root / 'bin/go')
        actual_go = run('host-go-version', [go, 'version'], capture=True)
        found = re.search(r'go([0-9]+\.[0-9]+\.[0-9]+)', actual_go)
        if found is None or tuple(map(int, found[1].split('.'))) < tuple(map(int, go_version.split('.'))):
            raise RuntimeError('repository-supported host Go is required')
        module = 'golang.org/toolchain@v0.0.1-go' + go_version + '.linux-' + arch
        module_env = dict(environment, GOENV='off', GOTOOLCHAIN='local', GOPROXY='https://proxy.golang.org', GOSUMDB='sum.golang.org', GOPRIVATE='', GONOPROXY='', GONOSUMDB='', GOMODCACHE=str(cache / 'go-toolchains'))
        toolchain = json.loads(run('linux-go-toolchain', [go, 'mod', 'download', '-json', module], env=module_env, cwd=cache, capture=True, timeout=600))
        if not toolchain.get('Sum', '').startswith('h1:') or not Path(toolchain['Dir']).is_dir():
            raise RuntimeError('verified official Go toolchain missing')
        report['go_toolchain'] = toolchain
        node_arch = {'arm64': 'arm64', 'amd64': 'x64'}[arch]
        filename = f'node-v{node_version}-linux-{node_arch}.tar.xz'
        checksums = cache / ('node-v' + node_version + '-SHASUMS256.txt')
        node_base = 'https://nodejs.org/dist/v' + node_version + '/'
        download('node-checksums', node_base + 'SHASUMS256.txt', checksums)
        expected = [line.split()[0] for line in checksums.read_text().splitlines() if line.split()[-1] == filename]
        if len(expected) != 1:
            raise RuntimeError('official Node checksum missing or ambiguous')
        node = cache / filename
        report['node'] = download('linux-node-toolchain', node_base + filename, node, expected[0])
        names = source_paths()
        source = evidence / 'source'
        inventory = snapshot_source(source)
        (evidence / 'source-inventory.json').write_text(json.dumps(inventory, indent=2) + '\n')
        report['source_inventory_sha256'] = digest(evidence / 'source-inventory.json')
        name = 'steward-image-prepare-' + str(time.time_ns())
        report['container_name'] = name
        (evidence / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')
        command = docker + ['create', '--pull', 'never', '--name', name, '--label', 'ai.steward.coding-preparation=' + name, '--cpus', '4', '--memory', '8g']
        for value in ('PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin', 'GOENV=off', 'GOTOOLCHAIN=local', 'GOTELEMETRY=off', 'GOMODCACHE=/opt/steward/go-mod', 'GOCACHE=/opt/steward/go-build', 'npm_config_cache=/opt/steward/npm-cache', 'npm_config_update_notifier=false', 'PLAYWRIGHT_BROWSERS_PATH=/opt/steward/browsers'):
            command += ['--env', value]
        cid = run('create-preparation', command + [base, '/bin/sh', '-c', 'exec sleep 2147483647'], capture=True)
        if not re.fullmatch(r'[0-9a-f]{64}', cid):
            raise RuntimeError('missing original preparation container ID')
        report['container_id'] = cid
        (evidence / 'progress.json').write_text(json.dumps(report, indent=2) + '\n')
        run('start-preparation', docker + ['start', cid])
        run('system-runtime', docker + ['exec', '--env', 'DEBIAN_FRONTEND=noninteractive', cid, '/bin/sh', '-ceu',
            'apt-get update && apt-get install -y --no-install-recommends ca-certificates xz-utils bubblewrap python3 sqlite3 make git gcc libc6-dev'], timeout=900)
        checkpoint('system')
        run('toolchain-directories', docker + ['exec', cid, 'mkdir', '-p', '/usr/local/go', '/opt/steward/source'])
        run('copy-go', docker + ['cp', toolchain['Dir'] + '/.', cid + ':/usr/local/go/'])
        run('copy-node', docker + ['cp', str(node), cid + ':/tmp/node.tar.xz'])
        run('install-toolchains', docker + ['exec', cid, '/bin/sh', '-ceu', 'chmod a+rx /usr/local/go/bin/* /usr/local/go/pkg/tool/linux_*/*; tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1; rm /tmp/node.tar.xz; go version; node --version'])
        run('copy-source', docker + ['cp', str(source) + '/.', cid + ':/opt/steward/source/'])
        run('go-dependencies', docker + ['exec', '--workdir', '/opt/steward/source', cid, 'go', 'mod', 'download'], timeout=600)
        checkpoint('go-dependencies')
        for i, component in enumerate(PACKAGES):
            run('node-dependencies-' + str(i), docker + ['exec', '--workdir', '/opt/steward/source', cid, 'npm', 'ci', '--prefix', component, '--ignore-scripts', '--prefer-offline', '--fetch-retries=0', '--fetch-timeout=30000'], timeout=600)
        checkpoint('node-dependencies')
        cli = '/opt/steward/source/guest-browser/node_modules/playwright-core/cli.js'
        run('browser-libraries', docker + ['exec', cid, 'node', cli, 'install-deps', 'chromium'], timeout=900)
        checkpoint('browser-libraries')
        run('locked-browser', docker + ['exec', cid, 'node', cli, 'install', 'chromium'], timeout=600)
        run('actual-browser-doctor', docker + ['exec', '--workdir', '/opt/steward/source', cid, 'node', 'scripts/setup_browser.cjs'])
        run('cold-steward-build', docker + ['exec', '--workdir', '/opt/steward/source', cid, 'go', 'build', '-p', '2', '-trimpath', '-o', '/opt/steward/prepared-steward', './cmd/steward'], timeout=900)
        run('seal-inputs', docker + ['exec', cid, '/bin/sh', '-ceu', '''
for component in provider-bridge runtime/javascript guest-browser web; do
 mkdir -p /opt/steward/deps/"$component"
 if [ -d /opt/steward/source/"$component"/node_modules ]; then mv /opt/steward/source/"$component"/node_modules /opt/steward/deps/"$component"/node_modules; fi
done
mkdir -p /opt/steward/metadata
go version > /opt/steward/metadata/go-version.txt
node --version > /opt/steward/metadata/node-version.txt
gcc --version > /opt/steward/metadata/gcc-version.txt
dpkg-query -W > /opt/steward/metadata/debian-packages.txt
rm -rf /opt/steward/source /opt/steward/prepared-steward
chmod -R a+rX /opt/steward/deps /opt/steward/go-mod /opt/steward/go-build /opt/steward/browsers
'''])
        if source_paths() != names:
            raise RuntimeError('source inventory changed during preparation')
        for item in inventory:
            original = ROOT / item['path']
            if not original.is_file() or digest(original) != item['sha256'] or bool(original.stat().st_mode & 0o111) != item['executable']:
                raise RuntimeError('source changed during preparation: ' + item['path'])
        image = checkpoint('ready')
        report.update(outcome='passed', image=image)
        (evidence / 'image.txt').write_text(image + '\n')
        print(image, flush=True)
        return report
    finally:
        interrupted = isinstance(sys.exc_info()[1], (KeyboardInterrupt, SystemExit))
        report['interrupted'] = interrupted
        try:
            if cid and docker:
                try:
                    if report['outcome'] != 'passed' and not interrupted:
                        checkpoint('incomplete')
                finally:
                    try:
                        run('cleanup-preparation', docker + ['rm', '--force', cid], timeout=3 if interrupted else 30)
                        report['owned_cleanup'] = 'confirmed'
                    except BaseException:
                        report.update(outcome='failed', owned_cleanup='unconfirmed')
                        raise
        finally:
            config.cleanup()
            report['wall_seconds'] = time.monotonic() - started
            (evidence / 'report.json').write_text(json.dumps(report, indent=2) + '\n')


def stop(*_):
    raise KeyboardInterrupt


def main():
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, stop)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence', type=Path, required=True)
    parser.add_argument('--cache', type=Path, default=ROOT / 'evidence/coding-image-inputs')
    parser.add_argument('--socket', default=os.environ.get('STEWARD_TEST_PROJECT_CONTAINER_SOCKET', '/var/run/docker.sock'))
    args = parser.parse_args()
    prepare(args.evidence.resolve(), args.cache.resolve(), args.socket)


if __name__ == '__main__':
    main()
