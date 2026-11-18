"""Exercise the owner preparation lifecycle with small local command fixtures."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import prepare_coding_image as prep


class CodingImageTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.repo = self.base / 'repo'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q'], cwd=self.repo, check=True)
        (self.repo / '.gitignore').write_text('/evidence/\n')
        patched = patch.object(prep, 'ROOT', self.repo)
        patched.start()
        self.addCleanup(patched.stop)

    def test_unignored_outputs_are_rejected_before_preparation(self):
        with self.assertRaisesRegex(ValueError, 'ignored output'):
            prep.prepare(self.repo / 'ordinary/output', self.base / 'cache', '/tmp/socket')
        self.assertFalse((self.repo / 'ordinary').exists())
        prep.check_output_path(self.repo / 'evidence' / 'run')
        prep.check_output_path(self.base / 'cache')
        with self.assertRaises(ValueError):
            prep.check_output_path(self.repo / '.git' / 'objects')

    def test_ready_publication_follows_container_cleanup(self):
        artifacts = self.repo / 'scripts'
        artifacts.mkdir()
        sha = hashlib.sha256(b'root').hexdigest()
        (artifacts / 'coding-image-inputs.json').write_text(json.dumps({
            'debian': {'arm64': {'url': 'fixture:root', 'sha256': sha, 'bytes': 4}}}))
        (self.repo / '.node-version').write_text('24.21.0')
        (self.repo / 'go.mod').write_text('go 1.26.11\n')
        executable = self.base / 'docker'
        executable.touch()
        original = subprocess.Popen
        ready = 'sha256:' + 'b' * 64
        for fail_cleanup in (True, False):
            with self.subTest(fail_cleanup=fail_cleanup):
                evidence = self.base / ('failed' if fail_cleanup else 'passed')
                commits = []

                def launch(command, *args, **kwargs):
                    if command[0] == 'git':
                        return original(command, *args, **kwargs)
                    value, code = '', 0
                    if command[0] == 'curl':
                        dest = Path(command[-1])
                        if 'SHASUMS256' in dest.name:
                            dest.write_text(hashlib.sha256(b'node').hexdigest() +
                                            '  node-v24.21.0-linux-arm64.tar.xz\n')
                        else:
                            dest.write_bytes(b'root' if 'debian' in dest.name else b'node')
                    elif command[0] == str(executable):
                        action = command[5]
                        if action == 'version':
                            value = json.dumps({'Server': {'Arch': 'arm64', 'Os': 'linux'}})
                        elif action == 'image':
                            value = 'sha256:' + 'a' * 64
                        elif action == 'create':
                            value = 'c' * 64
                        elif action == 'commit':
                            commits.append(action)
                            value = ready
                        elif action == 'rm':
                            self.assertFalse((evidence / 'image.txt').exists())
                            code = int(fail_cleanup)
                    elif command[1] == 'env':
                        value = str(self.base)
                    elif command[1] == 'version':
                        value = 'go version go1.26.11'
                    elif command[1] == 'mod':
                        value = json.dumps({'Sum': 'h1:fixture', 'Dir': str(self.base)})
                    else:
                        self.fail(command)
                    return original([sys.executable, '-c', 'import sys; print(sys.argv[1]); sys.exit(int(sys.argv[2]))',
                                     value, str(code)], *args, **kwargs)

                with patch.object(prep.shutil, 'which', return_value=str(executable)), patch.object(subprocess, 'Popen', launch):
                    if fail_cleanup:
                        with self.assertRaisesRegex(RuntimeError, 'cleanup-preparation'):
                            prep.prepare(evidence, self.base / 'cache', '/tmp/socket')
                    else:
                        prep.prepare(evidence, self.base / 'cache', '/tmp/socket')
                report = json.loads((evidence / 'report.json').read_text())
                self.assertEqual(commits, ['commit'])
                self.assertEqual(report['outcome'], 'failed' if fail_cleanup else 'passed')
                self.assertEqual((evidence / 'image.txt').exists(), not fail_cleanup)
                if not fail_cleanup:
                    self.assertEqual((evidence / 'image.txt').read_text().strip(), ready)
