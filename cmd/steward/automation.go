package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/automation"
)

func runAutomation(ctx context.Context, application *app.App, flags *flag.FlagSet, in io.Reader, out io.Writer, foreground func(func(context.Context) error) error) error {
	if flags.Arg(1) == "work" {
		return foreground(application.WorkAutomations)
	}
	if flags.Arg(1) == "tick" {
		if e := application.ScheduleAutomations(ctx); e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]bool{"accepted": true})
	}
	q := automation.Request{Operation: "list"}
	if flags.Arg(1) == "manage" {
		raw, e := io.ReadAll(io.LimitReader(in, 16385))
		if e != nil || len(raw) > 16384 {
			return automation.ErrInvalid
		}
		q, e = automation.DecodeRequest(string(raw))
		if e != nil {
			return e
		}
	}
	key := flags.Arg(2)
	if key == "" {
		key = "list"
	}
	result, e := application.ManageAutomation(ctx, key, q)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
