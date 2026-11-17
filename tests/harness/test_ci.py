"""Check partition completeness and the fail-closed required status."""
import unittest
import re
import shlex
import subprocess
from pathlib import Path
from unittest.mock import DEFAULT, patch

import ci
import race
import verify_channels
from runner import CORE


class CIChecksTests(unittest.TestCase):
    def test_go_partitions_keep_all_roots_and_original_package_alarm(self):
        root = Path(__file__).resolve().parents[2]
        commands = subprocess.check_output(['make', '--no-print-directory', '-n', 'test-go'], cwd=root, text=True)
        partitions = [shlex.split(line) for line in commands.splitlines()]
        self.assertEqual(len(partitions), 5)
        for tokens in partitions:
            self.assertIn('-timeout=15m', tokens)
            self.assertEqual(tokens[tokens.index('-p') + 1], '1')
            self.assertIn('-count=1', tokens)
        self.assertIn('./...', partitions[0])
        skip = partitions[0][partitions[0].index('-skip') + 1]
        self.assertIn('./...', partitions[1])
        selected = partitions[1][partitions[1].index('-run') + 1]
        self.assertIn('./...', partitions[2])
        selected_ui = partitions[2][partitions[2].index('-run') + 1]
        self.assertIn('./...', partitions[3])
        selected_schedule = partitions[3][partitions[3].index('-run') + 1]
        self.assertIn('./...', partitions[4])
        selected_apps = partitions[4][partitions[4].index('-run') + 1]
        for folder in ('cmd', 'internal', 'pkg', 'examples'):
            for source in (root / folder).rglob('*_test.go'):
                for name in re.findall(r'^func (Test\w+)\(t \*testing.T\)', source.read_text(), re.MULTILINE):
                    first = not re.search(skip, name)
                    second = bool(re.search(selected, name))
                    third = bool(re.search(selected_ui, name))
                    fourth = bool(re.search(selected_schedule, name))
                    fifth = bool(re.search(selected_apps, name))
                    self.assertEqual(sum((first, second, third, fourth, fifth)), 1, f'{source}: {name} must run exactly once')

    def test_prompt_partitions_keep_inheritance_and_managed_lifecycles_required(self):
        root = Path(__file__).resolve().parents[2]
        commands = subprocess.check_output(['make', '-n', 'test-prompts'],
                                           cwd=root, text=True)
        patterns = []
        for line in commands.splitlines():
            tokens = shlex.split(line)
            if '-run' in tokens:
                self.assertNotIn('-timeout', tokens)
                patterns.append(tokens[tokens.index('-run') + 1])
        self.assertEqual(len(patterns), 4)
        required = {
            'TestAgentStartsWithOneFrozenSystemMessage',
            'TestChildInheritsOnlyFrozenSystemSections',
            'TestContextRolesAndFrozenResources',
            'TestRecallFrozenPerTurnAndFenced',
            'TestContextInspectionRealMainRootChildAndReplayPreservesSnapshots',
            'TestMemoryCompactionPreservesMixedCodeBusinessReceipt',
        }
        # New lifecycle cases in these families must remain reachable without
        # extending a hand-maintained list of individual regression names.
        families = re.compile(r'^Test(?:ComputerPlugin|CodingPlugin|ShellPlugin|'
                              r'FilePlugin|CodeModePlugin|DelegationPlugin|'
                              r'DelegationMethods|FeedbackPlugin|Progress|TaskPlugin|'
                              r'SkillPlugin|ScheduleGuidance|ScheduleThreshold|Conversation|RecallPlugin|ContextPlugin|ContextPureSelection|'
                              r'SemanticCompactionPlugin|PluginSnapshotWithoutCatalog|'
                              r'V64ComputerActionPlan|V63TaskLabel|V62NativePluginBinding|'
                              r'V61CodingMethods|V60ShellMethod|V59FileMethod|'
                              r'V58CodeDescription|V52FeatureSnapshot)')
        for folder in ('internal/agent', 'internal/agent/sqlite'):
            for source in (root / folder).glob('*_test.go'):
                names = re.findall(r'^func (Test\w+)\(t \*testing.T\)',
                                   source.read_text(), re.MULTILINE)
                required.update(name for name in names if families.match(name))
        for name in sorted(required):
            self.assertEqual(sum(bool(re.fullmatch(pattern, name)) for pattern in patterns),
                             1, f'{name} must run in exactly one prompt partition')

    def test_required_web_lane_reaches_settings_and_task_regressions(self):
        # Standalone scenarios protect a flow only when the required lane runs it.
        scenarios = ('preference_settings', 'memory_inspection', 'attention_workspace',
                     'sending_feedback', 'live_updates', 'streaming_replies',
                     'inspection_relationships', 'capability_settings',
                     'dependency_recovery', 'native_components', 'rich_replies',
                     'account_presentation', 'native_trace_inspection',
                     'page_transitions', 'model_connections', 'task_control_components')
        lanes = {row['lane']: row for row in ci.plan('system')['include']}
        self.assertEqual(lanes['channels-web']['browser'], 'true')
        self.assertIn('--group web', ci.command('channels-web'))
        report = dict(checks=[], scenarios={})
        binary, evidence = Path('/unused/steward'), Path('/unused/evidence/report.json')
        with patch.multiple(verify_channels, **dict.fromkeys(scenarios, DEFAULT)) as checks:
            ci.CHANNEL_GROUPS['web'](binary, evidence, report)
        for name in scenarios:
            checks[name].assert_called_once()
        for name in ('preference_settings', 'page_transitions', 'model_connections',
                     'task_control_components', 'capability_settings'):
            checks[name].assert_called_once_with(binary, evidence, report)

    def test_connected_apps_and_browser_regressions_are_required_software_lanes(self):
        lanes = {row['lane']: row for row in ci.plan('software')['include']}
        self.assertEqual(ci.command('connected-apps'), 'make test-connected-apps')
        self.assertIn('connected-apps', lanes)
        self.assertIn('make test-browser', ci.command('browser-infra'))
        self.assertEqual(lanes['browser-infra']['browser'], 'true')
        self.assertEqual(lanes['browser-infra']['frontend'], 'true')
        dry_run = subprocess.check_output(['make', '-n', 'test-browser'],
                                          cwd=Path(__file__).resolve().parents[2], text=True)
        patterns = []
        for line in dry_run.splitlines():
            tokens = shlex.split(line)
            if '-run' in tokens:
                patterns.append(tokens[tokens.index('-run') + 1])
        for test in ('TestAppsClientActualBrowserOriginalControlsAndStyles', 'TestAppsBrowserActualOpaqueWorkerTransport', 'TestAppsBrowserConnectionLifecycle', 'TestComposerModelsBrowserLoadingAndRefresh',
                     'TestMemoryMaintenanceBrowser',
                     'TestTracePageBrowserKeepsOneFrozenAssetVersion',
                     'TestWorkbenchBrowserOriginalStylesAndPinnedUpdates',
                     'TestTraceWorkbenchBrowserReadOnlyControls',
                     'TestTraceWorkbenchBrowserManagedTreeAndColumnPolicies',
                     'TestTraceTimelineMalformedProjectionKeepsOriginalSSR',
                     'TestTraceBrushActualDelayedRepliesCoalesceAndCancel',
                     'TestTraceActivityActualDelayedRepliesCoalesceAndCancel',
                     'TestTraceProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures'):
            self.assertTrue(any(re.fullmatch(pattern, test) for pattern in patterns),
                            f'{test} is missing from the required browser recipe')


    def test_default24_discovery_requires_actual_stock_cli_system_gate(self):
        from runner import REGISTRY
        lanes = {lane['lane'] for lane in ci.plan('system')['include']}
        self.assertIn('tool-discovery-plugins', lanes)
        command = ci.command('tool-discovery-plugins')
        self.assertIn('make build', command)
        self.assertEqual(command.count('--gate tool-discovery-plugins'), 1)
        self.assertEqual(REGISTRY['tool-discovery-plugins'], 'verify_tool_discovery_plugins.py')
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_github_requires_real_source_lifecycle_and_independent_owner_races(self):
        from runner import REGISTRY
        self.assertIn('github-plugins', {r['lane'] for r in ci.plan('system')['include']})
        command = ci.command('github-plugins')
        self.assertIn('make test-github-plugins', command)
        self.assertEqual(command.count('--gate github-plugins'), 1)
        self.assertEqual(REGISTRY['github-plugins'], 'verify_github_plugins.py')
        recipe = subprocess.check_output(['make', '-n', 'test-github-plugins'],
            cwd=Path(__file__).resolve().parents[2], text=True)
        self.assertIn('scripts/build-plugins.sh', recipe)
        self.assertIn('STEWARD_TEST_APP_DEFAULT_BUNDLE=', recipe)
        args = shlex.split(recipe)
        for flag in ('-race', '-count=1', '-timeout=15m'):
            self.assertIn(flag, args)
        pattern = args[args.index('-run') + 1]
        for name in ('TestGitHubBootstrapIndependentOracleAndOriginalSelection',
                     'TestGitHubDefaultProductionControlPollAndColdLifecycle',
                     'TestGitHubNativeReadingMatchesOriginal27Cases',
                     'TestGitHubPluginBadInitialPlanCannotReadCredentialOrSource',
                     'TestMigrationCatalogContainsEveryForwardSource'):
            self.assertIsNotNone(re.fullmatch(pattern, name), name)
        self.assertNotIn('|| true', command)

    def test_artifact_requires_actual_owner_lifecycle_and_no_native_fallback(self):
        from runner import REGISTRY
        self.assertIn('artifact-plugins', {r['lane'] for r in ci.plan('system')['include']})
        command = ci.command('artifact-plugins')
        self.assertIn('make test-artifact-plugins', command)
        self.assertEqual(command.count('--gate artifact-plugins'), 1)
        self.assertEqual(REGISTRY['artifact-plugins'], 'verify_artifact_plugins.py')
        recipe = subprocess.check_output(['make', '-n', 'test-artifact-plugins'],
            cwd=Path(__file__).resolve().parents[2], text=True)
        self.assertIn('scripts/build-plugins.sh', recipe)
        self.assertIn('STEWARD_TEST_APP_DEFAULT_BUNDLE=', recipe)
        args = shlex.split(recipe)
        for flag in ('-race', '-count=1', '-timeout=15m'):
            self.assertIn(flag, args)
        pattern = args[args.index('-run') + 1]
        for name in ('TestArtifactDefaultProductionOwnerWithoutModelAndColdLifecycle',
                     'TestArtifactFreshUnbundledProductionOwnerCannotUseNative',
                     'TestArtifactDefaultProductionHTTPChannelsFreezeCreationTypeAcrossVersions',
                     'TestArtifactWorkbenchPRHandoffRetainsAssociationAndFencesJointCommit',
                     'TestArtifactWorkbenchActualReadAndSearchMatchIndependentNativeOracle',
                     'TestArtifactWorkbenchActualMediaKindsMatchIndependentNativeOracle',
                     'TestArtifactWorkbenchActualOutputAndCleanupMatchIndependentNativeOracle',
                     'TestArtifactWorkbenchActualChannelImportRetainsRecipeAndOriginalDedup',
                     'TestArtifactWorkbenchTaskLifetimeAndRetentionAfterTaskEnd',
                     'TestArtifactWorkbenchActualCodingReportUsesIndependentCreationVersion',
                     'TestArtifactWorkbenchCodingCommitKeepsBothMethodsBehindLateRevocation',
                     'TestArtifactWorkbenchOwnerLegacyEvidenceIsMigrationOnlyAndActivationClosesIt',
                     'TestArtifactWorkbenchCleanupRevalidatesWholeSetBeforeDeletion',
                     'TestArtifactWorkbenchOwnerForgetAfterStagingRejectsPublication',
                     'TestArtifactWorkbenchCleanupLateRevocationNeverDeletes',
                     'TestArtifactWorkbenchEffectPreparesBeforeDispatchAndReconcilesWithoutCandidate',
                     'TestV68ArtifactRecipeMigrationPreservesOriginalFactsAndNativeBoundary',
                     'TestMigrationCatalogContainsEveryForwardSource'):
            self.assertIsNotNone(re.fullmatch(pattern, name), name)
        self.assertNotIn('|| true', command)

    def test_tool_discovery_requires_owned_races_and_forward_schema_catalog(self):
        self.assertIn('test-tool-discovery', ci.command('go'))
        recipe = subprocess.check_output(['make', '-n', 'test-tool-discovery'],
            cwd=Path(__file__).resolve().parents[2], text=True)
        args = shlex.split(recipe)
        for flag in ('-race', '-count=1', '-timeout=15m'):
            self.assertIn(flag, args)
        pattern = args[args.index('-run') + 1]
        for name in ('TestToolDiscoveryOriginal288FrozenRankings',
                     'TestToolDiscoveryActualNative500AndWorkerCodeSearchWithoutGrantingAccess',
                     'TestToolDiscoveryAcceptedCacheQuarantineAndOldVMFences',
                     'TestToolDiscoveryCacheCommitFailureNeverAcknowledgesUnretainedReply',
                     'TestToolDiscoveryLateForgetRejectsReplyWithoutHoldingSQLiteWriter',
                     'TestToolDiscoveryActualColdVMRecoversFrozenReplyAndOriginalRankVersion',
                     'TestToolDiscoveryActualFrozenRunChildUpgradeRollbackRetireAndDisable',
                     'TestToolDiscoveryRealVMRepliesStayByteStableAcrossNestedSuspension',
                     'TestToolDiscoveryDeclaredBadPlanCannotRestoreNativeOrLoadForeignTools',
                     'TestToolDiscoveryActualFrozenMetadataBeyondOrdinaryInputLimit',
                     'TestMigrationCatalogContainsEveryForwardSource'):
            self.assertIsNotNone(re.fullmatch(pattern, name))

    def test_all_core_gates_run_once(self):
        gates = [gate for group in ci.SYSTEM.values() for gate in group]
        self.assertCountEqual(gates + ['channels'], CORE)
        self.assertEqual(len(gates), len(set(gates)))
        for kind in ('software', 'system'):
            for lane in ci.plan(kind)['include']:
                self.assertTrue(ci.command(lane['lane']))
        lanes = {lane['lane'] for lane in ci.plan('system')['include']}
        self.assertEqual({name.removeprefix('channels-') for name in lanes if name.startswith('channels-')}, set(ci.CHANNEL_GROUPS))

    def test_intelligent_ui_uses_required_go_browser_and_race_checks(self):
        self.assertIn('test-go', ci.command('go'))
        self.assertIn('intelligent-ui', ci.SYSTEM['startup-ui'])
        ui_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_intelligent_ui.py').read_text()
        self.assertIn('from verify_ui_plan_plugins import run as run_managed_plan', ui_system)
        self.assertIn('managed_plan = run_managed_plan(binary,', ui_system)
        self.assertIn('from verify_ui_cards_plugins import run as run_managed_cards', ui_system)
        self.assertIn('managed_cards = run_managed_cards(binary,', ui_system)
        self.assertIn('from verify_ui_table_plugins import run as run_managed_table', ui_system)
        self.assertIn('managed_table = run_managed_table(binary,', ui_system)
        self.assertIn('from verify_ui_charts_plugins import run as run_managed_charts', ui_system)
        self.assertIn('managed_charts = run_managed_charts(binary,', ui_system)
        self.assertIn('from verify_ui_forms_plugins import run as run_managed_forms', ui_system)
        self.assertIn('managed_forms = run_managed_forms(binary,', ui_system)
        self.assertIn('from verify_ui_meetings_plugins import run as run_managed_meetings', ui_system)
        self.assertIn('managed_meetings = run_managed_meetings(binary,', ui_system)
        self.assertIn('from verify_ui_actions_plugins import run as run_managed_actions', ui_system)
        self.assertIn('managed_actions = run_managed_actions(binary,', ui_system)
        action_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_actions_plugins.py').read_text()
        self.assertIn('^TestUIActionActualForgetRevokesOriginalFeedbackAndReceipt$', action_system)
        self.assertIn('STEWARD_TEST_ACTION_CONFIG', action_system)
        meeting_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_meetings_plugins.py').read_text()
        self.assertIn('^TestUIMeetingActualForgetRevokesOriginalProjection$', meeting_system)
        self.assertIn('STEWARD_TEST_MEETING_CONFIG=str(h.config)', meeting_system)
        forms_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_forms_plugins.py').read_text()
        self.assertIn('^TestUIFormViewActualForgetRevokesOriginalViewAndDraft$', forms_system)
        self.assertIn('STEWARD_TEST_FORM_CONFIG=str(h.config)', forms_system)
        charts_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_charts_plugins.py').read_text()
        self.assertIn('^TestUIChartActualForgetRevokesOriginalViewAndExport$', charts_system)
        self.assertIn('STEWARD_TEST_CHART_CONFIG=str(h.config)', charts_system)
        table_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_table_plugins.py').read_text()
        self.assertIn('^TestUITableActualForgetRevokesOriginalViewAndExport$', table_system)
        self.assertIn('STEWARD_TEST_TABLE_CONFIG=str(h.config)', table_system)
        cards_system = (Path(__file__).resolve().parents[2] / 'tests/system/verify_ui_cards_plugins.py').read_text()
        self.assertIn('^TestUICardsActualOpenRevocationFence$', cards_system)
        self.assertIn('STEWARD_TEST_CARDS_CONFIG=str(h.config)', cards_system)
        self.assertIn('ui-interactions', ci.SYSTEM['startup-ui'])
        lanes = {row['lane']: row for row in ci.plan('system')['include']}
        self.assertEqual(lanes['startup-ui']['browser'], 'true')
        race = ci.command('capabilities-race')
        for package in ('agent', 'agent/sqlite', 'interaction/web', 'llm/bridge'):
            self.assertIn('./internal/' + package + ' ', ci.RACE)
        self.assertIn(ci.RACE, race)
        self.assertIn('.*UI.*', ci.RACE)
        self.assertIn('ReplyStream.*', ci.RACE)
        browser_command = subprocess.check_output(['make', '-n', 'test-browser'],
                                                  cwd=Path(__file__).resolve().parents[2], text=True)
        self.assertIn('UIPlanViewBrowserIsolationAndDetails', browser_command)
        self.assertIn('UICardsViewBrowserIsolationAndOriginalStyles', browser_command)
        self.assertIn('UICardsControllerBrowserProjectionCASGroupAndRecovery', browser_command)
        self.assertIn('UICardsImagesBrowserOriginalBlobAndBoundRequests', browser_command)
        self.assertIn('UICardsPreviewBrowserReadOnlyAndRevision', browser_command)
        self.assertIn('UITableWorkerBrowserIsolationAndBoundedProjection', browser_command)
        self.assertIn('UITableControllerBrowserProjectionCASGroupAndExport', browser_command)
        self.assertIn('UITablePreviewBrowserReadOnlyAndRevision', browser_command)
        self.assertIn('UIChartWorkerBridgeBrowser', browser_command)
        self.assertIn('UIChartControllerBrowserProjectionCASAndRange', browser_command)
        self.assertIn('UIChartPreviewBrowserReadOnlyAndRevision', browser_command)
        self.assertIn('UIFormViewControllerBrowserOriginalControlsDraftAndBoundSubmit', browser_command)
        self.assertIn('UIFormViewPreviewBrowserReadOnlyAndRevision', browser_command)
        self.assertIn('UIMeetingViewBrowserOriginalControlsChoiceAndPreview', browser_command)
        self.assertIn('UIActionViewBrowserFeedbackRecoveryAndOriginalSource', browser_command)

        web = Path(__file__).resolve().parents[2] / 'internal/interaction/web'
        for filename in ('cards_controller_browser_test.go', 'cards_images_browser_test.go', 'cards_preview_browser_test.go'):
            self.assertNotIn('waitForFunction(async', (web / filename).read_text(),
                             'Pinned Playwright must not treat an HTTP Promise as a true predicate')

        tokens = shlex.split(ci.RACE)
        selected = tokens[tokens.index('-run') + 1]
        folder = Path(__file__).resolve().parents[2] / 'internal/agent/sqlite'
        for filename, names in {
            'plugin_ui_form_test.go': ['TestUIManagedFormAdmissionRollbackRetryAndOfflineRestore',
                                       'TestUIManagedFormRevocationBeforePolicyAndAdmission'],
            'ui_form_binding_test.go': ['TestUIWebFormBindingPreservesDuplicatePositionsAndFrozenRun'],
            'plugin_ui_plan_versions_test.go': ['TestUIPlanViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots'],
            'plugin_ui_cards_versions_test.go': ['TestUICardsViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots',
                                                'TestUICardsChildAdmissionAfterUpgradeKeepsParentGraphAndService'],
            'plugin_ui_cards_test.go': ['TestUICardsViewSnapshotSurvivesReceiptFailureWithoutCandidateReplay',
                                       'TestUICardsViewSnapshotRollbackDoesNotReserveEffect',
                                       'TestUICardsViewRefBindingRejectsMissingManagedReceipt',
                                       'TestUICardsViewRejectsMissingDuplicateAndOtherComponentSlots',
                                       'TestUICardsImageGrantUsesPublishedOriginalItemAndPrivacyFence',
                                       'TestUICardsLinkGrantUsesPublishedOriginalItemAndPrivacyFence',
                                       'TestUICardsPreviewUsesOriginalPureServiceWithoutDeliveryGrants'],
            'plugin_ui_plan_test.go': ['TestUIPlanViewSnapshotSurvivesReceiptFailureWithoutCandidateReplay',
                                      'TestUIPlanViewSnapshotRejectsMissingDuplicateAndOtherComponentSlots',
                                      'TestUIPlanViewSnapshotRollbackDoesNotReserveEffect',
                                      'TestUIPlanViewPreviewUsesOriginalPureServiceAndPrivacyFence'],
        }.items():
            source = (folder / filename).read_text()
            for name in names:
                self.assertIn('func ' + name + '(', source)
                self.assertRegex(name, selected, name + ' must remain in required UI race checks')


    def test_memory_privacy_race_and_system_gates_are_required(self):
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('memory-context', lanes)
        self.assertEqual(lanes['memory-context']['browser'], 'false')
        command = ci.command('memory-context')
        self.assertIn('make test-memory build', command)
        self.assertEqual(ci.MEMORY_GATES, ('memory-maintenance', 'recall'))
        for gate in ci.MEMORY_GATES:
            self.assertEqual(command.count('--gate ' + gate), 1)
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_managed_memory_inspector_browser_is_required(self):
        root = Path(__file__).resolve().parents[2]
        recipe = subprocess.check_output(['make', '-n', 'test-browser'], cwd=root, text=True)
        expected = "go test ./internal/app -run '^TestMemoryInspectorActualOwnerPageGraphPinDisableAndFirstColdReference$' -count=1"
        self.assertIn(expected, recipe)
        line = next(line for line in recipe.splitlines() if expected in line)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', line)
        self.assertIn('STEWARD_MEMORY_MANAGED_BROWSER_EVIDENCE=', line)

    def test_required_memory_lane_covers_identity_and_candidate_boundaries(self):
        makefile = (Path(__file__).resolve().parents[2] / 'Makefile').read_text()
        commands = makefile.split('\ntest-memory:\n', 1)[1].split('\n\n', 1)[0]
        self.assertIn('memory-context', {lane['lane'] for lane in ci.plan('system')['include']})
        self.assertEqual(commands.count('go test -race '), 3)
        self.assertIn('python3 tests/system/race.py --part memory', commands)
        for package in ('memory/...', 'recall/...', 'conversation/...', 'tool', 'llm/bridge', 'app'):
            self.assertIn('./internal/' + package + ' ', commands)
        self.assertRegex('TestMemoryInspectorActualProcessLateForgetReplacementPrivacyAndGuardian', race.MEMORY_PATTERN)
        for regression in ('Recall', 'Forget', 'Evidence', 'ObjectIDs', 'Layered', 'RootFailureNotice'):
            self.assertIn(regression, race.MEMORY_PATTERN)
        for regression in ('NativeFragmented', 'MemoryExternalSecrets', 'MemoryInspector', 'MemoryView', 'MemoryStyles', 'MemoryLocalization', 'MemoryClientConfiguration', 'MemoryProgram', 'AppsStyles'):
            self.assertIn(regression, commands)
        self.assertIn('./internal/plugin/memoryinspector/... ', commands)
        browser_recipe = subprocess.check_output(['make', '-n', 'test-browser'], cwd=Path(__file__).resolve().parents[2], text=True)
        self.assertIn('MemoryProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures', browser_recipe)
        self.assertIn('./internal/app ./internal/rsi ./internal/interaction/web ', commands)
        # Existence in test-go alone must not silently remove race coverage.
        dry_run = subprocess.check_output(['make', '-n', 'test-memory'],
                                          cwd=Path(__file__).resolve().parents[2], text=True)
        self.assertIn('python3 tests/system/race.py --part memory', dry_run)
        for test in ('TestMemoryWorkerEvidencePageBoundaryAndContinuation',
                     'TestMemoryMissingFieldsSurviveNativeAndCodeModeReceipts',
                     'TestMemoryDreamDoesNotBlindlyRecreateOversizedInjectedHead',
                     'TestMemoryDreamFailureDiagnosticsAndSuccessfulRetry',
                     'TestWebPluginSearchLateForgetAndGuardianCannotReleaseSourceBytes',
                     'TestWebPluginReadLateForgetAndGuardianCannotAcceptPrivateResult',
                     'TestWebPluginResearchLateForgetAndGuardianAtPublicationBoundary',
                     'TestWebPluginFetchLateForgetAndGuardianBeforePublication',
                     'TestWebPluginSourceFormattingLateForgetBeforeSourceRelease',
                     'TestWebPluginHTMLLateForgetAndGuardianCannotReleaseDerivedText',
                     'TestWebPluginImageLateForgetAndGuardianCannotPublishAttachment',
                     'TestWebPluginExtractLateForgetAndGuardianBeforePublication',
                     'TestWebPluginRouteLateForgetAndGuardianBeforeDecode'):
            self.assertTrue(re.search(race.MEMORY_PATTERN, test),
                            f'{test} is missing from required memory race checks')

    def test_web_plugin_business_roots_are_in_required_race_recipe(self):
        self.assertIn('make test-web-access', ci.command('capabilities-race'))
        dry_run = subprocess.check_output(['make', '-n', 'test-web-access'],
                                          cwd=Path(__file__).resolve().parents[2], text=True)
        commands = [shlex.split(line) for line in dry_run.splitlines()]
        def selected(root):
            return sum(re.search(row[row.index('-run') + 1], root) is not None and
                       ('-skip' not in row or re.search(row[row.index('-skip') + 1], root) is None)
                       for row in commands if '-run' in row)
        tokens = shlex.split(dry_run)
        patterns = [tokens[i + 1] for i, token in enumerate(tokens) if token == '-run']
        self.assertEqual(len(patterns), 8)
        self.assertIn('./internal/webcontent/...', tokens)
        self.assertIn('./internal/rsi', tokens)
        self.assertIn('./internal/app', tokens)
        for root in ('TestWebBootstrapActualMethodsUnavailableResourceAndOwnerSelection',
                     'TestWebDefaultCustomSourceColdReopenRetainsOriginalModuleAndInterpreter',
                     'TestWebDefaultInvalidOptionalSourceKeepsOtherManagedMethodsAvailable',
                     'TestToolDiscoveryBootstrapRejectsCorruptPlanAndRetainsOwnerDisable',
                     'TestVerificationBoundedFreshProcessesAndLateFailure',
                     'TestVerificationBeyondFormerCaseLimitIncludesLateFailure',
                     'TestVerificationPolicyCaseBound'):
            self.assertEqual(selected(root), 1,
                            f'{root} is missing from required Web race')
        for root in ('TestWebPluginSearchActualTaskUpgradeRetainedRollbackAndDisable',
                     'TestWebPluginReadPublishedInterpreterSurvivesUpgradeRetirementAndDisable',
                     'TestWebPluginReadComplete24MiBDocumentAndExactUnicodeFind',
                     'TestWebPluginReadRejectsAcceptedForgedMatchWithoutNativeFallback',
                     'TestWebPluginReadLateForgetAndGuardianCannotAcceptPrivateResult',
                     'TestWebPluginReadOriginalVersionAfterRealReleaseRecoveryAndClaim',
                     'TestWebPluginReadMainCodeModeUsesScopedPersistedDocument',
                     'TestWebPluginResearchActualBatchSummaryAndCompleteEvidence',
                     'TestWebPluginResearchPageAnswersUseOriginalFrozenModelAndReceipt',
                     'TestWebPluginResearchRealPreServiceRunUpgradePreviewRollbackAndDisable',
                     'TestWebPluginResearchAcceptedBadPackagesCannotFetchForgeOrSpend',
                     'TestWebPluginResearchLateForgetAndGuardianAtPublicationBoundary',
                     'TestWebPluginResearchBudgetApprovalBeforeAuxiliaryModel',
                     'TestWebPluginResearchPendingModelCannotPublishOrRepeat',
                     'TestWebPluginResearchLargeCompleteEvidenceDoesNotUsePureResultLimit',
                     'TestWebPluginFetchActualUpgradeOriginalRunRollbackAndSafeReplay',
                     'TestWebPluginFetchAcceptedForgedRangeCannotInventPreview',
                     'TestWebPluginFetchLateForgetAndGuardianBeforePublication',
                     'TestWebPluginSourceFormattingActualHTTPUpgradeRawRowsAndRetainedRun',
                     'TestWebPluginRawSourceFactsKeepFullSanitizedRows',
                     'TestWebPluginSourceFormattingAcceptedBadPlanCannotReleaseOrReplay',
                     'TestWebPluginSourceFormattingLargeSanitizedInputUsesOriginalDescriptor',
                     'TestWebPluginSourceFormattingExaOriginalLimitAndIndependentUpgrade',
                     'TestWebPluginSourceFormattingLateForgetBeforeSourceRelease',
                     'TestWebPluginFetchRealReleaseRecoveryKeepsOriginalPreviewAndReceipt',
                     'TestWebPluginHTMLActualUpgradeRetainedRunRollbackAndDisable',
                     'TestWebPluginHTMLCompleteEightMiBSourceAndDerivedText',
                     'TestWebPluginHTMLLateForgetAndGuardianCannotReleaseDerivedText',
                     'TestWebPluginImageActualUpgradeRetainedRunRollbackAndDisable',
                     'TestWebPluginImageCompleteEightMiBSourceAndLargeDerivedOutput',
                     'TestWebPluginImageAcceptedForgedMetadataCannotPublishOrFallback',
                     'TestWebPluginImageLateForgetAndGuardianCannotPublishAttachment',
                     'TestWebPluginImageChildUsesRetainedOriginalParserAndPrivateScope',
                     'TestWebPluginImageRealReleaseRecoveryDoesNotReplayUnknownSource',
                     'TestWebAccessParserBundleDoesNotExecuteSourceAndRetainsCompleteNotices',
                     'TestWebAccessParserBundleRetainsActualOwnerImportsAfterSourceReplacement',
                     'TestWebAccessParserBundleRejectsEscapesUnboundedInputAndNetworkResolution',
                     'TestWebAccessParserBundleRejectsSpecialFilesWithoutBlocking',
                     'TestWebAccessParserBundleKeepsCanvasAbsentAndRejectsEscapingNotice',
                     'TestWebAccessParserResourceActualHTMLPDFMatchOriginalDependencyBytes',
                     'TestWebAccessParserResourceRetainsActualCommandAndModuleAcrossReplacement',
                     'TestWebAccessParserResourceRejectsCorruptionPathsAndUnboundedCapture',
                     'TestWebAccessParserResourceOSBoundaryDeniesAmbientFSNetworkForkAndEnvironment',
                     'TestWebAccessParserResourceOriginalEightMiBInputLargeOutputAndCancellation',
                     'TestWebPluginExtractActualHTMLPDFUpgradeRetainedResourcesRollbackAndDisable',
                     'TestWebPluginExtractGuardianRejectsActualModuleAndForgedScopeOrProof',
                     'TestWebPluginExtractLateForgetAndGuardianBeforePublication',
                     'TestWebPluginExtractChildRetainsOriginalGraphAfterInstallationReplacement',
                     'TestWebPluginExtractRealReleaseRecoveryDoesNotReplayOriginalNeverTool',
                     'TestWebPluginExtractCustomAbsoluteCommandAndScriptKeepFullInputAndLargeResult',
                     'TestWebPluginExtractMissingCatalogAndChangedConfigCannotUseRetainedAuthority'):
            self.assertEqual(selected(root), 1, f'{root} is missing from Web race')
        directory = Path(__file__).resolve().parents[2] / 'internal/agent/sqlite'
        for source in directory.glob('plugin_web*_test.go'):
            for root in re.findall(r'^func (TestWebPlugin\w+)\(', source.read_text(), re.MULTILINE):
                self.assertEqual(selected(root), 1,
                                f'{source.name}: {root} is missing from partitioned Web race')

    def test_default_computer_business_gate_is_a_required_system_lane(self):
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('computer-plugins', lanes)
        command = ci.command('computer-plugins')
        self.assertIn('make build', command)
        self.assertEqual(command.count('--gate computer-plugins'), 1)
        from runner import REGISTRY
        self.assertEqual(REGISTRY['computer-plugins'], 'verify_computer_plugins.py')
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_artifact_round_trips_run_in_required_tools_lane(self):
        self.assertIn('artifacts', CORE)
        self.assertEqual(ci.command('tools-provider').count('--gate artifacts'), 1)

    def test_default_web_business_gate_is_a_required_system_lane(self):
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('web-plugins', lanes)
        command = ci.command('web-plugins')
        self.assertIn('make build', command)
        self.assertEqual(command.count('--gate web-plugins'), 1)
        from runner import REGISTRY
        self.assertEqual(REGISTRY['web-plugins'], 'verify_web_plugins.py')
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_default_mcp_business_gate_is_a_required_system_lane(self):
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('mcp-plugins', lanes)
        command = ci.command('mcp-plugins')
        self.assertIn('make build', command)
        self.assertIn('go test -race -p 1 -timeout=15m', command)
        self.assertIn("^Test(MCP|ToolUnknown|ArtifactQueryRecovery|TaskControlCancelTreeAndInvocationOutcomes)", command)
        self.assertEqual(command.count('--gate mcp-plugins'), 1)
        from runner import REGISTRY
        self.assertEqual(REGISTRY['mcp-plugins'], 'verify_mcp_plugins.py')
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)

    def test_default_connected_apps_business_and_namespaces_are_required(self):
        from runner import REGISTRY
        lanes = {lane['lane']: lane for lane in ci.plan('system')['include']}
        self.assertIn('connected-apps-plugins', lanes)
        command = ci.command('connected-apps-plugins')
        self.assertIn('make build', command)
        self.assertEqual(command.count('--gate connected-apps-plugins'), 1)
        self.assertEqual(REGISTRY['connected-apps-plugins'], 'verify_connected_apps_plugins.py')
        self.assertNotIn('|| true', command)
        self.assertNotIn('continue-on-error', command)
        recipe = subprocess.check_output(['make', '-n', 'test-connected-apps'],
            cwd=Path(__file__).resolve().parents[2], text=True)
        import race
        self.assertIn('python3 tests/system/race.py --part apps', recipe)
        self.assertIn('python3 tests/system/race.py --part apps-sqlite', recipe)
        self.assertEqual(race.groups([race.SQLITE, race.BRIDGE, race.RECORDER, race.APP], 'apps-sqlite'), [('remainder', [race.SQLITE])])
        self.assertEqual(race.groups([race.SQLITE, race.BRIDGE, race.RECORDER, race.APP], 'apps'), [('app', [race.APP])])
        self.assertRegex('TestConnectedAppsDefaultProductionCompositionAndColdLifecycle', race.APP_PATTERN)
        self.assertRegex('TestAppsGraphicsActualVersionsRefsColdFrozenEffectsAndReceipts', race.APP_PATTERN)
        self.assertRegex('TestAppsBrowserActualSealedProducerRouteRollbackAndColdReference', race.APP_PATTERN)
        for name in ('TestAppsAccountInventoryActualDefaultPrivateStateAndCold',
                     'TestAppsAccountInventoryActualIndependentVersionsFrozenAndDisable',
                     'TestAppsAccountInventoryActualLargeWindowsAndInterWindowFence',
                     'TestAppsAccountInventoryAcceptedEffectKeepsOriginalLinkWithoutReplay',
                     'TestAppsAccountInventoryOriginalProfileRollbackAndFirstInstall',
                     'TestAppsAccountInventoryOriginalVersionLabelCannotForgeProtocolProfile',
                     'TestAppsAccountInventoryAllRetainedPreServiceProfilesKeepOriginalColdRead'):
            self.assertRegex(name, race.APP_PATTERN)
        tokens = shlex.split(recipe)
        self.assertNotIn('./internal/app', tokens)  # App runs once through the audited batch inventory.
        self.assertNotIn('./internal/agent/sqlite', tokens)
        self.assertEqual(tokens[tokens.index('-run') + 1], race.APPS_SQLITE_PATTERN)
        for package in ('./internal/plugin/appsmethods/...', './internal/plugin/appsdirectory/...', './internal/plugin/appsaccounts/...', './pkg/rsiplugin', './internal/rsi', './internal/tool/codemode'):
            self.assertIn(package, tokens)
        pattern = tokens[tokens.index('-run') + 1]
        for name in ('TestConnectedAppNamespacesKeepExactAccountBoundary',
                     'TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable',
                     'TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle',
                     'TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures',
                     'TestAppsDirectoryActualOrderCompleteSourceVersionColdAndInvalidWindows',
                     'TestAppsDirectoryActualCardStatesOriginalRowsVersionAndRevisionFence',
                     'TestAppsDirectoryActualInventoryOriginalReferencesVersionAndCold',
                     'TestAppsDirectoryActualPrivateOrderLargeOriginalColdAndFences',
                     'TestAppsAccountIntentActualNativeEffectsVersionColdAndPrivateFences',
                     'TestAppsAuthConfigActualOriginalSourceChoiceVersionAndFences',
                     'TestAppsAuthConfigExactPreServiceColdInflightAndOptInBoundary',
                     'TestAppsSessionFlowActualOriginalEffectsVersionColdAndFences',
                     'TestAppsSessionFlowExactPreServiceColdInflightAndOptInBoundary',
                     'TestAppsSessionFlowHostRejectsForgedEffectsAndPreservesAcceptedLease',
                     'TestAppsAccountCheckActualOriginalSourceVersionColdAndFences',
                     'TestAppsAccountCheckExactPreServiceColdInflightAndOptInBoundary',
                     'TestAppsAccountCheckManagedMatchesOriginalCacheAndReceipts',
                     'TestAppsAccountCheckHostRejectsInventedStatusAuthority',
                     'TestAppsAccountCloseActualOriginalEffectsVersionColdAndFences',
                     'TestAppsAccountCloseExactPreServiceColdInflightAndIndependentOwnerBoundary',
                     'TestAppsAccountCloseManagedMatchesOriginalCacheAndReceipts',
                     'TestAppsAccountCloseHostRejectsForgedOrderAndKeepsSourceProof',
                     'TestAppsReconnectActualOriginalEffectsVersionColdAndFences',
                     'TestAppsReconnectExactPreServiceColdInflightAndOptInBoundary',
                     'TestAppsCompositionManagedOriginal224PagesAndSidebar',
                     'TestAppsCompositionRejectsMissingDuplicateAndUnknownSections',
                     'TestAppsCompositionPreservesCopyEscapingAndOriginalFields',
                     'TestAppsGraphicsOriginal28BodiesAndActualOwningResource',
                     'TestAppsGraphicsPassiveVariantAndSharedModelsIndependence',
                     'TestAppsGraphicsRejectsActiveSourcesReflectionAndChangedBindings',
                     'TestAppsGraphicsActualVersionsRefsColdFrozenEffectsAndReceipts',
                     'TestAppsMarkupPassiveSourceAndMeaningfulVariant',
                     'TestAppsMarkupRejectsReflectionExecutionAndChangedBindings',
                     'TestAppsMarkupActualResourceVersionsRefsColdFrozenEffectsAndReceipts',
                     'TestAppsMarkupOnlyNeverRestoresWholeNative',
                     'TestAppsSidebarViewOriginal34BodiesAndActualResource',
                     'TestAppsSidebarViewRejectsMissingOrDuplicateAtoms',
                     'TestAppsSidebarViewActualVersionsRefsColdFrozenEffectsAndReceipts',
                     'TestAppsSidebarViewOnlyNeverRestoresWholeNative',
                     'TestAppsCardViewOriginal512BodiesAndActualSealedResource',
                     'TestAppsCardViewRejectsInvalidScopesAndMissingControls',
                     'TestAppsCardViewActualResourceVersionsRefsColdAndUnavailable',
                     'TestAppsCardViewOnlyNeverRestoresWholeNative',
                     'TestAppsLocalizationActualV2MatchesOriginalHostDisplayText',
                     'TestAppsLocalizationActualOwnedTextV1RefsColdFrozenEffectsAndNoFallback',
                     'TestAppsLocalizationCompositionOnlyNeverRestoresNative',
                     'TestAppsCompositionActualVersionsColdOriginalProfileOnlyAndUnavailable',
                     'TestAppsInterfaceRejectsUnsafePlanWithoutNativeFallback',
                     'TestAppsInterfaceManagedOriginal512TemplatesAnd32Errors',
                     'TestAppsInterfaceActualProfileFrozenEffectsColdAndUnavailable',
                     'TestAppsLogoActualAccountFragmentsKeepOriginalVersionAndAcceptedAction',
                     'TestAppsLogoActualOwnerHTTPOriginal65FrozenReferencesColdAndDisable',
                     'TestAppsLogoManagedOriginalHTTPBytesSourcesAndCache',
                     'TestAppsLogoHostRejectsInventedCacheSourceBytesAndRevocation',
                     'TestAppsSettingsActualOriginalHTTPRowsDraftAndSave',
                     'TestAppsSettingsExactPreServiceColdInflightAndIndependentBoundary',
                     'TestAppsSettingsHostPreservesOriginalRowsAndExplicitDraftAuthority',
                     'TestAppsSettingsFactsExactBytesAndRejectMalformedInput',
                     'TestAppsAuthConfigHostRejectsChangedIntentAndForgedCommands',
                     'TestAppsAuthConfigHostPinsSourceCursorAndOriginalPrior',
                     'TestAppsAuthConfigFactsExactBytesAndRejectInvalidMetadata',
                     'TestAppsConnectIntentScopedFactsAndOriginalPendingIdentity',
                     'TestAppsConnectIntentRejectsForgedAliasReferenceAndManagedFallback',
                     'TestAppsReconnectUnavailableIntentDoesNotCloseOriginalConnection',
                     'TestAppsAccountIntentFactsPreserveExactPrivateAliasBytes',
                     'TestEditorialInventoryOrderCompletePrivateWindowsAndOriginalRevision',
                     'TestEditorialInventoryOrderInvalidPermutationNeverReleasesPartialOrNative',
                     'TestInventoryOrderFactsExactOriginalBytesAndRejectMalformedInput',
                     'TestInputStreamPreservesLargeIdentityAndDoesNotWidenOrdinaryRead',
                     'TestEditorialCardStatesUseOnlyScopedFactsAndOriginalPublicValue',
                     'TestEditorialCardStatesRejectForgedReferencesAndPartialGroups',
                     'TestAppsOriginalInventoryAndStateProjectionRemainExact',
                     'TestAppsBrowserActualSealedProducerRouteRollbackAndColdReference',
                     'TestAppsBrowserSourceHasOpaqueCSPAndEscapesProgram',
                     'TestAppsStylesOriginalAndIndependentLayout',
                     'TestAppsStylesRejectForeignAuthorityAndHiddenDisclosures',
                     'TestAppsBrowserRequiresExactSelectedReferenceAndCannotUseNative',
                     'TestCatalogueOrderFactsUseExactPrivateWireAndRejectCorruption',
                     'TestCatalogueOrderFactsPreserveCapturedNativeNormalizationBeyondInputSize',
                     'TestEditorialOrderCompleteWindowsFreezeOriginalFactsAndLabels',
                     'TestEditorialOrderInvalidOrPartialPermutationNeverFallsBack',
                     'TestEditorialDetailSnapshotCannotChangeSourceAuthority',
                     'TestEditorialLabelsProjectOriginalFieldsWithPresencePlans',
                     'TestEditorialLabelsNeverInferNativeFromInvalidSelection',
                     'TestAppsLabelsProjectBeforeCatalogueOrder',
                     'TestEditorialCaptionsProjectPrivateOriginalSourcesAcrossBatches',
                     'TestEditorialCaptionsRejectInvalidReferencesWithoutPartialOrFallback',
                     'TestEditorialCategoryPlanUsesOnlyOriginalReferences',
                     'TestEditorialCategoryPlanRejectsInvalidMasksAndDuplicateReferences',
                     'TestEditorialLookupCannotInventAuthorityOrAnInterpreter',
                     'TestEditorialRouteCannotInventLiveAuthorityOrRewriteCursorSize',
                     'TestClientCatalogPageKeepsOriginalQueryAndOpaqueContinuation',
                     'TestEditorialManagedFailureNeverRestoresLegacyData',
                     'TestEditorialQuerySnapshotCannotBeReusedForAnotherSearch',
                     'TestConnectedAppsBootstrapVerifiesOfflineAndPreservesOwnerSelection',
                     'TestConnectedAppsGuidanceAndMCPColdProjectionShareOriginalAdmission'):
            self.assertIsNotNone(re.match(pattern, name), name + ' is missing from required race recipe')

    def test_editorial_browser_branch_remains_in_required_software_gate(self):
        root = Path(__file__).resolve().parents[2]
        recipe = subprocess.check_output(['make', '-n', 'test-browser'], cwd=root, text=True)
        commands = [shlex.split(line) for line in recipe.splitlines()]
        app_commands = [tokens for tokens in commands if './internal/app' in tokens]
        owning = [tokens for tokens in app_commands if '-run' in tokens and re.match(
            tokens[tokens.index('-run') + 1], 'TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable')]
        connect = [tokens for tokens in app_commands if '-run' in tokens and re.match(
            tokens[tokens.index('-run') + 1], 'TestAppsConnectViewActualBrowserAndRecoveryLink')]
        settings_view = [tokens for tokens in app_commands if '-run' in tokens and re.match(
            tokens[tokens.index('-run') + 1], 'TestAppsSettingsViewActualBrowserDraftSave')]
        directory_controls = [tokens for tokens in app_commands if '-run' in tokens and re.match(
            tokens[tokens.index('-run') + 1], 'TestAppsDirectoryControlsActualBrowserNavigationFilterAndPagination')]
        pages = [tokens for tokens in app_commands if '-run' in tokens and re.match(
            tokens[tokens.index('-run') + 1], 'TestAppsPagesActualBrowserOriginalControlsAndViews')]
        self.assertEqual(len(pages), 1)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', pages[0])
        self.assertIn('STEWARD_APP_PAGES_BROWSER_EVIDENCE=', ' '.join(pages[0]))
        self.assertEqual(len(directory_controls), 1)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', directory_controls[0])
        self.assertIn('STEWARD_APP_DIRECTORY_CONTROLS_BROWSER_EVIDENCE=', ' '.join(directory_controls[0]))
        self.assertEqual(len(settings_view), 1)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', settings_view[0])
        self.assertIn('STEWARD_APP_SETTINGS_VIEW_BROWSER_EVIDENCE=', ' '.join(settings_view[0]))
        self.assertEqual(len(connect), 1)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', connect[0])
        self.assertIn('STEWARD_APP_CONNECT_VIEW_BROWSER_EVIDENCE=', ' '.join(connect[0]))
        self.assertEqual(len(owning), 1)
        self.assertIn('STEWARD_TEST_APP_BROWSER=1', owning[0])
        batches = [row for row in commands if any('STEWARD_APP_COMPOSITION_BROWSER_EVIDENCE=' in token for token in row)]
        self.assertEqual(len(batches), 3)
        patterns = [row[row.index('-run') + 1] for row in batches]
        self.assertEqual(sum(re.match(pattern, 'TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsSettingsActualOriginalHTTPRowsDraftAndSave') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsLogoActualOwnerHTTPOriginal65FrozenReferencesColdAndDisable') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsInterfaceActualManagedBrowserLifecycle') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsCompositionActualVersionsColdOriginalProfileOnlyAndUnavailable') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsLocalizationActualBrowserOwnedFeedbackAndNative') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsCardViewActualManagedResourceBrowser') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsSidebarViewActualBrowserAndAcceptedLink') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsMarkupActualBrowserAndRecoveryLink') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsGraphicsActualBrowserAndRecoveryLink') is not None for pattern in patterns), 1)
        self.assertEqual(sum(re.match(pattern, 'TestAppsAccountMarkupActualBrowserAndRecoveryLink') is not None for pattern in patterns), 1)
        self.assertIn('STEWARD_APP_SETTINGS_BROWSER_EVIDENCE=', ' '.join(owning[0]))

    def test_required_check_rejects_missing_failed_and_cancelled_work(self):
        for docs in ('true', 'false'):
            selected = 'skipped' if docs == 'true' else 'success'
            valid = dict(scope=dict(result='success', outputs=dict(docs_only=docs)),
                         software=dict(result=selected), system=dict(result=selected))
            self.assertTrue(ci.passed(valid))
            for name in valid:
                with self.subTest(docs=docs, job=name):
                    self.assertFalse(ci.passed({key: value for key, value in valid.items() if key != name}))
                    for status in ('failure', 'cancelled', 'skipped' if selected == 'success' else 'success'):
                        self.assertFalse(ci.passed({**valid, name: dict(result=status)}))
        self.assertFalse(ci.passed(dict(scope=dict(result='success', outputs={}),
                                       software=dict(result='success'), system=dict(result='success'))))

    def test_model_menu_actual_lifecycle_is_required(self):
        from runner import REGISTRY
        self.assertEqual(REGISTRY['model-menu-plugins'], 'verify_model_menu_plugins.py')
        self.assertIn('model-menu-plugins', ci.SYSTEM['tools-provider'])
        self.assertIn('model-menu-plugins', CORE)
        self.assertIn('.*ModelMenu.*', ci.RACE)
        self.assertIn('ModelConnection.*', ci.RACE)
        self.assertIn('ModelPicker.*', ci.RACE)
        self.assertIn('TerminalInterface.*', ci.RACE)
        self.assertIn('./internal/tui', ci.RACE)
        self.assertIn('FeishuStatus.*', ci.RACE)
        self.assertIn('feishu-plugins', ci.SYSTEM['tools-provider'])
        self.assertIn('feishu-plugins', CORE)
        self.assertEqual(REGISTRY['feishu-plugins'], 'verify_feishu_plugins.py')
        gate=(Path(__file__).resolve().parents[2]/'tests/system/verify_feishu_plugins.py').read_text()
        self.assertIn('scenario(binary,business,managed=True)',gate)
        for scenario in ('feishu_topic_delivery','feishu_task_questions','feishu_artifact_reviews'):
            self.assertIn(scenario,gate)
        self.assertIn('ModelSettings.*', ci.RACE)
        self.assertIn('SettingsDraft.*', ci.RACE)

    def test_managed_capability_forms_remain_required(self):
        from runner import REGISTRY
        self.assertIn('capability-form-plugins',CORE)
        self.assertIn('capability-form-plugins',ci.SYSTEM['startup-ui'])
        self.assertEqual(REGISTRY['capability-form-plugins'],'verify_capability_form_plugins.py')
        self.assertIn('Capability.*',ci.RACE)
        self.assertIn('./internal/config/setup',ci.RACE)

    def test_trace_missing_catalog_recovery_remains_required(self):
        command=ci.command('capabilities-race')
        self.assertIn('./internal/observability/sqlite ./internal/rsi',command)
        for scenario in ('ManagedHistoryWithoutCatalog','ColdMissingCatalog','PartialActivationSchema'):
            self.assertIn(scenario,command)
