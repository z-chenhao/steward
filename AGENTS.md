# Repository Guidelines

## Project Structure & Module Organization

Steward’s Go entry point is `cmd/steward/`; domain packages and adapters live in `internal/`. Keep `internal/app/` focused on composition. React/TypeScript sources live in `web/src/`; generated assets in `internal/interaction/web/assets/`. Model protocols live in `internal/llm/provider/`; the isolated RPC client/server share `internal/llm/bridge/`. The Node execution sidecar lives in `runtime/javascript/`; `provider-bridge/` retains the compatibility launcher and protocol fixtures. `guest-browser/` contains browser checks; `tests/{system,harness,fixtures}/` holds system flows, Python regressions, and fixtures. Operational helpers belong in `scripts/`.

## Build, Test, and Development Commands

Use Go 1.26.10+, Node 24.21.0 (`.node-version`), Python 3.10.12+, bash, and a C compiler.

- `make setup`: install pinned dependencies.
- `make build` / `make run`: compile `bin/steward` / build and start locally.
- `make test lint`: run Go, Node, browser, frontend-output, Python, formatting, type, documentation, and workflow checks.
- `make test-prompts`: check Main/Root/Child system ordering, frozen inheritance and Root progress projection.
- `make test-e2e E2E_EVIDENCE=evidence/my-change`: run core system flows; always choose a fresh evidence directory.
- `make test-full` / `make test-race`: extended system coverage / Go race checks.
- `make audit`: query current Go/npm advisories; requires network access.
- `python3 scripts/check_docs.py`: validate documentation-only changes.

## Coding Style & Naming Conventions

Use `gofmt`, tabs, lowercase Go packages, and interfaces defined by consumers. Keep adapters within domain subpackages. JavaScript/TypeScript uses two-space indentation, camelCase functions, PascalCase React components, and kebab-case component filenames. Linting includes `go vet`, pinned Staticcheck, TypeScript, and Node syntax checks.

## Testing Guidelines

Use Go `testing` (`*_test.go`, beside sources), Node `node:test` (`*.test.mjs`), and Python `unittest` (`test_*.py`). Coverage targets observable scenarios, without a fixed percentage. Run focused checks first; cross-module changes require `make test lint` and relevant system gates. Follow the [testing guide](docs/test-plans/product.md); report skipped/unrun checks.

Finish edits and focused regressions before starting final acceptance. Keep the source and binary fixed during a gate; changes invalidate that gate's evidence and require a fresh run of the affected checks.

Use disposable `STEWARD_DATA_ROOT` directories. Close Python SQLite connections with `contextlib.closing`; use read transactions for consistent cross-table predicates.

## Agent Context Changes

Follow the [context contract](docs/designs/memory-context.md#recall-ranking-budget-and-recovery). For new policies, order the first request as system → history → recall → current user input. Freeze automatic recall by logical input identity, not model step; tool continuations preserve the exact message prefix and append exchanges unless compaction is required. Per-generation budget checks and request freezing remain necessary.

Context changes must verify message order, byte-stable continuation prefixes, recall reuse despite corpus/provider changes, refresh on a new input identity, and frozen replay. Keep UI state and retrieved history scoped as evidence. Run `make test-prompts` and the owning store/observability regressions; new regression tests must remain included in the required CI checks.

## Memory and Recall Changes

Follow the [memory/recall invariants](docs/designs/memory-context.md#memory-and-recall-invariants). Fix extraction, provenance, lifecycle and retrieval in the owning code; prompt wording alone is not a data-integrity fix. Conversation is the original evidence, not a Memory record. Keep private Curator proposals behind Host validation, durable receipts, privacy fences and model-usage authorization.

Add behavioral regressions for the reproduced failure, including negative cases. Memory/Recall changes must run `make test-memory` and both `memory-maintenance` and `recall` system gates; the required PR CI must keep those gates and their race checks enabled. Preserve historical frozen requests and unrelated local work. Forward migrations must also update the shared system-fixture schema expectation; keep its migration-catalog consistency regression in ordinary harness checks.

Worker evidence pagination must verify exact-page completion and continuation without dropped or replayed receipts. Validate memory argument diagnostics through accepted native and CodeMode receipts, not only schema/parser helpers.

## UI Component Changes

Before changing Web UI, read [design.md](design.md), especially the [global component contract](design.md#全局组件契约). It is the single source of visual and interaction rules; do not maintain a separate design specification here.

- Use the shared primitives in `web/src/components/ui/` across Settings, Chat, Tasks, Developer and Intelligent UI. Extend the owning shared component when needed; do not create page-specific selectors, menus, switches or control skins.
- Keep Steward typography, page layout and branding when adopting or replacing components, unless the user explicitly requests those changes. Follow the documented OpenIntelligentUI protocol and extension boundaries.
- Preserve each feature's validation, persistence, authorization and native fallback semantics. A visual replacement does not authorize changing autosave or explicit-save behavior.
- Run the existing control-ownership/build checks and relevant behavior regressions. Review desktop/mobile and light/dark screenshots for visual changes. Update `design.md` and the owning regression when an approved design rule changes; retain third-party notices.

## Commit & Pull Request Guidelines

Use imperative subjects with customary prefixes, e.g. `feat(web): improve task navigation`, `fix:`, or `refactor:`. Keep PRs focused. Follow the [PR template](.github/pull_request_template.md): describe behavior, link existing issues, record exact validation/evidence, and identify compatibility implications and residual risks. Small fixes need no issue.

Update the [Feature Map](docs/feature-map.md), owning feature, design, and test plan when behavior changes. [Documentation policy](docs/README.md#强制维护规则) requires current topics; keep development logs and execution results in PRs or CI artifacts.

Local deployment is not delivery to `main`. Before preparing a PR, inventory all
changes belonging to the requested behavior, including untracked sources,
templates, generated assets, documentation and regressions; preserve unrelated
work separately. Record any requested fixes left outside the PR explicitly.
Before replacing a local deployment with `main`, compare its source revision and
local-only fixes with the merged PRs. Preserve and report any difference; do not
claim those fixes are included merely because the new server starts. Verify the
merged revision and the actual served assets, then exercise the affected UI.

## Security & Agent Instructions

Follow the [engineering workflow](docs/features/engineering.md), [runtime contracts](docs/designs/runtime-contracts.md), and [governance](GOVERNANCE.md). Preserve approvals, frozen snapshots, and durable replay. Store credentials only in SecretStore. Releases require explicit authorization; remote branch deletion requires governance checks and exact-head leases.

Derive README/favicon artwork from the current Ghost atlas; `web/artwork/source/` is build input. Preserve third-party notices.

Connector implementations must be Steward-owned. Do not add external agent SDKs, vendor plugin implementations, or introduce their runtime-specific names. Reference behavior and public protocols while implementing adapters locally; general-purpose parsing libraries are allowed. Preserve required third-party attribution for retained code.
