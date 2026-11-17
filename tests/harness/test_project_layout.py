from pathlib import Path
import shutil
import sys
import tempfile
import subprocess
import unittest

from check_docs import ROOT, broken_links, documentation_policy_errors, unindexed_docs


class ProjectLayoutTests(unittest.TestCase):
    def test_documentation_policy_rejects_logs_but_preserves_current_contracts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs/designs').mkdir(parents=True)
            current = root / 'docs/designs/protocol.md'
            current.write_text('''# Current protocol
## MCP 2025-03-26 compatibility
MCP `2025-03-26` remains a supported wire format.
## Compatibility with old snapshots
Schema 28 does not rewrite frozen requests.
```md
## Changelog
## 2026-11-03 Review
```
''')
            self.assertEqual(list(documentation_policy_errors(root)), [])
            (root / 'docs/reviews').mkdir()
            (root / 'docs/reviews/2026-11-03-review.md').write_text('# Review\n')
            current.write_text(current.read_text() + '\nChangeLog\n---------\n')
            errors = list(documentation_policy_errors(root))
            self.assertEqual(len(errors), 3, errors)
            self.assertTrue(any('no review or archive directories' in e for e in errors))
            self.assertTrue(any('stable topic filename' in e for e in errors))
            self.assertTrue(any("log heading 'ChangeLog'" in e for e in errors))

    def test_dated_topic_filenames_cannot_hide_in_an_allowed_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            directory = root / 'docs/designs'
            directory.mkdir(parents=True)
            for filename in ('2026-11-03-review.md', '2026_11_03_review.md',
                             'review_2026-11-03.md'):
                with self.subTest(filename=filename):
                    path = directory / filename
                    path.write_text('# Review\n')
                    self.assertEqual(list(documentation_policy_errors(root)), [
                        f'docs/designs/{filename}: use a stable topic filename, not a date'])
                    path.unlink()

    def test_checker_cli_enforces_current_documentation_policy(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'scripts').mkdir()
            shutil.copy2(ROOT / 'scripts/check_docs.py', root / 'scripts/check_docs.py')
            (root / 'docs/designs').mkdir(parents=True)
            (root / 'README.md').write_text('[Design](docs/designs/current.md)')
            doc = root / 'docs/designs/current.md'
            for heading in ('Changelog', '2026-11-03 Review', '历史实现进度快照'):
                with self.subTest(heading=heading):
                    doc.write_text(f'# Current design\n\n## {heading}\n')
                    result = subprocess.run([sys.executable, str(root / 'scripts/check_docs.py')],
                                            capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                    self.assertIn('log heading', result.stderr)

    def test_picture_sources_and_html_images_are_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text('''# Artwork
<picture><source media="(prefers-color-scheme: dark)" srcset="missing-dark.png">
<img src="missing-light.png" alt="Ghost"></picture>
<img src="https://example.test/image.png">
`<img src="example.png">`
''')
            self.assertEqual(list(broken_links(root)), [
                'README.md: missing missing-dark.png',
                'README.md: missing missing-light.png'])

    def test_frontend_build_tracks_ui_sources_but_not_documentation(self):
        # Exercise Tailwind itself: docs must not change CSS, UI classes must.
        for directory, suffix, should_pass in ((ROOT / 'web', '.md', True),
                                                (ROOT / 'web/src', '.tsx', False)):
            with self.subTest(directory=directory):
                with tempfile.NamedTemporaryFile(mode='w', prefix='content-scope-probe-',
                                                 suffix=suffix, dir=directory, delete=False) as probe:
                    probe.write('<div className="p-[173.219px]">content scope</div>')
                    name = Path(probe.name)
                try:
                    result = subprocess.run(['npm', 'run', 'check', '--prefix', 'web'],
                                            cwd=ROOT, text=True, capture_output=True, timeout=30)
                    if should_pass:
                        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    else:
                        self.assertNotEqual(result.returncode, 0)
                        self.assertIn('Stale embedded UI asset: style-base.css', result.stderr)
                finally:
                    name.unlink()

    def test_nested_list_link_is_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs').mkdir()
            (root / 'docs/guide.md').write_text('# Guide\n')
            (root / 'README.md').write_text('''- Navigation
    - [broken](missing.md)
      [anchor](docs/guide.md#gone)
    - [guide](docs/guide.md#guide)
''')
            self.assertEqual(list(broken_links(root)), [
                'README.md: missing missing.md',
                'README.md: missing anchor docs/guide.md#gone'])
            self.assertEqual(list(unindexed_docs(root)), [])

    def test_tab_indentation_distinguishes_nested_lists_from_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text(
                '- Navigation\n\t- [broken](missing.md)\n\n'
                'Outside list\n\n\t- [code](missing-code.md)\n')
            self.assertEqual(list(broken_links(root)), ['README.md: missing missing.md'])

    def test_list_indentation_preserves_prose_and_excludes_code(self):
        for marker in ('-', '1.', '123)'):
            with self.subTest(marker=marker), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                indent = ' ' * (len(marker) + 1)
                (root / 'README.md').write_text(
                    f'{marker} Navigation\n\n'
                    f'{indent}[continuation](missing-continuation.md)\n\n'
                    f'{indent}    - [code](missing-code.md)\n\n'
                    f'{indent}~~~md\n'
                    f'{indent}[fenced](missing-fenced.md)\n'
                    f'{indent}~~~\n\n'
                    f'{indent}- Nested\n\n'
                    f'{indent}      [code](missing-nested-code.md)\n\n'
                    f'{indent}- [sibling](missing-sibling.md)\n\n'
                    'Outside list\n\n'
                    '    - [code](missing-outside-code.md)\n')
                self.assertEqual(list(broken_links(root)), [
                    'README.md: missing missing-continuation.md',
                    'README.md: missing missing-sibling.md'])

    def test_inline_code_is_not_an_html_anchor(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text('''# Here
`<a id="gone"></a>` ``<a name='also-gone'>`</a>``
[broken](#gone) [also broken](#also-gone)
''')
            self.assertEqual(list(broken_links(root)), [
                'README.md: missing anchor #gone',
                'README.md: missing anchor #also-gone'])

    def test_checker_cli_rejects_list_links_and_example_anchors(self):
        fixtures = (
            ('- Navigation\n    - [broken](missing.md)\n', 1, 'missing missing.md'),
            ('# Here\n`<a id="gone"></a>`\n[broken](#gone)\n',
             1, 'missing anchor #gone'),
            ('# A `Code` Title\n<a id="real"></a>\n<a name="legacy"></a>\n'
             '[heading](#a-code-title) [real](#real) [named](#legacy)\n'
             '\n    - [example](absent.md)\n'
             '\n- Examples\n\n      [indented](absent.md)\n'
             '\n  ```md\n  [fenced](absent.md)\n  ```\n', 0, ''),
        )
        for markdown, code, error in fixtures:
            with self.subTest(markdown=markdown), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / 'scripts').mkdir()
                shutil.copy2(ROOT / 'scripts/check_docs.py', root / 'scripts/check_docs.py')
                (root / 'README.md').write_text(markdown, encoding='utf-8')
                result = subprocess.run([sys.executable, str(root / 'scripts/check_docs.py')],
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, code, result.stdout + result.stderr)
                self.assertIn(error, result.stderr)
                if code == 0:
                    self.assertIn('Local documentation links and index: OK', result.stdout)

    def test_reference_links_check_local_files_and_heading_fragments(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs').mkdir()
            (root / 'docs/guide name.md').write_text('# Heading\n')
            (root / 'README.md').write_text('''# Here
[source][file] [heading][ ANCHOR   ref ] [self][] [shortcut] ![image][pic]
[file]: docs/missing.md#gone
[anchor ref]: <docs/guide name.md#gone> "Guide"
[self]: #gone
[shortcut]: missing-shortcut.md
[pic]: missing.png
''')
            self.assertEqual(list(broken_links(root)), [
                'README.md: missing docs/missing.md#gone',
                'README.md: missing anchor docs/guide name.md#gone',
                'README.md: missing anchor #gone',
                'README.md: missing missing-shortcut.md',
                'README.md: missing missing.png'])

    def test_unused_external_and_example_references_are_not_local_links(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text('''# Here
[web][external] [protocol][] [mail]
[inline](#here) [local][first]
`[code][missing]` ``[code ` span][missing]``
~~~md
[fenced][missing]
[hidden]: missing-hidden.md
~~~
    [indented][missing]
<!-- [comment][missing] -->
[hidden] [unresolved]
[external]: https://example.test/no#missing
[protocol]: //example.test/no#missing
[mail]: mailto:example@example.test
[inline]: missing-inline.md
[first]: #here
[FIRST]: missing-shadowed.md
[missing]: missing-example.md
[unused]: missing-unused.md#gone
''')
            self.assertEqual(list(broken_links(root)), [])

    def test_reference_links_reach_docs_with_relative_and_encoded_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs').mkdir()
            (root / 'README.md').write_text('[Guide][guide]\n[guide]: docs/guide%20name.md#heading')
            (root / 'docs/guide name.md').write_text('''# Heading
[home][root] [self][] [stable]
[root]: ../README.md
[self]: /docs/guide%20name.md#heading
[stable]: #explicit
<a id="explicit"></a>
''')
            self.assertEqual(list(broken_links(root)), [])
            self.assertEqual(list(unindexed_docs(root)), [])

    def test_markdown_moves_fail_for_missing_local_destinations(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs').mkdir()
            (root / 'docs/guide.md').write_text('# Heading\n')
            (root / 'README.md').write_text(
                '[guide](docs/guide.md#heading) [web](https://example.test/missing)\n'
                '```md\n[example](missing-example.md)\n```\n'
                '[broken](docs/moved.md)')
            self.assertEqual(list(broken_links(root)), ['README.md: missing docs/moved.md'])

    def test_agents_and_self_fragments_fail_for_missing_targets(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text('# Here\n[bad](#absent) [ok](#here)')
            (root / 'AGENTS.md').write_text('[bad](missing.md) [bad anchor](README.md#gone)')
            self.assertEqual(list(broken_links(root)), [
                'AGENTS.md: missing missing.md', 'AGENTS.md: missing anchor README.md#gone',
                'README.md: missing anchor #absent'])

    def test_heading_slugs_explicit_anchors_and_relative_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs').mkdir()
            (root / 'README.md').write_text('[guide](docs/guide%20name.md#repeat-2)')
            (root / 'docs/guide name.md').write_text('''# Repeat
## Repeat
## Repeat-1
## Repeat
## A `Code` &amp; **Title**!
中文 标题
---
<a id="stable"></a><a name='legacy'></a>
[collision](#repeat-1-1) [code](#a-code--title) [unicode](#%E4%B8%AD%E6%96%87-%E6%A0%87%E9%A2%98)
[explicit](#stable) [named](#legacy) [relative](../README.md)
[root](/docs/guide%20name.md#repeat) [space](<guide name.md#stable>)
''')
            self.assertEqual(list(broken_links(root)), [])
            (root / 'README.md').write_text('[bad](docs/guide%20name.md#repeat-3)')
            self.assertEqual(list(broken_links(root)), [
                'README.md: missing anchor docs/guide%20name.md#repeat-3'])

    def test_code_examples_and_external_fragments_are_not_local_links(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'README.md').write_text('''# Here
[web](https://example.test/no#missing) [protocol](//example.test/no#missing)
`[example](inline.md)` ``[backtick ` example](inline2.md)``
~~~md
[example](tilde.md)
~~~
````md
```
[example](long-fence.md)
````
    [example](indented.md)
<!-- [example](comment.md) -->
\\[escaped](escaped.md)
''')
            self.assertEqual(list(broken_links(root)), [])

    def test_injected_runtime_is_outside_repository_documentation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'AGENTS.md').write_text('''[bad](repository-missing.md)
<!-- BEGIN MULTICA-RUNTIME (auto-managed; do not edit) -->
[private runtime resource](references/private.md)
<!-- END MULTICA-RUNTIME -->
''')
            self.assertEqual(list(broken_links(root)), ['AGENTS.md: missing repository-missing.md'])

    def test_documentation_must_be_reachable_through_the_index(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'docs/guides').mkdir(parents=True)
            (root / 'README.md').write_text('[docs](docs/README.md)')
            (root / 'docs/README.md').write_text('[guide](guides/active.md)')
            (root / 'docs/guides/active.md').write_text('[back](../README.md)')
            orphan = root / 'docs/guides/orphan.md'
            orphan.write_text('lost guide')
            self.assertEqual(list(unindexed_docs(root)), [
                'docs/guides/orphan.md: unreachable from README.md; update the documentation index'])
            (root / 'docs/README.md').write_text('[active](guides/active.md) [found](guides/orphan.md)')
            self.assertEqual(list(unindexed_docs(root)), [])

    def test_node_pin_and_frontend_paths_have_one_consistent_value(self):
        import json
        version = (ROOT / '.node-version').read_text().strip()
        runtime = json.loads((ROOT / 'runtime/javascript/package.json').read_text())
        self.assertEqual(runtime['engines']['node'], version)
        self.assertIn('v' + version, (ROOT / 'install.sh').read_text())
        self.assertIn('v' + version, (ROOT / 'cmd/steward/start.go').read_text())
        self.assertIn('node-version-file: .node-version', (ROOT / '.github/actions/ci-inputs/action.yml').read_text())
        for path in ('Makefile', '.github/actions/ci-inputs/action.yml', 'web/build.mjs'):
            self.assertNotIn('internal/interaction/web/client', (ROOT / path).read_text())

    def test_packaging_stays_separate_from_the_required_pr_gate(self):
        workflow = (ROOT / '.github/workflows/release.yml').read_text()
        self.assertIn('--draft', workflow)
        self.assertIn('--verify-tag', workflow)
        self.assertNotIn('pull_request:', workflow)
        self.assertIn('node-version-file: .node-version', workflow)

    def test_startup_owns_node_compile_cache_and_temporary_files(self):
        from verify_startup import install_environment
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp)
            root = parent / 'owned'
            root.mkdir()
            env = install_environment(root)
            subprocess.run(['node', '-e', "require('node:module').enableCompileCache(); require('node:fs')"],
                           env=env, check=True, capture_output=True)
            subprocess.run(['npm', '--version'], env=env, check=True, capture_output=True)
            self.assertEqual(list(parent.iterdir()), [root], 'Node/npm left cache outside its fixture')
            self.assertEqual(Path(env['TMPDIR']).parent, root)
            self.assertEqual(Path(env['NODE_COMPILE_CACHE']).parent, root)
