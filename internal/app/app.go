// Package app is the composition root. Readiness and persistence belong to agent;
// process configuration and telemetry belong to their respective modules.
package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/agent/sqlite"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/config"
	configsetup "github.com/z-chenhao/assistant/internal/config/setup"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/conversation"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/delivery"
	deliveryfake "github.com/z-chenhao/assistant/internal/delivery/fake"
	deliveryterminal "github.com/z-chenhao/assistant/internal/delivery/terminal"
	"github.com/z-chenhao/assistant/internal/event"
	eventnats "github.com/z-chenhao/assistant/internal/event/nats"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/account"
	"github.com/z-chenhao/assistant/internal/llm/bridge"
	"github.com/z-chenhao/assistant/internal/llm/fake"
	"github.com/z-chenhao/assistant/internal/llm/pricing"
	"github.com/z-chenhao/assistant/internal/llm/secret"
	llmsqlite "github.com/z-chenhao/assistant/internal/llm/sqlite"
	"github.com/z-chenhao/assistant/internal/maintenance"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/operations"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tui"
)

type App struct {
	connectedApps           *connectedapps.Service
	connectedAppsRegistryMu sync.Mutex
	capabilitySettings      *configsetup.Manager
	core                    atomic.Pointer[runtimeCore]
	prepareCore             func(context.Context) error
	eventsRequired          atomic.Bool
	eventsReady             atomic.Bool
	runtimeWake             chan struct{}
	runtimeDone             <-chan struct{}
	rootLock                *os.File
	feishu                  *feishu.Client
	github                  *github.Service
	computer                *macosvm.Provider
	computerPreviewMu       sync.Mutex
	memoryCurator           *memory.Curator
	evidenceVault           *memory.Vault
	memoryOnce              sync.Once
	memoryCancel            context.CancelFunc
	memoryDone              chan struct{}
	skillConfig             *skill.Config
	store                   *sqlite.Store
	artifacts               artifactStore
	telemetry               *observability.Telemetry
	provider                *bridge.Client
	control                 llm.Control
	accounts                *account.Manager
	history                 *conversationsqlite.Store
	memory                  *memorysqlite.Store
	terminalInbox           *deliveryterminal.Adapter
	deliveryPolicy          delivery.Policy
	deliveryFake            *deliveryfake.Adapter
	testDelivery            string
	testBoundary            func(string)
	testAutomationClock     *time.Time
}

type runtimeCore struct {
	runs     *sqlite.Runs
	registry *tool.Registry
}

func (a *App) runtimeRuns() *sqlite.Runs { return a.core.Load().runs }

// OpenRuntime defers Tool/Computer initialization until the production broker
// phase. Explicit fixtures still use the very same composition and lifecycle.
func OpenRuntime(ctx context.Context, cfg config.Config, fixture, secretsPath string) (*App, error) {
	if fixture != "" && !fake.ValidFixture(fixture) {
		return nil, llm.InvalidProviderResponse
	}
	var secrets llm.SecretStore = secret.Files{DataRoot: cfg.App.DataRoot}
	if secretsPath != "" {
		secrets = secret.TestSocket{Path: secretsPath}
	}
	return open(ctx, cfg, fixture, secrets, true)
}

// Control commands remain usable when a Tool, Computer or Sidecar is broken.
// They never acquire the execution runtime or expose an Actor for dispatch.
func OpenControl(ctx context.Context, cfg config.Config, fixture, secretsPath string) (*App, error) {
	a, e := OpenRuntime(ctx, cfg, fixture, secretsPath)
	if e == nil {
		a.eventsRequired.Store(false)
	}
	return a, e
}

func Open(ctx context.Context, cfg config.Config) (*App, error) {
	return open(ctx, cfg, "", secret.Files{DataRoot: cfg.App.DataRoot}, false)
}

// OpenRuntimeWithSecrets binds an operator-authenticated principal and an
// externally managed SecretStore without selecting a cloud vendor or files.
func OpenRuntimeWithSecrets(ctx context.Context, cfg config.Config, secrets llm.SecretStore) (*App, error) {
	if secrets == nil {
		return nil, llm.ErrSecretStore
	}
	return open(ctx, cfg, "", secrets, true)
}

func OpenWithTestSecrets(ctx context.Context, cfg config.Config, path string) (*App, error) {
	return open(ctx, cfg, "", secret.TestSocket{Path: path}, false)
}

// OpenWithTestBridge is explicit test injection. No config default, environment
// discovery, missing credential, or saved active profile can select a Fake.
func OpenWithTestBridge(ctx context.Context, cfg config.Config, fixture string) (*App, error) {
	if !fake.ValidFixture(fixture) {
		return nil, llm.InvalidProviderResponse
	}
	return open(ctx, cfg, fixture, secret.Files{DataRoot: cfg.App.DataRoot}, false)
}

func open(ctx context.Context, cfg config.Config, fixture string, secrets llm.SecretStore, deferred bool) (*App, error) {
	if err := cfg.ValidateDefaults(); err != nil {
		return nil, err
	}
	lock, err := maintenance.Lock(cfg.App.DataRoot, false)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	if maintenance.Pending(cfg.App.DataRoot) {
		return nil, errors.New("restored data root is fenced; retire source and reconcile external outcomes, then use ops activate")
	}
	if cfg.Memory != nil && (!cfg.Memory.Valid() || cfg.Embedding != nil && !cfg.Embedding.Local()) {
		//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
		return nil, errors.New("Memory maintenance requires an encryption key reference and local embedding")
	}
	t, err := observability.OpenWithLimits(cfg.App.DataRoot, cfg.LogLevel(), cfg.App.LogMaxBytes, cfg.App.LogBackups)
	if err != nil {
		return nil, err
	}
	ctx, startup, err := t.Start(ctx, "app.startup")
	if err != nil {
		return nil, errors.Join(err, t.Close())
	}
	_, database, err := t.Start(ctx, "sqlite.open_and_migrate")
	if err != nil {
		startup.End(err)
		return nil, errors.Join(err, t.Close())
	}
	if err = maintenance.CheckVersions(ctx, cfg.App.DataRoot, SchemaVersions()); err != nil {
		database.End(err)
		startup.End(err)
		return nil, errors.Join(err, t.Close())
	}
	store, err := sqlite.Open(ctx, cfg.App.DataRoot)
	database.End(err)
	if err != nil {
		startup.End(err)
		return nil, errors.Join(err, t.Close())
	}
	if err = store.BindOwner(ctx, cfg.App.OwnerID); err != nil {
		_ = store.Close()
		_ = t.Close()
		return nil, err
	}
	store.Artifacts().Policy = cfg.Artifact
	store.ConfigureCostEstimator(pricing.New())
	a := &App{capabilitySettings: configsetup.New(cfg, secrets), rootLock: lock, store: store, telemetry: t, artifacts: store.Artifacts(), skillConfig: cfg.Skills}
	cfg = a.capabilitySettings.PrepareDefaults(ctx)
	a.skillConfig = cfg.Skills
	a.history, err = conversationsqlite.Open(ctx, cfg.App.DataRoot)
	if err != nil {
		_ = store.Close()
		_ = t.Close()
		return nil, err
	}
	identity, identityErr := store.Diagnostics(ctx)
	if identityErr != nil {
		a.Close()
		return nil, identityErr
	}
	if err = a.history.BindOwner(ctx, store.OwnerID(), identity.AgentID); err != nil {
		a.Close()
		return nil, err
	}
	a.terminalInbox = store.TerminalInbox()
	a.memory, err = memorysqlite.Open(ctx, cfg.App.DataRoot)
	if err != nil {
		a.Close()
		return nil, err
	}
	startup.Logger.Info("startup stage", "stage", "storage_ready")
	if cfg.Memory != nil {
		if err = a.memory.ConfigureVault(memory.Vault{Secrets: secrets, Ref: cfg.Memory.EncryptionKeyRef}); err != nil {
			a.Close()
			return nil, err
		}
	}
	if cfg.Embedding != nil {
		engine := embedding.Client{Config: *cfg.Embedding, Secrets: secrets}
		if err = a.memory.Configure(ctx, engine); err == nil {
			err = a.history.Configure(ctx, engine)
		}
		if err != nil {
			a.Close()
			return nil, err
		}
	}
	if cfg.Automatic("embedding") {
		a.memory.ConfigureLocal()
		a.history.ConfigureLocal()
	}
	a.deliveryPolicy = cfg.Delivery
	if cfg.Feishu != nil {
		a.feishu = feishu.NewClient(*cfg.Feishu, secrets)
	}
	a.control = llm.Control{Store: store.Models(), Secrets: secrets}
	a.connectedApps = &connectedapps.Service{Store: store, Secrets: secrets}
	a.connectedApps.Changed = a.refreshConnectedApps
	if cfg.GitHub != nil {
		a.github = &github.Service{Config: *cfg.GitHub, Store: store, Client: github.Client{Secrets: secrets}}
	}
	accountSecrets := secrets
	if _, production := secrets.(secret.Files); production {
		accountSecrets = secret.AccountFiles{DataRoot: cfg.App.DataRoot}
	}
	a.accounts = &account.Manager{Store: store.Models(), Secrets: accountSecrets, LockRoot: filepath.Join(cfg.App.DataRoot, "auth-locks")}
	a.control.AccountCatalog = a.AccountCatalog
	if fixture != "" {
		path, err := os.Executable()
		if err == nil {
			a.provider, err = bridge.OpenObserved(ctx, path, a.accounts.Observer(a.recordGeneration), "-test-provider-bridge", fixture, filepath.Join(cfg.App.DataRoot, "fake-provider-calls.jsonl"))
		}
		if err != nil {
			a.control.BridgeError = llm.NormalizeError(err.Error())
		} else {
			a.control.Probe = a.provider
			a.control.BridgeIdentity = "test-bridge-v1:" + fixture
		}
	}
	if fixture == "" && cfg.LLM.ProviderBridgeCommand != "" {
		a.provider, err = bridge.OpenLocal(ctx, cfg.LLM.ProviderBridgeCommand, cfg.LLM.ProviderBridgeScript, a.accounts.Resolver(store.Models().Resolver(secrets)), a.accounts.Observer(a.recordGeneration))
		if err != nil {
			a.control.BridgeError = llm.NormalizeError(err.Error())
		} else {
			a.control.Probe = a.provider
			a.control.BridgeIdentity = cfg.LLM.ProviderBridgeVersion
		}
	}
	if a.provider != nil {
		a.provider.SetDispatchObserver(store.DispatchRecorder().Record)
	}
	if fixture == "" {
		if a.provider != nil {
			a.accounts.Provider = a.provider
		}
		a.control.AccountAuth = a.accounts.Check
		if err = a.accounts.Recover(ctx); err != nil {
			_ = a.Close()
			return nil, err
		}
	}
	startup.Logger.Info("startup stage", "stage", "provider_ready")
	projection := store.Runs(a.history, nil)
	projection.ConfigureScheduler(cfg.Scheduler)
	a.core.Store(&runtimeCore{runs: projection})
	if cfg.Memory != nil {
		a.memoryCurator = &memory.Curator{Process: func(ctx context.Context) error { return a.runtimeRuns().CurateMemory(ctx) }, Deferred: func() { a.telemetry.Logger.Warn("memory maintenance deferred", "code", "MEMORY_UNAVAILABLE") }}
	}
	var prepareMu sync.Mutex
	prepared := false
	a.prepareCore = func(ctx context.Context) error {
		prepareMu.Lock()
		defer prepareMu.Unlock()
		if prepared {
			return nil
		}
		if err := a.prepareRuntime(ctx, cfg, fixture, secrets); err != nil {
			return err
		}
		prepared = true
		return nil
	}
	if deferred {
		a.eventsRequired.Store(true)
	} else if err = a.prepareCore(ctx); err != nil {
		a.Close()
		return nil, err
	}
	startup.Logger.Info("application started")
	startup.End(nil)
	success = true
	return a, nil
}

func SchemaVersions() map[string]int {
	return map[string]int{"state.db": sqlite.SchemaVersion(), "conversation.db": conversationsqlite.SchemaVersion, "memory.db": memorysqlite.SchemaVersion}
}

func (a *App) Operations(ctx context.Context) (operations.Report, error) {
	return a.store.Operations(ctx)
}
func (a *App) OperationalTrace(ctx context.Context, id string) (operations.RunTrace, error) {
	return a.store.OperationalTrace(ctx, id)
}
func (a *App) RetryEvent(ctx context.Context, id string, attempts int) error {
	return a.store.Events().Retry(ctx, id, attempts)
}

func (a *App) BrokerHealth(ctx context.Context, cfg config.Config) (eventnats.Health, error) {
	bus, e := eventnats.OpenWithSecrets(ctx, cfg.NATS, a.control.Secrets)
	if e != nil {
		return eventnats.Health{}, e
	}
	defer bus.Close()
	return bus.Health(ctx)
}

func (a *App) UpgradeBroker(ctx context.Context, cfg config.Config) (eventnats.Health, error) {
	bus, e := eventnats.Upgrade(ctx, cfg.NATS, a.control.Secrets)
	if e != nil {
		return eventnats.Health{}, e
	}
	defer bus.Close()
	return bus.Health(ctx)
}

func (a *App) Diagnostics(ctx context.Context) (agent.Diagnostics, error) {
	d, err := a.providerDiagnostics(ctx)
	if err == nil && a.eventsRequired.Load() && !a.eventsReady.Load() && d.AgentIngressEnabled {
		d.AgentIngressEnabled = false
		d.Readiness = agent.Degraded
	}
	return d, err
}

func (a *App) providerDiagnostics(ctx context.Context) (agent.Diagnostics, error) {
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		return d, err
	}
	return agent.EffectiveReadiness(ctx, d, modelAvailability(a.control.Configured), a.provider), nil
}

type modelAvailability func(context.Context, string) bool

func (check modelAvailability) Available(ctx context.Context, id string) bool {
	return check(ctx, id)
}

func (a *App) Models() *llmsqlite.Store   { return a.store.Models() }
func (a *App) ModelControl() llm.Control  { return a.control }
func (a *App) Accounts() *account.Manager { return a.accounts }
func (a *App) Catalog(ctx context.Context) ([]llm.CatalogModel, error) {
	if a.provider == nil {
		if a.control.BridgeError != nil {
			return nil, a.control.BridgeError
		}
		return nil, llm.ErrNoBridge
	}
	return a.provider.List(ctx)
}

func (a *App) Close() error {
	if a.feishu != nil {
		defer a.feishu.Close()
	}
	if a.memoryCancel != nil {
		a.memoryCancel()
		<-a.memoryDone
	}
	if a.provider != nil {
		_ = a.provider.Close()
	}
	a.computerPreviewMu.Lock()
	if a.computer != nil {
		a.computer.Close()
	}
	a.computerPreviewMu.Unlock()
	err := a.store.Close()
	err = errors.Join(err, a.history.Close())
	if a.memory != nil {
		err = errors.Join(err, a.memory.Close())
	}
	a.telemetry.Logger.Info("application stopped")
	err = errors.Join(err, a.telemetry.Close())
	if a.rootLock != nil {
		err = errors.Join(err, a.rootLock.Close())
		a.rootLock = nil
	}
	return err
}

func (a *App) recordGeneration(ctx context.Context, r llm.GenerationRequest, result llm.GenerationResult, latency time.Duration, cause error) error {
	err := a.store.Models().RecordUsageAnd(ctx, r, result, latency, cause, func(ctx context.Context, tx *sql.Tx, r llm.GenerationRequest, result llm.GenerationResult, cause error) error {
		if strings.HasPrefix(r.GenerationID, "memory:") {
			return a.runtimeRuns().AcceptMemoryGeneration(ctx, tx, r, result, cause)
		}
		return a.store.AcceptGeneration(ctx, tx, r, result, cause)
	})
	if err == nil && r.OwnerToken != "" && a.store.MainBoundary != nil {
		a.store.MainBoundary("after_generation_commit")
	}
	return err
}

func (a *App) SetTestMainBoundary(boundary func(string)) {
	a.store.MainBoundary = boundary
	a.testBoundary = boundary
}
func (a *App) Actor(sink agent.EventSink) agent.Actor {
	core := a.core.Load()
	var provider llm.LLMProvider
	if a.provider != nil && core.registry != nil && (!a.eventsRequired.Load() || a.eventsReady.Load()) {
		provider = core.runs.GuardProvider(a.provider)
	}
	return agent.Actor{Store: core.runs, Provider: provider, Registry: core.registry, Sink: sink,
		TraceError: func(error) { a.telemetry.Logger.Warn("run trace unavailable", "code", "TRACE_UNAVAILABLE") }, Ready: func(ctx context.Context) bool {
			if core.registry == nil || a.core.Load() != core {
				return false
			}
			if a.eventsRequired.Load() && !a.eventsReady.Load() {
				return false
			}
			d, err := a.Diagnostics(ctx)
			return err == nil && d.AgentIngressEnabled && d.ActiveModelProfileID != nil &&
				a.control.Available(ctx, *d.ActiveModelProfileID)
		}}
}
func (a *App) History(ctx context.Context) ([]conversation.Message, error) {
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		return nil, err
	}
	return a.history.List(ctx, d.AgentID)
}

func (a *App) Terminal() tui.Session {
	return tui.Session{State: a, Models: a.Models(), Accounts: a.accounts, DecideApproval: a.DecideApproval,
		Messages: a.TerminalHistory, Control: a.control, CurrentActor: func() agent.Actor { return a.Actor(nil) }, AwaitRuntime: a.WaitForRuntime, Discover: a.Discover,
		AccountCatalog: a.AccountCatalog, LoginEnvironment: a.LoginEnvironment}
}

// Relay runs infrastructure only. It never enables Agent ingress or a model.
func (a *App) Relay(ctx context.Context, cfg config.Config) error {
	return event.Relay(ctx, a.configuredOutbox(cfg), func(ctx context.Context) (event.EventBus, error) {
		bus, err := eventnats.OpenWithSecrets(ctx, cfg.NATS, a.control.Secrets)
		if err != nil {
			return nil, err
		}
		return bus, nil
	}, cfg.NATS.RetryInterval, a.telemetry.Logger)
}

func (a *App) CatalogProfile(ctx context.Context, id string) ([]llm.CatalogModel, error) {
	if a.provider == nil {
		return nil, llm.ErrNoBridge
	}
	p, err := a.Models().Profile(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.ProviderKind == "openai_account" {
		return a.AccountCatalog(ctx, p.AuthConnectionID)
	}
	return a.provider.CatalogForProfile(ctx, p)
}

func (a *App) AccountCatalog(ctx context.Context, id string) ([]llm.CatalogModel, error) {
	if a.provider == nil {
		return nil, llm.ErrNoBridge
	}
	return a.accounts.Catalog(ctx, id, a.provider.CatalogAt)
}

// LoginKey consumes only stdin bytes; credentials never enter arguments or metadata.
func (a *App) LoginKey(ctx context.Context, value []byte) (llm.AuthConnection, error) {
	return llm.RegisterAuth(ctx, a.Models(), a.control.Secrets, llm.AuthInput{Provider: "openai_compatible", AuthKind: "api_key"}, value)
}
func (a *App) LoginEnvironment(ctx context.Context, name string) (llm.AuthConnection, error) {
	return secret.RegisterEnvironment(ctx, a.Models(), name)
}

func (a *App) Discover(ctx context.Context, endpoint, authID string) ([]llm.CatalogModel, error) {
	if a.provider == nil {
		return nil, llm.ErrNoBridge
	}
	connection, err := a.Models().Connection(ctx, endpoint, authID, a.control.Secrets)
	if err != nil {
		return nil, err
	}
	return a.provider.CatalogAt(ctx, connection)
}

func (a *App) Activity(ctx context.Context) ([]task.Summary, error) {
	return a.runtimeRuns().Activity(ctx)
}

func (a *App) Spend(ctx context.Context) ([]agent.SpendView, error) {
	return a.runtimeRuns().Spend(ctx)
}

func (a *App) Approvals(ctx context.Context) ([]interaction.Approval, error) {
	return a.runtimeRuns().Approvals(ctx)
}
func (a *App) DecideApproval(ctx context.Context, id, hash, decision string) error {
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		return err
	}
	return a.runtimeRuns().Decide(ctx, d.AgentID, id, hash, decision)
}
func (a *App) ReconcileTools(ctx context.Context) ([]tool.ToolOutcome, error) {
	if err := a.runtimeRuns().ReconcileUnknown(ctx); err != nil {
		return nil, err
	}
	return a.runtimeRuns().ToolOutcomes(ctx)
}
