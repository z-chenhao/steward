package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"

	"github.com/z-chenhao/assistant/internal/app"
)

func runTaskMail(ctx context.Context, a *app.App, flags *flag.FlagSet, in io.Reader, out io.Writer) error {
	var value any
	var err error
	switch flags.Arg(1) {
	case "enable-plugin":
		value, err = a.EnableTaskMail(ctx, flags.Arg(2))
	case "bind":
		value, err = a.BindTaskMailbox(ctx, flags.Arg(2), flags.Arg(3))
	case "close":
		err = a.CloseTaskMailbox(ctx, flags.Arg(2))
		value = struct {
			Closed bool `json:"closed"`
		}{Closed: err == nil}
	case "messages":
		var before int64
		if flags.NArg() == 4 {
			before, err = strconv.ParseInt(flags.Arg(3), 10, 64)
			if err != nil || before < 1 {
				return errors.New("invalid mailbox cursor")
			}
		}
		value, err = a.RecentTaskMail(ctx, flags.Arg(2), before)
	case "receive":
		value, err = a.ReceiveTaskMail(ctx, flags.Arg(2), flags.Arg(3), in)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(value)
}
