package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm"
)

func dispatchCommand(ctx context.Context, application *app.App, cfg config.Config, command command, options commandOptions, input llm.ProfileInput, in io.Reader, out, errOut io.Writer) error {
	flags := options.flags
	foreground := func(action func(context.Context) error) error {
		if options.testBridge == "" || options.testEvents {
			return application.WithEvents(ctx, cfg, action)
		}
		application.StartMemory(ctx)
		return action(ctx)
	}
	if command.kind != commandTerminal && flags.NArg() == 0 || command.kind == commandChat && flags.Arg(1) == "work" {
		application.StartMemory(ctx)
	}
	switch command.kind {
	case commandTaskMail:
		return runTaskMail(ctx, application, flags, in, out)
	case commandPlugin:
		if flags.Arg(1) == "parser-resource" {
			return runParserResource(ctx, application, cfg, flags.Arg(2), out)
		}
		return runPlugin(ctx, application, flags, in, out)
	case commandCoding:
		if strings.HasPrefix(flags.Arg(1), "draft-") {
			return runCodingDraft(ctx, application, flags, in, out)
		}
		if flags.Arg(1) == "report-pr-exact" {
			value, err := application.SaveCodePRExactReport(ctx, flags.Arg(2), flags.Arg(3), cfg.ExecutionConfig().RepositorySource)
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(value)
		}
		if flags.Arg(1) == "report-pr" {
			value, err := application.SaveCodePRReport(ctx, flags.Arg(2), flags.Arg(3))
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(value)
		}
		if flags.Arg(1) == "report" {
			value, err := application.SaveCodeReport(ctx, flags.Arg(2))
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(value)
		}
		value, err := application.CodeReport(ctx, flags.Arg(2), flags.Arg(1) == "diff")
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(value)
	case commandAudio:
		var value any
		var err error
		if flags.Arg(1) == "enable-plugin" {
			value, err = application.EnableAudioTranscript(ctx, flags.Arg(2))
		} else {
			value, err = application.TranscribeLocalAudio(ctx, flags.Arg(2), flags.Arg(3))
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(value)
	case commandEvidence:
		return runEvidence(ctx, application, in, out)
	case commandTask:
		return runTask(ctx, application, flags, in, out)
	case commandGitHub:
		return runGitHub(ctx, application, flags, in, out)
	case commandWeb:
		return runWeb(ctx, application, cfg, out, foreground)
	case commandChannel:
		return runChannel(ctx, application, flags, in, out)
	case commandDelivery:
		return runDelivery(ctx, application, flags, in, out)
	case commandAutomation:
		return runAutomation(ctx, application, flags, in, out, foreground)
	case commandArtifact:
		return runArtifact(ctx, application, flags, in, out)
	case commandSkill:
		return runSkill(ctx, application, flags, in, out)
	case commandMemory:
		return runMemory(ctx, application, flags, in, out)
	case commandSpend:
		values, err := application.Spend(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(values)
	case commandApproval:
		return runApproval(ctx, application, flags, in, out)

	case commandTerminal:
		return runTerminal(ctx, application, in, out, foreground)
	case commandChat:
		return runChat(ctx, application, flags, in, out)
	case commandModel:
		return runModel(ctx, application, flags, in, out, input, options.testBridge != "", errOut)
	}

	diagnostics, err := application.Diagnostics(ctx)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(diagnostics); err != nil {
		return err
	}
	if command.kind == commandRelay {
		return application.Relay(ctx, cfg)
	}
	if flags.NArg() == 0 {
		<-ctx.Done()
	}
	return nil
}
