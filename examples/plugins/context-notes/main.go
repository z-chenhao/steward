// context-notes is a complete Go plugin example: a configurable context hook
// and a schema-bound text service. It imports only the public SDK and stdlib.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	var config struct {
		Note string `json:"note"`
	}
	plugin := rsiplugin.Handlers{PluginID: "steward.context-notes", Version: "1.0.0",
		OnPrepare: func(_ context.Context, p rsiplugin.PrepareInput) error { return rsiplugin.Decode(p.Config, &config) },
		Hooks: map[rsiplugin.Stage]func(context.Context, rsiplugin.HookCall) (rsiplugin.HookResult, error){rsiplugin.BeforeModelRequest: func(_ context.Context, c rsiplugin.HookCall) (rsiplugin.HookResult, error) {
			out := rsiplugin.HookResult{}
			if config.Note != "" {
				out.Output.AdditionalContext = []rsiplugin.Message{{Kind: "plugin_context", Text: config.Note}}
			}
			return out, nil
		}},
		Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){"steward.text-normalize/1": func(_ context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := rsiplugin.Decode(c.Input, &input); err != nil {
				return rsiplugin.Result{}, err
			}
			input.Text = strings.TrimSpace(input.Text)
			raw, err := json.Marshal(input)
			return rsiplugin.Result{Output: raw}, err
		}},
	}
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, plugin); err != nil {
		fmt.Fprintln(os.Stderr, "plugin stopped")
		os.Exit(1)
	}
}
