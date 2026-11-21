// Package meetingprep identifies one upcoming meeting with related correspondence.
// Only subject lines and meeting titles enter the managed process.
package meetingprep

import (
	"context"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/meetingprep/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func subject(text string) string {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	for {
		prefix, remainder, found := strings.Cut(text, ":")
		if !found || prefix != "re" && prefix != "fw" && prefix != "fwd" {
			return text
		}
		text = strings.TrimSpace(remainder)
	}
}

func Select(q contract.Input) (contract.Pair, error) {
	none := contract.Pair{Meeting: -1, Thread: -1}
	if len(q.Meetings) > 20 || len(q.Subjects) > 10 {
		return none, errors.New("invalid source window")
	}
	for i, title := range q.Meetings {
		key := subject(title)
		if key == "" {
			continue
		}
		for j, mail := range q.Subjects {
			if key == subject(mail) {
				return contract.Pair{Meeting: i, Thread: j}, nil
			}
		}
		return contract.Pair{Meeting: i, Thread: -1}, nil
	}
	return none, nil
}
func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.1.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Select: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var q contract.Input
		if err := rsiplugin.Decode(c.Input, &q); err != nil {
			return rsiplugin.Result{}, err
		}
		pair, err := Select(q)
		if err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, pair)
	}}}
}
