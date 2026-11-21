package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/secret"
	"github.com/z-chenhao/assistant/internal/operations/service"
	"github.com/z-chenhao/assistant/internal/startup"
)

func runService(ctx context.Context, options commandOptions, out io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("background service currently supports macOS user login sessions only")
	}
	operation := options.flags.Arg(1)
	root := options.flags.Arg(2)
	configPath := options.configPath
	if root == "" {
		cfg, err := config.Load(configPath, os.LookupEnv)
		if err != nil {
			return err
		}
		root = cfg.App.DataRoot
	}
	if configPath != "" {
		var err error
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := service.New(root, home, executable)
	if err != nil {
		return err
	}
	switch operation {
	case "run":
		return runBackground(ctx, m, root, options, out)
	case "inspect":
		var secrets llm.SecretStore = secret.Files{DataRoot: root}
		if options.testSecrets != "" {
			secrets = secret.TestSocket{Path: options.testSecrets}
		}
		v, err := m.InspectAndNotify(ctx, secrets)
		if err != nil && v.Code == "" {
			return err
		}
		return errors.Join(err, json.NewEncoder(out).Encode(v))
	case "alerts-feishu":
		if configPath == "" {
			configPath, err = m.DeploymentConfigPath()
			if err != nil {
				return err
			}
		}
		cfg, err := config.Load(configPath, func(key string) (string, bool) {
			if key == "STEWARD_DATA_ROOT" {
				return root, true
			}
			return os.LookupEnv(key)
		})
		if err != nil {
			return err
		}
		if cfg.Feishu == nil {
			return errors.New("configure a Feishu account before selecting its alert chat")
		}
		if err = m.ConfigureFeishuAlerts(options.flags.Arg(3), *cfg.Feishu); err != nil {
			return err
		}
	case "alerts-off":
		err = m.DisableAlerts()
	case "install":
		err = m.Install(ctx, configPath)
	case "start":
		err = m.Start(ctx)
	case "stop":
		err = m.Stop(ctx)
	case "uninstall":
		err = m.Uninstall(ctx)
	}
	if err != nil {
		return err
	}
	v, err := m.Status()
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(v)
}

func runBackground(ctx context.Context, manager *service.Manager, root string, options commandOptions, out io.Writer) (err error) {
	// launchctl may start us while the controlling process still commits its
	// operation. Wait outside all locks; never create a second runtime owner.
	var session *service.Session
	var configPath string
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		session, configPath, err = manager.Begin()
		if !errors.Is(err, service.ErrBusy) {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return err
		case <-time.After(50 * time.Millisecond):
		}
	}
	if errors.Is(err, service.ErrStopped) {
		return nil
	}
	if err != nil {
		return err
	}
	defer session.Close()
	failureCode := "startup_failed"
	defer func() {
		if err != nil {
			err = errors.Join(err, session.Heartbeat(failureCode))
		}
	}()
	lookup := func(name string) (string, bool) {
		if name == "STEWARD_DATA_ROOT" {
			return root, true
		}
		return os.LookupEnv(name)
	}
	cfg, err := config.Load(configPath, lookup)
	if err != nil {
		failureCode = "configuration_error"
		return err
	}
	if configPath == "" && options.testBridge == "" {
		home, e := runtimeHome()
		if e != nil {
			return e
		}
		configureRuntime(&cfg, home)
	}
	if cfg.Web == nil {
		if configPath != "" {
			failureCode = "configuration_error"
			return errors.New("background service requires an explicit loopback web configuration")
		}
		cfg.Web = &web.Config{Listen: "127.0.0.1:8787"}
	}
	if _, external := os.LookupEnv("STEWARD_NATS_URL"); configPath == "" && !external {
		broker, e := startup.OpenLocalBroker(ctx, cfg.App.DataRoot)
		if e != nil {
			return e
		}
		defer broker.Close()
		cfg.NATS.URL = broker.URL()
	}
	application, err := app.OpenRuntime(ctx, cfg, options.testBridge, options.testSecrets)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, application.Close()) }()
	if err = options.configureClock(application); err != nil {
		return err
	}
	if err = options.configureExecution(ctx, application, out); err != nil {
		return err
	}
	if session.NeedsRestartRecipe() {
		// Failure disables automatic restarts for this deployment. Manual control
		// and the ordinary Agent runtime remain independent of this optional policy.
		recipe, recipeErr := application.ServiceRestartRecipe(ctx)
		if recipeErr != nil {
			recipe = nil
		}
		if err = session.FreezeRestartRecipe(recipe); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	healthDone := make(chan error, 1)
	watchHealth := func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			enabled, e := session.Enabled()
			if e != nil || !enabled {
				cancel()
				healthDone <- e
				return
			}
			check, stop := context.WithTimeout(ctx, 3*time.Second)
			code := backgroundHealth(check, application)
			stop()
			if e := session.Heartbeat(code); e != nil {
				cancel()
				healthDone <- e
				return
			}
			select {
			case <-ctx.Done():
				healthDone <- nil
				return
			case <-ticker.C:
			}
		}
	}
	failureCode = "process_exited"
	err = runWeb(ctx, application, cfg, out, func(action func(context.Context) error) error {
		return application.WithEvents(ctx, cfg, func(workCtx context.Context) error {
			// Required runtime readiness is installed by WithEvents before the
			// first heartbeat; a persisted ready Profile alone is insufficient.
			go watchHealth()
			err := action(workCtx)
			cancel()
			return errors.Join(err, <-healthDone)
		})
	})
	// A normal logout/reboot is recoverable if the persisted enabled intent
	// remains true. A human stop remains disabled regardless of this heartbeat.
	return err
}

func backgroundHealth(ctx context.Context, application *app.App) string {
	d, err := application.Diagnostics(ctx)
	if err != nil {
		return "health_unavailable"
	}
	switch d.Readiness {
	case agent.SetupRequired:
		return "setup_required"
	case agent.Ready:
		return "ready"
	default:
		if d.ActiveModelProfileID != nil && !application.ModelControl().Configured(ctx, *d.ActiveModelProfileID) {
			return "credential_or_provider_unavailable"
		}
		return "dependency_unavailable"
	}
}
