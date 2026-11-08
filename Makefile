.PHONY: help setup install run build test test-prompts test-memory test-web-access test-connected-apps test-go test-node test-browser test-infra test-e2e test-full test-race lint lint-actions audit

.DEFAULT_GOAL := setup

help:
	@echo 'make setup       Install locked development dependencies'
	@echo 'make build/run   Build / start Steward'
	@echo 'make test lint   Run software tests and local quality checks'
	@echo 'make test-prompts Check Agent prompt assembly and frozen Worker inheritance'
	@echo 'make test-connected-apps Check app credentials, lifecycle, approvals and migrations with race detection'
	@echo 'make test-e2e    Run core flows (set E2E_EVIDENCE to a fresh path)'
	@echo 'make test-full   Run all deterministic system flows'
	@echo 'make test-race   Run the complete Go race inventory'
	@echo 'make audit       Check current Go/npm vulnerability advisories (network required)'
	@echo 'See CONTRIBUTING.md for prerequisites and focused checks.'

# Operations live in scripts; software tests live in tests. Model evals are external.
setup:
	python3 scripts/setup.py $(SETUP_FLAGS)

install:
	sh install.sh

build:
	go build -o bin/steward ./cmd/steward

run: build
	./bin/steward start

# Go tests currently contain both unit and component integration tests.
# Keep that explicit rather than relabeling the entire package suite as UT.
test: test-go test-node test-browser test-infra

test-prompts:
	go test ./internal/agent ./internal/agent/sqlite -run '^Test(MemoryCompactionPreservesMixedCodeBusinessReceipt|RecallCompleteSearchStillAllowsReadingCASMetadata|RecallRepeatedReadReusesVisibleEvidenceWithoutDuplicatingBody|RecallUnifiedVisibilityIncludesSealedReadsAndAutomaticEvidence|LayeredMemoryNavigationSelectsScopedLinksWithoutRewritingSources|RecallVisibleResultsRequireOwnedReceiptAndCompleteVisibleFrame|LayeredMemoryInjectedSnapshotFrozenSealedAndLoadedWithoutSearch|LayeredMemoryCodeReadProjectionIsStableScopedAndDoesNotRewriteReceipt|LayeredMemoryTranscriptReadIsScopedPagedSealedAndRevoked|ContextPromptBytes|ProviderToolUsesFrozenModelDeclarationAndKeepsLegacySchema|ContextRolesAndFrozenResources|AgentStartsWithOneFrozenSystemMessage|ChildInheritsOnlyFrozenSystemSections|SkillsUseSearchWithoutPromptCatalog|SplitSystemMessagesAreRejected|RootLoopPublishesStageUpdatesWithoutMainWork|Progress.*|ContextInspectionRealMainRootChildAndReplayPreservesSnapshots|RecallFollowUpUsesRecentTopicWithoutDuplicatingVisibleHistory|RecallFrozenPerTurnAndFenced|RecallInputKeepsChronologyAndEscapesHistoricalMessages|ConversationalRecallQueryBoundsAndEvidenceIsolation|ContextPlainObjectIDs.*|TaskInstructionUsesPlainObjectIDs|ContextDeferredOperationIDsKeepBatchAndLegacyCorrelation)$$' -count=1

test-go:
	go test ./...

# Deterministic connector contracts; live network checks are explicitly opt-in.
test-web-access:
	go test -race ./internal/plugin/webaccess ./internal/connector/... ./internal/webcontent ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run '(WebAccess|WebSearch|HostedSearch|SearchCompletion|SearchPolicy|DefinitionsFreeze|ExaMCP|FetchAddresses)' -count=1

# Privacy, provenance and lifecycle regressions run in the required CI lane.
test-memory:
	go test -race ./internal/memory/... ./internal/recall/... ./internal/conversation/... ./internal/tool ./internal/context ./internal/llm -count=1
	go test -race ./internal/agent/sqlite -run 'Memory|Layered|Semantic|AutomaticRecall|Recall|Forget|Evidence|ObjectIDs|RootFailureNotice' -count=1
	go test -race ./internal/llm/bridge -run 'LayeredMemory|NativeFragmented' -count=1
	go test -race ./internal/app -run 'MemoryExternalSecrets' -count=1

test-connected-apps:
	go test -race ./internal/connector/composio ./internal/connectedapps ./internal/plugin/connectedapps ./internal/interaction/web ./internal/tool ./internal/app ./internal/agent/sqlite -run '^Test(ConnectedApps|Apps|AppLogo|ToolDescriptor|RegistryGroup|Client)' -count=1

test-node:
	npm run check --prefix web
	npm test --prefix web
	npm test --prefix provider-bridge
	npm test --prefix runtime/javascript

test-browser:
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_BROWSER_EVIDENCE="$${STEWARD_APP_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-browser}" go test ./internal/interaction/web -run '^Test(AppsBrowserConnectionLifecycle|ComposerModelsBrowserLoadingAndRefresh|MemoryMaintenanceBrowser)$$' -count=1
	npm test --prefix guest-browser
	node web/artwork/icons.mjs --check

export PYTHONPATH := $(CURDIR)/tests/system:$(CURDIR)/scripts:$(PYTHONPATH)

test-infra:
	python3 -m unittest discover -s tests/harness -p 'test_*.py'

E2E_EVIDENCE ?= evidence/e2e

test-e2e: build
	python3 tests/system/runner.py --suite core --binary bin/steward --evidence "$(E2E_EVIDENCE)"

test-full: build
	python3 tests/system/runner.py --suite full --binary bin/steward --evidence "$(E2E_EVIDENCE)"

test-race:
	python3 tests/system/race.py

lint: lint-actions
	python3 scripts/check_docs.py
	sh -n install.sh scripts/package.sh
	@files="$$(gofmt -l cmd internal)"; if [ -n "$$files" ]; then echo "$$files"; exit 1; fi
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
	go mod verify
	npm run lint --prefix provider-bridge
	npm run lint --prefix runtime/javascript
	npm run lint --prefix guest-browser
	npm run lint --prefix web
	node --check tests/system/apps_connections_browser.mjs
	node --check tests/system/channels_help.mjs

# Tool versions are explicit and do not alter the application's go.mod.
lint-actions:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -color

# Advisory availability is separate from deterministic software regressions.
audit:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
	npm audit --prefix provider-bridge --package-lock-only --audit-level=high
	npm audit --prefix runtime/javascript --package-lock-only --audit-level=high
	npm audit --prefix guest-browser --package-lock-only --audit-level=high
	npm audit --prefix web --package-lock-only --audit-level=high
