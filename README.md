# Steward

[![CI](https://github.com/z-chenhao/steward/actions/workflows/ci.yml/badge.svg)](https://github.com/z-chenhao/steward/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="internal/interaction/web/assets/brand/ghost-ivory.png">
  <img src="internal/interaction/web/assets/brand/ghost-charcoal.png" alt="Ghost, the Steward assistant" width="160" height="160">
</picture>

Steward is a local personal agent with terminal and Web interfaces. Its assistant,
Ghost, can answer questions, use tools and work on delegated tasks. You can inspect
progress, approve consequential actions and follow work across process restarts.
Conversation, tasks, tool calls and approvals share a durable execution core.

## What it does

- **Conversation and tasks:** one ongoing conversation, durable background work,
  child tasks, schedules, progress and delivered artifacts.
- **Tools with explicit authority:** isolated file/command execution, MCP tools,
  JavaScript orchestration, frozen approvals and recovery of uncertain outcomes.
- **Model connections:** explicitly select and probe a local OpenAI-compatible
  endpoint or a supported account/API connection. No model is chosen implicitly.
- **Inspection and control:** Web settings, task views, execution traces, memory,
  notifications and optional Feishu or macOS Computer integrations.

See the [Feature Map](docs/feature-map.md) for implemented behavior and limitations.
Web settings layout and controls follow [design.md](design.md).
Steward is under active development. Deterministic tests validate software
mechanisms; they do not certify model quality or every external provider.

## Install and start

From a checkout, install **Go 1.26.11+**, **Node.js 24.21.0** (with npm),
**Python 3.10.12+**, bash and a C compiler. Linux file/command isolation also needs
Bubblewrap and unprivileged user namespaces; macOS uses Seatbelt. Windows is not
currently supported by the installer. Optional OCR needs Tesseract and language data.

```sh
git clone https://github.com/z-chenhao/steward.git
cd steward
./install.sh
~/.local/bin/steward
```

First launch in a terminal guides model setup. Choose a provider and confirm the model; custom compatible endpoints are probed, while ChatGPT account models use their authenticated catalog. `/setup` repairs a connection, `/models`
lists profiles and `/quit` exits. ChatGPT sign-in and other saved credentials use
private local files on all supported platforms. Explicit API-key environment
references remain available. Never paste credentials into YAML or issues.

The installer creates a versioned runtime under `~/.local/lib/steward` and a
launcher under `~/.local/bin`. Data lives separately in `~/.local/share/steward`;
reinstalling preserves it. No daemon or shell-profile modification is installed.
Use `./install.sh --prefix /absolute/path` for another installation location.

```sh
~/.local/bin/steward doctor   # Read-only runtime diagnostics
```

The [installation guide](docs/features/installation.md) covers native archives,
Web configuration, data-root selection, supported platforms and recovery.
The Web interface binds to loopback and is intended for an owner's local workspace;
read the security and deployment guidance before exposing anything externally.

## Develop and verify

For development, install the prerequisites above, then:

```sh
make setup                 # Locked npm packages and Chromium
make build
make test lint
make test-e2e E2E_EVIDENCE=evidence/my-first-check
make run                   # Foreground terminal session
```

On a clean Ubuntu development host, browser system libraries can be prepared with
`python3 scripts/setup.py --part browser --with-deps` after installing packages.
Keep each system-test evidence directory fresh. Tests use disposable data roots
and scripted peers; ordinary regressions do not require paid model accounts.

| Command | Purpose |
| --- | --- |
| `make help` | Available development commands |
| `make test lint` | Go, Node, browser, Python harness, generated assets, types, docs and workflow checks |
| `make test-e2e` | Core deterministic system gates |
| `make test-full` | Complete deterministic system suite |
| `make test-race` | All Go packages, including the partitioned SQLite inventory |
| `make audit` | Current Go/npm vulnerability advisories; requires network access |

See [CONTRIBUTING.md](CONTRIBUTING.md) for first contributions and
[the testing guide](docs/test-plans/product.md) for focused commands and coverage.

## Repository map

| Location | Responsibility |
| --- | --- |
| `cmd/steward/` | CLI entry point |
| `internal/` | Go domains and adapters; `app` composes them, tests live beside code |
| `provider-bridge/`, `runtime/javascript/` | Product Node runtimes and their protocol tests |
| `web/src/` | Frontend sources; generated bundles are embedded under `internal/interaction/web/assets/` |
| `web/artwork/` | Current artwork sources and derivation; README/favicon share the production avatar's first frame |
| `guest-browser/` | Browser test dependencies and contract checks |
| `tests/system/`, `tests/harness/`, `tests/fixtures/` | System flows, harness regressions and synthetic fixtures |
| `scripts/` | Setup, packaging and maintenance utilities |
| `.github/` | CI, dependency updates and contribution templates |
| `docs/` | Feature requirements, architecture and test plans |

The Go module retains `github.com/z-chenhao/assistant` for import compatibility;
the canonical repository URL is `https://github.com/z-chenhao/steward`.

## Contribute, get help and report security issues

- [Contributing](CONTRIBUTING.md) · [Code of conduct](CODE_OF_CONDUCT.md)
- [Support](SUPPORT.md) · [Project governance](GOVERNANCE.md)
- [Security and private reporting](SECURITY.md)
- [Documentation index](docs/README.md) · [Architecture](docs/designs/product-overview.md) · [UI design](design.md)

## License

Steward is licensed under [Apache-2.0](LICENSE). Incorporated components and fonts
retain their own licenses; see [third-party notices](THIRD_PARTY_NOTICES.md) and
[NOTICE](NOTICE). The project license does not grant trademark rights.

Memory keys and ordinary credentials use private files under the data root (`secrets/`, directory 0700 and files 0600). Default startup does not require the system keychain. OAuth credentials retain their existing `account-secrets/` directory. Keep keys separately when backing up encrypted memories.
