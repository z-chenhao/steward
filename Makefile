.PHONY: help setup install run build build-host build-plugins test test-prompts test-memory test-tool-discovery test-web-access test-connected-apps test-github-plugins test-artifact-plugins test-go test-go-core test-go-domains test-node test-browser test-infra test-e2e test-full test-race test-coding-environment test-coding-development lint lint-actions audit

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

build: build-host build-plugins

build-host:
	go build -o bin/steward ./cmd/steward

build-plugins: build-host
	sh scripts/build-plugins.sh "$(CURDIR)/bin/steward" "$(CURDIR)/bin/plugins"

run: build
	./bin/steward start

# Go tests currently contain both unit and component integration tests.
# Keep that explicit rather than relabeling the entire package suite as UT.
test: test-go test-node test-browser test-infra

# Preserve all prompt/inheritance regressions; split real plugin lifecycle suites
# so their cumulative compilation time does not consume one package alarm.
test-prompts:
	go test ./internal/agent ./internal/agent/sqlite -run '^Test(MemoryCompactionPreservesMixedCodeBusinessReceipt|RecallCompleteSearchStillAllowsReadingCASMetadata|RecallRepeatedReadReusesVisibleEvidenceWithoutDuplicatingBody|RecallUnifiedVisibilityIncludesSealedReadsAndAutomaticEvidence|LayeredMemoryNavigationSelectsScopedLinksWithoutRewritingSources|RecallVisibleResultsRequireOwnedReceiptAndCompleteVisibleFrame|LayeredMemoryInjectedSnapshotFrozenSealedAndLoadedWithoutSearch|RecallEmptyEvidenceOmittedFromAgentMessagesAndFrozen|LayeredMemoryCodeReadProjectionIsStableScopedAndDoesNotRewriteReceipt|LayeredMemoryTranscriptReadIsScopedPagedSealedAndRevoked|ContextPromptBytes|ProviderToolUsesFrozenModelDeclarationAndKeepsLegacySchema|ContextRolesAndFrozenResources|AgentStartsWithOneFrozenSystemMessage|ChildInheritsOnlyFrozenSystemSections|SkillsUseSearchWithoutPromptCatalog|SplitSystemMessagesAreRejected|RootLoopPublishesStageUpdatesWithoutMainWork|ContextInspectionRealMainRootChildAndReplayPreservesSnapshots|RecallFollowUpUsesRecentTopicWithoutDuplicatingVisibleHistory|RecallFrozenPerTurnAndFenced|RecallInputKeepsChronologyAndEscapesHistoricalMessages|ConversationalRecallQueryBoundsAndEvidenceIsolation|ContextPlainObjectIDs.*|TaskInstructionUsesPlainObjectIDs|ContextDeferredOperationIDsKeepBatchAndLegacyCorrelation|Conversation.*|RecallPlugin.*|ContextPlugin.*|ContextPureSelection.*|V52FeatureSnapshot.*|SemanticCompactionPlugin.*)$$' -count=1
	go test ./internal/agent ./internal/agent/sqlite -run '^Test(V64ComputerActionPlan.*|V62NativePluginBinding.*|PluginSnapshotWithoutCatalog.*|ComputerPlugin.*)$$' -count=1
	go test ./internal/agent ./internal/agent/sqlite -run '^Test(CodingPlugin.*|V61CodingMethods.*|ShellPlugin.*|V60ShellMethod.*|FilePlugin.*|V59FileMethod.*|CodeModePlugin.*|V58CodeDescription.*)$$' -count=1
	go test ./internal/agent ./internal/agent/sqlite -run '^Test(Progress.*|V63TaskLabel.*|DelegationPlugin.*|DelegationMethods.*|FeedbackPlugin.*|TaskPlugin.*|SkillPlugin.*|ScheduleGuidance.*|ScheduleThreshold.*)$$' -count=1

# Run each Go root once under its original package limit. Keep local
# execution sequential while CI runs the two sets in independent jobs.
test-go: test-go-core
	$(MAKE) test-go-domains

test-go-core:
	go test -p 1 -timeout=15m ./... -skip '^Test(WebPlugin|MemoryInspector|UI|Schedule|Automation|OwnerSchedule|OwnerCanDisable|Apps|ConnectedApp|Memory|Recall|Context|Compaction|Forget|Curator|Dream|Experience|Plugin|Shipped|Runtime|Native|ToolDiscovery|ToolSearch|Skill|MCP|Coding|Computer|File|Shell)' -count=1
	go test -p 1 -timeout=15m ./... -run '^Test(WebPlugin|MemoryInspector)' -count=1
	go test -p 1 -timeout=15m ./... -run '^TestUI' -count=1
	go test -p 1 -timeout=15m ./... -run '^Test(Schedule|Automation|OwnerSchedule|OwnerCanDisable)' -count=1

test-go-domains:
	go test -p 1 -timeout=15m ./... -run '^Test(Apps|ConnectedApp)' -count=1
	go test -p 1 -timeout=15m ./... -run '^Test(Memory|Recall|Context|Compaction|Forget|Curator|Dream|Experience)' -skip '^TestMemoryInspector' -count=1
	go test -p 1 -timeout=15m ./... -run '^Test(Plugin|Shipped|Runtime|Native|ToolDiscovery|ToolSearch|Skill|MCP|Coding|Computer|File|Shell)' -count=1

# Both real discovery entry points keep their own frozen authority and replay.
test-tool-discovery:
	go test -race -p 1 -timeout=15m ./internal/plugin/tooldiscovery/... ./internal/plugin/skilllibrary/... ./internal/skill ./internal/tool ./internal/agent/sqlite -run '^Test(ToolDiscovery.*|ChildStorage.*|ToolSearch.*|LegacyMainLoadsOneOf500.*|LoadedToolSurvivesApproval.*|MigrationCatalogContainsEveryForwardSource|Skill.*)$$' -count=1

# Deterministic connector contracts; live network checks are explicitly opt-in.
test-web-access:
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run '(WebAccess|WebPlugin(Search|Read|Research|Fetch|SourceFormatting|RawSource|HTML|Image)|WebSearch|HostedSearch|SearchCompletion|SearchPolicy|DefinitionsFreeze|ExaMCP|FetchAddresses)' -skip 'WebPlugin(Search|Read|Research|Fetch|SourceFormatting|RawSource|HTML|Image)' -count=1
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run 'WebPlugin(Search|Read)' -count=1
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run 'WebPluginResearch' -count=1
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run 'WebPlugin(Fetch|SourceFormatting|RawSource)' -count=1
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run 'WebPluginHTML' -count=1
	go test -race ./internal/plugin/webaccess ./internal/plugin/webhtml ./internal/plugin/webimage ./internal/connector/... ./internal/webcontent/... ./internal/llm/bridge ./internal/llm/provider ./internal/agent ./internal/agent/sqlite -run 'WebPluginImage' -count=1
	go test -race ./internal/plugin/webextract/... ./internal/agent/sqlite -run 'WebPlugin(Extract|Route)' -count=1
	go test -race ./internal/rsi ./internal/app -run 'WebBootstrap|WebDefault|ShippedPlugin|ToolDiscoveryBootstrap|AppsBootstrap|Verification' -count=1

# Privacy, provenance and lifecycle regressions run in the required CI lane.
test-memory:
	go test -race ./internal/memory/... ./internal/plugin/memoryinspector/... ./internal/recall/... ./internal/conversation/... ./internal/tool ./internal/context ./internal/llm -count=1
	python3 tests/system/race.py --part memory
	go test -race ./internal/llm/bridge -run 'LayeredMemory|NativeFragmented' -count=1
	go test -race ./internal/app ./internal/rsi ./internal/interaction/web -run 'MemoryExternalSecrets|MemoryInspector|MemoryView|MemoryStyles|MemoryLocalization|MemoryClientConfiguration|MemoryProgram|AppsStyles' -count=1

test-connected-apps:
	go test -race -p 1 -timeout=15m ./internal/connector/composio ./internal/connectedapps ./internal/plugin/appsmethods/... ./internal/plugin/appsdirectory/... ./internal/plugin/appsaccounts/... ./internal/plugin/appslogos/... ./internal/plugin/appsinterface/... ./pkg/rsiplugin ./internal/plugin/connectedapps ./internal/rsi ./internal/interaction/web ./internal/tool ./internal/tool/codemode -run '^Test(ConnectedApps|ConnectedAppNamespaces|Apps|Editorial|CatalogueOrder|InventoryOrder|Input(Stream|Chunks|Rejects)|AppLogo|ToolDescriptor|RegistryGroup|Client)' -count=1
	python3 tests/system/race.py --part apps-sqlite
	python3 tests/system/race.py --part apps

# Build the exact distribution before the mandatory real App lifecycle test.
# Plugin/source oracles, SQL migration/recovery and CLI remain in the same race gate.
test-github-plugins: build
	STEWARD_TEST_APP_DEFAULT_BUNDLE="$(CURDIR)/bin/steward" go test -race -p 1 -timeout=15m ./internal/plugin/githubfollowup/... ./internal/observer/github ./internal/rsi ./internal/app ./cmd/steward -run '^Test(GitHub.*|StartupReconcilesRemovedOrChangedGitHubScopeBeforeActor|ShippedPluginDistributionRejectsPartialNativeFallback|MigrationCatalogContainsEveryForwardSource)$$' -count=1
	python3 tests/system/race.py --part github-sqlite

# Keep owner, Run, historical reference and pure-method regressions in CI.
test-artifact-plugins: build
	go test -race ./internal/artifact/... ./internal/plugin/artifactworkbench/... -count=1
	STEWARD_TEST_APP_DEFAULT_BUNDLE="$(CURDIR)/bin/steward" go test -race -p 1 -timeout=15m ./internal/rsi ./internal/app ./cmd/steward -run '^Test(Artifact.*|V68Artifact.*|ShippedPluginDistributionRejectsPartialNativeFallback|MigrationCatalogContainsEveryForwardSource)$$' -count=1
	STEWARD_TEST_APP_DEFAULT_BUNDLE="$(CURDIR)/bin/steward" python3 tests/system/race.py --part artifact-sqlite

test-node:
	npm run check --prefix web
	npm test --prefix web
	npm test --prefix provider-bridge
	npm test --prefix runtime/javascript

apps_browser_env = STEWARD_TEST_APP_BROWSER=1 \
	STEWARD_APP_DIRECTORY_BROWSER_EVIDENCE="$${STEWARD_APP_DIRECTORY_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-directory-browser}" \
	STEWARD_APP_SETTINGS_BROWSER_EVIDENCE="$${STEWARD_APP_SETTINGS_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-settings-browser}" \
	STEWARD_APP_LOGO_BROWSER_EVIDENCE="$${STEWARD_APP_LOGO_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-logos-browser}" \
	STEWARD_APP_LOCALIZATION_BROWSER_EVIDENCE="$${STEWARD_APP_LOCALIZATION_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-localization-browser}" \
	STEWARD_APP_COMPOSITION_BROWSER_EVIDENCE="$${STEWARD_APP_COMPOSITION_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-composition-browser}" \
	STEWARD_APP_INTERFACE_BROWSER_EVIDENCE="$${STEWARD_APP_INTERFACE_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-interface-browser}" \
	STEWARD_APP_CARD_VIEW_BROWSER_EVIDENCE="$${STEWARD_APP_CARD_VIEW_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-card-view-browser}" \
	STEWARD_APP_SIDEBAR_VIEW_BROWSER_EVIDENCE="$${STEWARD_APP_SIDEBAR_VIEW_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-sidebar-view-browser}" \
	STEWARD_APP_GRAPHICS_BROWSER_EVIDENCE="$${STEWARD_APP_GRAPHICS_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-graphics-browser}" \
	STEWARD_APP_MARKUP_BROWSER_EVIDENCE="$${STEWARD_APP_MARKUP_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-markup-browser}" \
	STEWARD_APP_ACCOUNT_MARKUP_BROWSER_EVIDENCE="$${STEWARD_APP_ACCOUNT_MARKUP_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-account-markup-browser}"

test-browser:
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_BROWSER_EVIDENCE="$${STEWARD_APP_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-browser}" go test ./internal/interaction/web -run '^Test(MemoryProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures|TraceProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures|AppsBrowserActualOpaqueWorkerTransport|AppsBrowserConnectionLifecycle|ComposerModelsBrowserLoadingAndRefresh|MemoryMaintenanceBrowser|TracePageBrowserKeepsOneFrozenAssetVersion|WorkbenchBrowserOriginalStylesAndPinnedUpdates|TraceWorkbenchBrowserReadOnlyControls|TraceWorkbenchBrowserManagedTreeAndColumnPolicies|TraceTimelineMalformedProjectionKeepsOriginalSSR|TraceBrushActualDelayedRepliesCoalesceAndCancel|TraceActivityActualDelayedRepliesCoalesceAndCancel|ApprovalParametersBrowser|UIPlanViewBrowserIsolationAndDetails|UICardsViewBrowserIsolationAndOriginalStyles|UICardsWorkerBrowserIsolationAndBoundedProjection|UICardsControllerBrowserProjectionCASGroupAndRecovery|UICardsImagesBrowserOriginalBlobAndBoundRequests|UICardsPreviewBrowserReadOnlyAndRevision|UITableWorkerBrowserIsolationAndBoundedProjection|UITableControllerBrowserProjectionCASGroupAndExport|UITablePreviewBrowserReadOnlyAndRevision|UIChartWorkerBridgeBrowser|UIChartControllerBrowserProjectionCASAndRange|UIChartPreviewBrowserReadOnlyAndRevision|UIFormViewControllerBrowserOriginalControlsDraftAndBoundSubmit|UIFormViewPreviewBrowserReadOnlyAndRevision|UIMeetingViewBrowserOriginalControlsChoiceAndPreview|UIActionViewBrowserFeedbackRecoveryAndOriginalSource)$$' -count=1
	$(apps_browser_env) go test ./internal/app -run '^TestApps(EditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable|SettingsActualOriginalHTTPRowsDraftAndSave|LogoActualOwnerHTTPOriginal65FrozenReferencesColdAndDisable|InterfaceActualManagedBrowserLifecycle)$$' -count=1 -timeout=5m
	$(apps_browser_env) go test ./internal/app -run '^TestApps(CompositionActualVersionsColdOriginalProfileOnlyAndUnavailable|LocalizationActualBrowserOwnedFeedbackAndNative|CardViewActualManagedResourceBrowser|SidebarViewActualBrowserAndAcceptedLink)$$' -count=1 -timeout=5m
	$(apps_browser_env) go test ./internal/app -run '^TestApps(MarkupActualBrowserAndRecoveryLink|GraphicsActualBrowserAndRecoveryLink|AccountMarkupActualBrowserAndRecoveryLink)$$' -count=1 -timeout=5m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_CONNECT_VIEW_BROWSER_EVIDENCE="$${STEWARD_APP_CONNECT_VIEW_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-connect-view-browser}" go test ./internal/app -run '^TestAppsConnectViewActualBrowserAndRecoveryLink$$' -count=1 -timeout=4m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_SETTINGS_VIEW_BROWSER_EVIDENCE="$${STEWARD_APP_SETTINGS_VIEW_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-settings-view-browser}" go test ./internal/app -run '^TestAppsSettingsViewActualBrowserDraftSave$$' -count=1 -timeout=4m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_DIRECTORY_CONTROLS_BROWSER_EVIDENCE="$${STEWARD_APP_DIRECTORY_CONTROLS_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-directory-controls-browser}" go test ./internal/app -run '^TestAppsDirectoryControlsActualBrowserNavigationFilterAndPagination$$' -count=1 -timeout=4m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_PAGES_BROWSER_EVIDENCE="$${STEWARD_APP_PAGES_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-pages-browser}" go test ./internal/app -run '^TestAppsPagesActualBrowserOriginalControlsAndViews$$' -count=1 -timeout=4m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_APP_CLIENT_BROWSER_EVIDENCE="$${STEWARD_APP_CLIENT_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-client-browser}" go test ./internal/app -run '^TestAppsClientActualBrowserOriginalControlsAndStyles$$' -count=1 -timeout=4m
	STEWARD_TEST_APP_BROWSER=1 STEWARD_MEMORY_MANAGED_BROWSER_EVIDENCE="$${STEWARD_MEMORY_MANAGED_BROWSER_EVIDENCE:-$${STEWARD_APP_BROWSER_EVIDENCE:-$(CURDIR)/evidence/apps-browser}/memory-managed}" go test ./internal/app -run '^TestMemoryInspectorActualOwnerPageGraphPinDisableAndFirstColdReference$$' -count=1
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

# A real namespace/Engine protocol gate; never silently skips when unavailable.
test-coding-environment:
	python3 tests/system/verify_coding_container.py --evidence "$${CODING_ENV_EVIDENCE:-evidence/coding-container}"

# Cold preparation by default; an explicit image reuses a recorded owner basis.
test-coding-development:
	python3 tests/system/verify_coding_development.py --evidence "$${CODING_DEVELOPMENT_EVIDENCE:-evidence/coding-development}"

lint: lint-actions
	python3 scripts/check_docs.py
	sh -n install.sh scripts/package.sh scripts/build-plugins.sh
	@files="$$(gofmt -l cmd internal pkg)" || exit $$?; if [ -n "$$files" ]; then echo "$$files"; exit 1; fi
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
	go mod verify
	npm run lint --prefix provider-bridge
	npm run lint --prefix runtime/javascript
	npm run lint --prefix guest-browser
	npm run lint --prefix web
	node --check internal/plugin/webextract/extract.mjs
	@for file in tests/system/*.mjs; do node --check "$$file" || exit $$?; done
	node --check internal/interaction/web/view/cards-gestures.js
	node --check guest-browser/meeting-controller-browser.mjs
	node --check guest-browser/action-controller-browser.mjs

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
