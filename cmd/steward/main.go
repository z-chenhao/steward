package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattn/go-isatty"
	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/bridge"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/startup"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := os.Args[1:]
	if len(args) == 0 && isatty.IsTerminal(os.Stdin.Fd()) {
		args = []string{"start"}
	}
	if err := run(ctx, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "steward:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) (err error) {
	if handled, err := parserresource.Exec(args); handled {
		return err
	}
	if handled, err := rsi.ExecPlugin(args); handled {
		return err
	}
	if len(args) == 1 && args[0] == "-model-bridge" {
		return (&bridge.Server{}).Serve(ctx, in, out)
	}
	if len(args) == 1 && args[0] == "-model-bridge-version" {
		_, err := fmt.Fprintln(out, bridge.Version)
		return err
	}
	if handled, err := runTestPeer(ctx, args, in, out); handled {
		return err
	}
	options, err := parseOptions(args, errOut)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	flags := options.flags
	command, err := selectCommand(flags, in)
	if err != nil {
		return err
	}
	if command.kind == commandPlugin && flags.Arg(1) == "seal" {
		return sealPlugin(flags.Arg(2), out)
	}
	if command.kind == commandPlugin && flags.Arg(1) == "parser-bundle" {
		return bundleParser(ctx, flags.Arg(2), flags.Arg(3), flags.Arg(4))
	}
	if command.kind == commandService {
		return runService(ctx, options, out)
	}
	input, err := readModelInput(command, flags, in)
	if err != nil {
		return err
	}
	cfg, err := config.Load(options.configPath, os.LookupEnv)
	if err != nil {
		return err
	}
	// Installation defaults are applied by the normal startup/service path.
	// Control and explicit drain commands do not discover a different runtime.
	if (command.start || command.kind == commandDoctor || flags.Arg(0) == "status") && options.configPath == "" && options.testBridge == "" {
		home, err := runtimeHome()
		if err != nil {
			return err
		}
		configureRuntime(&cfg, home)
	}
	if command.kind == commandDoctor {
		return doctor(ctx, cfg, out)
	}
	if command.start {
		if options.configPath == "" && options.testBridge == "" {
			var diagnostics bytes.Buffer
			if err := doctor(ctx, cfg, &diagnostics); err != nil {
				fmt.Fprint(errOut, diagnostics.String())
				return err
			}
		}
		if _, external := os.LookupEnv("STEWARD_NATS_URL"); options.configPath == "" && !external {
			broker, err := startup.OpenLocalBroker(ctx, cfg.App.DataRoot)
			if err != nil {
				return err
			}
			defer broker.Close()
			cfg.NATS.URL = broker.URL()
		}
	}
	if handled, e := runOfflineOperation(ctx, cfg, flags, options.testSecrets, out); handled {
		return e
	}
	if command.kind == commandComputer && flags.Arg(1) == "prepare-base" {
		if cfg.Computer == nil {
			return errors.New("computer configuration required")
		}
		if err := macosvm.ProvisionBase(ctx, cfg.Computer.Root, flags.Arg(2), cfg.Computer.Compatibility); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"base": "sealed"})
	}
	var application *app.App
	if (options.testBridge == "" || options.testEvents) && (command.kind == commandWeb || command.kind == commandTerminal || command.kind == commandAutomation && flags.Arg(1) == "work") {
		application, err = app.OpenRuntime(ctx, cfg, options.testBridge, options.testSecrets)
	} else if command.kind == commandEvidence {
		application, err = app.OpenEvidenceControl(ctx, cfg, options.testBridge, options.testSecrets)
	} else if command.kind == commandPlugin || command.kind == commandModel || command.kind == commandOps || command.kind == commandGitHub || command.kind == commandCoding || command.kind == commandTask && flags.Arg(1) == "feedbacks" || flags.Arg(0) == "status" {
		application, err = app.OpenControl(ctx, cfg, options.testBridge, options.testSecrets)
	} else if options.testBridge != "" {
		application, err = app.OpenWithTestBridge(ctx, cfg, options.testBridge)
	} else if options.testSecrets != "" {
		application, err = app.OpenWithTestSecrets(ctx, cfg, options.testSecrets)
	} else {
		application, err = app.Open(ctx, cfg)
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, application.Close()) }()
	if command.kind == commandOps {
		return runOperations(ctx, application, cfg, flags, out)
	}
	if err := options.configureClock(application); err != nil {
		return err
	}
	if command.kind == commandComputer {
		state, err := application.ComputerStatus(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(state)
	}
	if err := options.configureExecution(ctx, application, out); err != nil {
		return err
	}
	return dispatchCommand(ctx, application, cfg, command, options, input, in, out, errOut)
}
