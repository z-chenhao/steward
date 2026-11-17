// Package conversation owns independently versioned Main communication and work
// methods. Host retains identity, safety, frozen context and durable execution.
package conversation

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/conversation/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed main-policy.md
var rolePolicy string

//go:embed execution-policy.md
var executionPolicy string

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(c.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		if req.Role != "main" || len(c.Inputs) != 0 {
			return rsiplugin.Result{}, errors.New("unsupported conversation role")
		}
		return rsiplugin.JSONResult(ctx, contract.Policy{RolePolicy: strings.TrimSuffix(rolePolicy, "\n"), ExecutionPolicy: strings.TrimSuffix(executionPolicy, "\n")})
	}}}
}
