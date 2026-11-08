// Package config loads the process configuration. Model credentials and profiles
// are deliberately not part of this foundation's configuration surface.
package config

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/delivery"
	eventnats "github.com/z-chenhao/assistant/internal/event/nats"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool/mcp"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	WebAccess  webaccess.Config      `yaml:"web_access"`
	Defaults   *Defaults             `yaml:"defaults,omitempty"`
	SourcePath string                `yaml:"-" json:"-"`
	Web        *web.Config           `yaml:"web"`
	Feishu     *feishu.Config        `yaml:"feishu"`
	GitHub     *github.Config        `yaml:"github"`
	Computer   *macosvm.Config       `yaml:"computer"`
	Context    contextbuilder.Policy `yaml:"context"`
	Memory     *memory.Config        `yaml:"memory"`
	Embedding  *embedding.Config     `yaml:"embedding"`
	JavaScript *JavaScript           `yaml:"javascript"`
	Spend      agent.SpendPolicy     `yaml:"spend"`
	Scheduler  agent.SchedulerLimits `yaml:"scheduler"`
	Children   agent.ChildLimits     `yaml:"children"`
	MCP        []mcp.Config          `yaml:"mcp"`
	Skills     *skill.Config         `yaml:"skills"`
	Execution  *environment.Config   `yaml:"execution"`
	Delivery   delivery.Policy       `yaml:"delivery"`
	Version    int                   `yaml:"version"`
	Artifact   artifact.Policy       `yaml:"artifact"`
	App        App                   `yaml:"app"`
	NATS       eventnats.Options     `yaml:"nats"`
	Tools      ToolLimits            `yaml:"tools"`
	Tasks      Tasks                 `yaml:"tasks"`
	LLM        LLM                   `yaml:"llm"`
}

// Defaults selects product-managed resources. Explicit resource sections win.
// It is opt-in for operator configurations and enabled by the installed startup.
type Defaults struct {
	Memory   bool `yaml:"memory"`
	Search   bool `yaml:"search"`
	Skills   bool `yaml:"skills"`
	Computer bool `yaml:"computer"`
}

func (c Config) Automatic(id string) bool {
	if c.Defaults == nil {
		return false
	}
	switch id {
	case "memory":
		return c.Defaults.Memory && c.Memory == nil
	case "embedding":
		return c.Defaults.Search && c.Embedding == nil
	case "skills":
		return c.Defaults.Skills && c.Skills == nil
	case "computer":
		localOverride := c.Execution != nil && (c.Execution.Root != c.App.DataRoot+".computer" || len(c.Execution.ToolchainRoots) > 0 || c.Execution.RepositorySource != "")
		return c.Defaults.Computer && c.Computer == nil && !localOverride
	}
	return false
}

type JavaScript struct {
	Command string `yaml:"command"`
	Script  string `yaml:"script"`
}

type ToolLimits struct {
	ApprovalTTLSeconds int `yaml:"approval_ttl_seconds"`
	Global             int `yaml:"global_concurrency"`
	Agent              int `yaml:"agent_concurrency"`
	Run                int `yaml:"run_concurrency"`
	Provider           int `yaml:"provider_concurrency"`
	Tool               int `yaml:"tool_concurrency"`
}

type Tasks struct {
	RecentDays  int `yaml:"recent_days"`
	RecentLimit int `yaml:"recent_limit"`
}

type LLM struct {
	ProviderBridgeCommand string `yaml:"provider_bridge_command"`
	ProviderBridgeScript  string `yaml:"provider_bridge_script"`
	ProviderBridgeVersion string `yaml:"provider_bridge_version"`
}

type App struct {
	OwnerID     string `yaml:"owner_id,omitempty"`
	DataRoot    string `yaml:"data_root"`
	LogLevel    string `yaml:"log_level"`
	LogMaxBytes int64  `yaml:"log_max_bytes"`
	LogBackups  int    `yaml:"log_backups"`
}

// Load discovers the owner settings file beneath the selected data root when
// no explicit configuration is supplied. It never creates a file or directory.
func Load(path string, lookupEnv func(string) (string, bool)) (Config, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, errors.New("resolve home directory")
		}
		root := filepath.Join(home, ".local", "share", "steward")
		if value, ok := lookupEnv("STEWARD_DATA_ROOT"); ok {
			root = value
		}
		if !filepath.IsAbs(root) {
			return Config{}, errors.New("app.data_root must be an absolute path")
		}
		path = filepath.Join(root, "config.yaml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			cfg, e := Decode(nil, lookupEnv)
			cfg.SourcePath = path
			return cfg, e
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Config{}, errors.New("resolve configuration file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("open configuration file")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 256<<10+1))
	if err != nil || len(raw) > 256<<10 {
		return Config{}, errors.New("configuration exceeds 256 KiB")
	}
	cfg, err := Decode(raw, lookupEnv)
	cfg.SourcePath = path
	if err == nil {
		err = cfg.ValidateSourcePath(path)
	}
	return cfg, err
}

// Decode shares strict, whole-configuration validation with owner settings.
// A nil document selects built-in defaults; an empty file is invalid.
func Decode(raw []byte, lookupEnv func(string) (string, bool)) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, errors.New("resolve home directory")
	}
	cfg := Config{
		Version:   1,
		Context:   contextbuilder.DefaultPolicy(),
		Scheduler: agent.DefaultSchedulerLimits(),
		Children:  agent.DefaultChildLimits(),
		Delivery:  delivery.DefaultPolicy(),
		Artifact:  artifact.DefaultPolicy(),
		Tools:     ToolLimits{ApprovalTTLSeconds: 3600, Global: 32, Agent: 24, Run: 4, Provider: 16, Tool: 8},
		Tasks:     Tasks{RecentDays: 30, RecentLimit: 200},
		App:       App{DataRoot: filepath.Join(home, ".local", "share", "steward"), LogLevel: "info", LogMaxBytes: 16 << 20, LogBackups: 2},
		NATS:      eventnats.Options{URL: "nats://127.0.0.1:4222", StreamPrefix: "steward", Timeout: 2 * time.Second, AckWait: 30 * time.Second, RetryInterval: time.Second},
	}
	if raw != nil {
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		cfg.Version = 0
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, errors.New("invalid configuration YAML (fields, types, or syntax)")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Config{}, errors.New("configuration must contain exactly one YAML document")
		}
	}

	if value, ok := lookupEnv("STEWARD_DATA_ROOT"); ok {
		cfg.App.DataRoot = value
	}
	if value, ok := lookupEnv("STEWARD_LOG_LEVEL"); ok {
		cfg.App.LogLevel = value
	}
	if value, ok := lookupEnv("STEWARD_NATS_URL"); ok {
		cfg.NATS.URL = value
	}
	if value, ok := lookupEnv("STEWARD_NATS_STREAM_PREFIX"); ok {
		cfg.NATS.StreamPrefix = value
	}
	nativeBridge := cfg.LLM.ProviderBridgeVersion == "steward-go-v1" && filepath.IsAbs(cfg.LLM.ProviderBridgeCommand) && cfg.LLM.ProviderBridgeScript == "-model-bridge"
	legacyBridge := filepath.IsAbs(cfg.LLM.ProviderBridgeScript) && cfg.LLM.ProviderBridgeVersion == "pi-ai-1.3.0-v1"
	if (cfg.LLM.ProviderBridgeCommand == "") != (cfg.LLM.ProviderBridgeScript == "") || cfg.LLM.ProviderBridgeCommand != "" && !nativeBridge && !legacyBridge {
		return Config{}, errors.New("llm requires a native steward-go-v1 bridge or a supported external RPC bridge")
	}
	if err := cfg.WebAccess.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.JavaScript != nil && (!filepath.IsAbs(cfg.JavaScript.Command) || !filepath.IsAbs(cfg.JavaScript.Script)) {
		return Config{}, errors.New("javascript requires an absolute command and script")
	}
	if cfg.Computer != nil && (cfg.JavaScript == nil || cfg.Computer.Validate() != nil) {
		return Config{}, errors.New("computer requires JavaScript and an explicit private Lume configuration")
	}
	if cfg.Embedding != nil && cfg.Embedding.Validate() != nil {
		return Config{}, errors.New("invalid embedding provider, endpoint, model or dimensions")
	}
	if !cfg.Context.Valid() {
		return Config{}, errors.New("invalid context compaction policy")
	}
	if cfg.Memory != nil && !cfg.Memory.Valid() {
		return Config{}, errors.New("memory requires an encryption key reference")
	}
	if cfg.Memory != nil && cfg.Embedding != nil && !cfg.Embedding.Local() {
		return Config{}, errors.New("encrypted Memory requires a literal loopback embedding endpoint")
	}
	if err := cfg.Spend.Validate(); err != nil {
		return Config{}, err
	}
	if !cfg.Scheduler.Valid() {
		return Config{}, errors.New("invalid Worker scheduler limits")
	}
	if !cfg.Children.Valid() {
		return Config{}, errors.New("invalid Child Run safety limits")
	}
	if cfg.Web != nil {
		if err := cfg.Web.Validate(); err != nil {
			return Config{}, err
		}
	}
	if cfg.Feishu != nil {
		if err := cfg.Feishu.Validate(); err != nil {
			return Config{}, err
		}
	}
	if cfg.GitHub != nil {
		if err := cfg.GitHub.Validate(); err != nil {
			return Config{}, err
		}
	}
	if cfg.Delivery.Validate() != nil {
		return Config{}, errors.New("invalid delivery lease/retry/attempt policy")
	}
	if cfg.Artifact.Validate() != nil {
		return Config{}, errors.New("invalid artifact retention/quota/size/batch policy")
	}
	if err := cfg.NATS.Validate(); err != nil {
		return Config{}, err
	}
	if cfg.Tools.ApprovalTTLSeconds < 1 || cfg.Tools.ApprovalTTLSeconds > 86400 {
		return Config{}, errors.New("tools approval_ttl_seconds must be 1..86400")
	}
	for _, limit := range []int{cfg.Tools.Global, cfg.Tools.Agent, cfg.Tools.Run, cfg.Tools.Provider, cfg.Tools.Tool} {
		if limit < 1 || limit > 1024 {
			return Config{}, errors.New("tools concurrency limits must be 1..1024")
		}
	}
	if cfg.Tasks.RecentDays < 1 || cfg.Tasks.RecentDays > 3650 || cfg.Tasks.RecentLimit < 1 || cfg.Tasks.RecentLimit > 10000 {
		return Config{}, errors.New("tasks requires recent_days 1..3650 and recent_limit 1..10000")
	}
	if cfg.Version != 1 {
		return Config{}, errors.New("unsupported configuration version (expected 1)")
	}
	if !filepath.IsAbs(cfg.App.DataRoot) {
		return Config{}, errors.New("app.data_root must be an absolute path")
	}
	if cfg.App.LogMaxBytes < 1<<20 || cfg.App.LogMaxBytes > 1<<30 || cfg.App.LogBackups < 1 || cfg.App.LogBackups > 10 {
		return Config{}, errors.New("invalid bounded diagnostic log policy")
	}
	cfg.App.DataRoot = filepath.Clean(cfg.App.DataRoot)
	if cfg.Execution == nil && cfg.Computer == nil {
		cfg.Execution = &environment.Config{Root: cfg.App.DataRoot + ".computer"}
	}
	if cfg.Execution != nil && cfg.Computer != nil {
		return Config{}, errors.New("execution and computer are alternative backends; core file/command tools use the configured Computer")
	}
	if cfg.Computer != nil && (overlaps(cfg.Computer.Root, cfg.App.DataRoot) || cfg.Execution != nil && overlaps(cfg.Computer.Root, cfg.Execution.Root) || cfg.Skills != nil && overlaps(cfg.Computer.Root, cfg.Skills.Root)) {
		return Config{}, errors.New("computer storage must be separate from application data, workspace and Skills")
	}
	seenServers := map[string]bool{}
	providerDirectories := []string{}
	for _, server := range cfg.MCP {
		if server.Validate() != nil || seenServers[server.Name] || overlaps(server.Directory, cfg.App.DataRoot) || (cfg.Skills != nil && overlaps(server.Directory, cfg.Skills.Root)) || (cfg.Execution != nil && overlaps(server.Directory, cfg.Execution.Root)) || (cfg.Computer != nil && overlaps(server.Directory, cfg.Computer.Root)) {
			return Config{}, errors.New("invalid or duplicate MCP configuration")
		}
		for _, directory := range providerDirectories {
			if overlaps(directory, server.Directory) {
				return Config{}, errors.New("MCP provider directories must not overlap")
			}
		}
		providerDirectories = append(providerDirectories, server.Directory)
		seenServers[server.Name] = true
	}
	if cfg.Skills != nil && cfg.Skills.Validate() != nil {
		return Config{}, errors.New("invalid Skills configuration")
	}
	codeStorage := canonicalScopePath(cfg.App.DataRoot) + ".task-code"
	if cfg.Computer != nil && overlaps(codeStorage, cfg.Computer.Root) || cfg.Skills != nil && overlaps(codeStorage, cfg.Skills.Root) {
		return Config{}, errors.New("task code storage must be separate from Computer and Skills")
	}
	for _, server := range cfg.MCP {
		if overlaps(codeStorage, server.Directory) || overlaps(codeStorage, server.Command) {
			return Config{}, errors.New("task code storage cannot overlap MCP provider")
		}
	}
	if cfg.WebAccess.ExtractScript != "" {
		trusted := filepath.Dir(filepath.Dir(cfg.WebAccess.ExtractScript))
		var writable []string
		if cfg.Execution != nil {
			writable = append(writable, cfg.Execution.Root, cfg.Execution.RepositorySource, canonicalScopePath(cfg.App.DataRoot)+".task-code")
		}
		if cfg.Computer != nil {
			writable = append(writable, cfg.Computer.Root)
		}
		for _, root := range writable {
			if root != "" && (overlaps(root, trusted) || overlaps(root, cfg.WebAccess.ExtractCommand)) {
				return Config{}, errors.New("web extractor must be separate from execution scopes")
			}
		}
	}
	if cfg.Execution != nil {
		for _, root := range append([]string{cfg.Execution.Root}, cfg.Execution.ToolchainRoots...) {
			if overlaps(codeStorage, root) {
				return Config{}, errors.New("execution scope cannot expose Task code storage")
			}
		}
		for _, root := range cfg.Execution.ToolchainRoots {
			if overlaps(root, cfg.Execution.Root) {
				return Config{}, errors.New("execution workspace must be separate from read-only toolchains")
			}
		}
		roots := append([]string{cfg.Execution.Root}, cfg.Execution.ToolchainRoots...)
		if source := cfg.Execution.RepositorySource; source != "" {
			if cfg.Computer != nil || overlaps(source, cfg.Execution.Root) {
				return Config{}, errors.New("repository source requires a separate native execution workspace")
			}
			storage := codeStorage
			if overlaps(storage, source) {
				return Config{}, errors.New("repository source cannot overlap Task code storage")
			}
			for _, root := range roots {
				if overlaps(source, root) {
					return Config{}, errors.New("task code storage, source and execution scopes must be separate")
				}
			}
			for _, root := range []string{"/usr", "/bin", "/System", "/Library/Developer", "/Applications/Xcode.app/Contents/Developer", "/etc/alternatives", "/dev", "/proc"} {
				if overlaps(storage, root) {
					return Config{}, errors.New("task code storage must be outside system sandbox roots")
				}
			}
			for _, root := range []string{source, storage} {
				if cfg.JavaScript != nil && (overlaps(root, cfg.JavaScript.Script) || overlaps(root, cfg.JavaScript.Command)) ||
					cfg.LLM.ProviderBridgeScript != "" && overlaps(root, cfg.LLM.ProviderBridgeScript) {
					return Config{}, errors.New("task code source and storage must be separate from trusted runtimes")
				}
			}
			roots = append(roots, source, storage)
		}
		if cfg.Execution.Validate() != nil {
			return Config{}, errors.New("invalid execution workspace")
		}
		for _, root := range roots {
			if overlaps(root, cfg.App.DataRoot) || cfg.Skills != nil && overlaps(root, cfg.Skills.Root) {
				return Config{}, errors.New("execution scope must be separate from application data and Skills")
			}
			if cfg.JavaScript != nil && (overlaps(cfg.Execution.Root, cfg.JavaScript.Script) || overlaps(cfg.Execution.Root, cfg.JavaScript.Command)) {
				return Config{}, errors.New("execution workspace cannot contain trusted runtime")
			}
			if cfg.LLM.ProviderBridgeScript != "" && overlaps(cfg.Execution.Root, cfg.LLM.ProviderBridgeScript) {
				return Config{}, errors.New("execution workspace cannot contain model bridge")
			}
			for _, server := range cfg.MCP {
				if overlaps(root, server.Directory) || overlaps(cfg.Execution.Root, server.Command) {
					return Config{}, errors.New("execution scope cannot overlap MCP provider")
				}
			}
		}
	}

	switch cfg.App.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, errors.New("app.log_level must be debug, info, warn, or error")
	}
	cfg.App.DataRoot = filepath.Clean(cfg.App.DataRoot)
	if err := cfg.ValidateDefaults(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) defaultRoots() []string {
	var roots []string
	if c.Automatic("skills") {
		roots = append(roots, c.App.DataRoot+".skills")
	}
	if c.Automatic("computer") {
		roots = append(roots, c.App.DataRoot+".desktop")
	}
	return roots
}

func (c Config) ValidateDefaults() error {
	if c.Automatic("memory") && c.Embedding != nil && !c.Embedding.Local() {
		return errors.New("automatic encrypted Memory requires local search or a loopback embedding endpoint")
	}
	roots := []string{c.App.DataRoot}
	if c.Execution != nil {
		roots = append(roots, c.Execution.Root)
		roots = append(roots, c.Execution.ToolchainRoots...)
		if c.Execution.RepositorySource != "" {
			roots = append(roots, c.Execution.RepositorySource)
		}
	}
	if c.Computer != nil {
		roots = append(roots, c.Computer.Root)
	}
	if c.Skills != nil {
		roots = append(roots, c.Skills.Root)
	}
	for _, server := range c.MCP {
		roots = append(roots, server.Directory, server.Command)
	}
	if c.JavaScript != nil {
		roots = append(roots, c.JavaScript.Command, c.JavaScript.Script)
	}
	if c.WebAccess.ExtractScript != "" {
		roots = append(roots, c.WebAccess.ExtractCommand, filepath.Dir(filepath.Dir(c.WebAccess.ExtractScript)))
	}
	if c.LLM.ProviderBridgeScript != "" {
		roots = append(roots, c.LLM.ProviderBridgeScript)
	}
	for _, managed := range c.defaultRoots() {
		for _, root := range roots {
			if overlaps(managed, root) {
				return errors.New("automatic capability storage must be separate from configured workspaces and runtimes")
			}
		}
	}
	return nil
}

// ValidateSourcePath keeps owner configuration outside any model/provider
// readable or writable resource, using the same canonical isolation rules.
func (c Config) ValidateSourcePath(path string) error {
	roots := c.defaultRoots()
	if c.Execution != nil {
		roots = append(roots, c.Execution.Root)
		roots = append(roots, c.Execution.ToolchainRoots...)
	}
	if c.Computer != nil {
		roots = append(roots, c.Computer.Root)
	}
	if c.Skills != nil {
		roots = append(roots, c.Skills.Root)
	}
	for _, server := range c.MCP {
		roots = append(roots, server.Directory)
	}
	for _, root := range roots {
		if overlaps(root, path) {
			return errors.New("configuration file must be separate from execution, toolchain, Skills and provider scopes")
		}
	}
	return nil
}

func overlaps(a, b string) bool {
	// A new data directory may not exist yet. Resolve its closest existing
	// ancestor too, otherwise /alias/new-data can evade /real/provider scope.

	a = canonicalScopePath(a)
	b = canonicalScopePath(b)
	within := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func canonicalScopePath(path string) string {
	original := path
	tail := []string{}
	for {
		if p, e := filepath.EvalSymlinks(path); e == nil {
			return filepath.Join(append([]string{p}, tail...)...)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Clean(original)
		}
		tail = append([]string{filepath.Base(path)}, tail...)
		path = parent
	}
}

func (c Config) LogLevel() slog.Level {
	var level slog.Level
	// Load validates the value before this conversion.
	_ = level.UnmarshalText([]byte(c.App.LogLevel))
	return level
}

// ExecutionConfig supplies the product default even for programmatic callers.
func (c Config) ExecutionConfig() environment.Config {
	if c.Execution != nil {
		return *c.Execution
	}
	return environment.Config{Root: c.App.DataRoot + ".computer"}
}
