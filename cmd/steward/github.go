package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/observer/github"
)

func runGitHub(ctx context.Context, application *app.App, flags *flag.FlagSet, in io.Reader, out io.Writer) error {
	scopes := application.GitHubScopes()
	if flags.Arg(1) == "scopes" {
		return json.NewEncoder(out).Encode(scopes)
	}
	scope := flags.Lookup("github-scope").Value.String()
	if scope == "" {
		if len(scopes) == 1 {
			scope = scopes[0]
		} else if len(scopes) == 0 && flags.Arg(1) == "status" {
			return json.NewEncoder(out).Encode(github.Status{State: "not_configured"})
		} else if len(scopes) == 0 && flags.Arg(1) == "binding" {
			return json.NewEncoder(out).Encode(github.TaskBinding{State: "not_configured", Mode: "off"})
		}
	}
	switch flags.Arg(1) {
	case "bind", "unbind":
		raw, err := io.ReadAll(io.LimitReader(in, 4097))
		if err != nil || len(raw) > 4096 {
			return errors.New("invalid GitHub task binding request")
		}
		q, err := github.ParseBinding(string(raw), flags.Arg(1) == "bind")
		if err != nil {
			return err
		}
		binding, err := application.BindGitHubTaskFor(ctx, scope, q, flags.Arg(1) == "bind")
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(binding)
	case "binding":
		binding, err := application.GitHubTaskBindingFor(ctx, scope)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(binding)
	case "poll":
		if err := application.PollGitHubFor(ctx, scope); err != nil {
			return err
		}
	case "stop", "resume":
		if err := application.SetGitHubWatchFor(ctx, scope, flags.Arg(1) == "resume"); err != nil {
			return err
		}
	case "events", "decisions":
		var before int64
		if flags.NArg() == 3 {
			var err error
			before, err = strconv.ParseInt(flags.Arg(2), 10, 64)
			if err != nil || before < 1 {
				return errors.New("invalid GitHub observation cursor")
			}
		}
		if flags.Arg(1) == "decisions" {
			decisions, err := application.GitHubRuleDecisionsFor(ctx, scope, before)
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(decisions)
		}
		events, err := application.GitHubObservationsFor(ctx, scope, before)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(events)
	}
	status, err := application.GitHubStatusFor(ctx, scope)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(status)
}
