package mcpbridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.mcp-bridge", Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Method: method, contract.Namespace: namespace}}
}

// The method owns the paginated rediscovery workflow and text presentation.
// The Host port retains the peer, frozen schema and exact approved dispatch.
func method(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	cursor := ""
	complete := false
	for page := 0; page < 16; page++ {
		var reply contract.SourceReply
		if err := rsiplugin.CallResource(ctx, contract.Source, fmt.Sprintf("list:%d", page), contract.SourceRequest{Phase: "list", Cursor: cursor}, &reply); err != nil {
			return rsiplugin.Result{}, err
		}
		if reply.NextCursor == "" {
			complete = true
			break
		}
		if reply.NextCursor == cursor {
			return rsiplugin.Result{}, errors.New("MCP cursor did not advance")
		}
		cursor = reply.NextCursor
	}
	if !complete {
		return rsiplugin.Result{}, errors.New("MCP catalog exceeds page limit")
	}
	var reply contract.SourceReply
	if err := rsiplugin.CallResource(ctx, contract.Source, "call", contract.SourceRequest{Phase: "call"}, &reply); err != nil {
		return rsiplugin.Result{}, err
	}
	plan := contract.Plan{Parts: []contract.Part{}, Separator: ""}
	if !reply.IsError {
		for index, text := range reply.Text {
			plan.Parts = append(plan.Parts, contract.Part{Index: index, End: len(text)})
		}
	}
	return rsiplugin.JSONResult(ctx, plan)
}
