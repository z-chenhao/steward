# Contributing to Steward

Bug fixes, documentation, tests and focused improvements are welcome. Read the
[code of conduct](CODE_OF_CONDUCT.md) and [governance](GOVERNANCE.md). Use an issue
for a significant proposal; a small fix does not require an issue number.

## First contribution

1. Fork the repository and create a topic branch from current `main`.
2. Install Go 1.26.11+, Node 24.21.0, Python 3.10.12+, bash and a C compiler.
   Linux isolation needs Bubblewrap/user namespaces; optional OCR tests need
   Tesseract with English and Simplified Chinese data. See [installation](docs/features/installation.md).
3. Run `make setup`, then `make test lint` to establish your baseline. On a fresh
   Ubuntu host, `python3 scripts/setup.py --part browser --with-deps` supplies
   browser libraries. Report missing native dependencies instead of weakening tests.
4. Read [AGENTS.md](AGENTS.md), the owning module and nearby tests. Change sources,
   regenerate affected frontend outputs, and add a regression for a reproduced bug.
5. Run focused checks and the relevant system gates with fresh evidence paths.
   Use disposable `STEWARD_DATA_ROOT` directories, never personal data.
6. Open a PR with the problem, resulting behavior, exact verification and remaining
   limits. Preserve unrelated changes. Maintainers review compatibility before merge.

## Where changes belong

The [repository map](README.md#repository-map) identifies implementation owners.
Keep `internal/app` focused on composition and adapters in their domain packages;
do not split the authoritative transaction just to shorten a large file.
UI sources and current artwork belong under `web/`; shipped bundles belong under
`internal/interaction/web/assets/`. Runtime prompts and sidecar bytes can affect
frozen snapshots, so changing them needs explicit compatibility review.

The maintained [engineering workflow](docs/features/engineering.md#contributing-working-on-a-change)
covers proportional checks, offline setup/cache recovery and release boundaries.
The [documentation policy](docs/README.md#强制维护规则) defines content ownership:
update current topics in place and keep review logs and execution results in PRs,
Git history or CI artifacts. The [Feature Map rules](docs/feature-map.md#文档维护规则)
define requirement status and acceptance maintenance.

Dependency update PRs are proposals, not automatic approvals. Inspect runtime
fingerprints, lockfiles, licenses and vulnerabilities; regenerate frontend outputs
when needed. `make audit` queries current advisories separately from deterministic CI.

By submitting a contribution, you intend it to be distributed under Apache-2.0.
Retain existing copyright and third-party notices; do not contribute material you
cannot license. Publishing a release remains a separate, explicitly authorized action.
