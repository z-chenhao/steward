"""Build the actual frozen 1.0.0 producer for retained-graph regression tests."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main(output):
    fixture = Path(__file__).resolve().parent
    repo = fixture.parents[3]
    source = repo / 'internal/plugin/memoryinspector'
    resources = {
        'memory.html': ('managed-memory-v1.html.txt', 'memoryViewSource'),
        'memory.css': ('memory.css.txt', 'memoryStylesheetSource'),
        'copy.json': ('localization-v1.json', 'memoryLocalizationSource'),
        'memory-worker.js': ('worker-v1.js.txt', 'memoryWorkerSource'),
    }
    (output / 'views').mkdir()
    with tempfile.TemporaryDirectory() as temporary:
        temp = Path(temporary)
        presentation = (fixture / 'presentation.go.txt').read_text()
        for target, (original, variable) in resources.items():
            data = (fixture / original).read_text()
            declaration = f'//go:embed view/{target}\nvar {variable} string'
            if presentation.count(declaration) != 1:
                raise ValueError(declaration)
            presentation = presentation.replace(declaration, f'var {variable} = {json.dumps(data, ensure_ascii=False)}')
            shutil.copyfile(fixture / original, output / 'views' / target)
        (temp / 'presentation.go').write_text(presentation)
        overlay = {str(source / 'plugin.go'): str(fixture / 'plugin.go.txt'),
                   str(source / 'presentation.go'): str(temp / 'presentation.go')}
        (temp / 'overlay.json').write_text(json.dumps({'Replace': overlay}))
        subprocess.run(['go', 'build', '-overlay', temp / 'overlay.json', '-o', output / 'plugin',
                        './cmd/plugins/memory-inspector'], cwd=repo, check=True)
    shutil.copyfile(fixture / 'manifest.json', output / 'steward.plugin.json')
    subprocess.run(['go', 'run', './cmd/steward', 'plugin', 'seal', output], cwd=repo,
                   stdout=subprocess.DEVNULL, check=True)


if __name__ == '__main__':
    main(Path(sys.argv[1]).resolve())
