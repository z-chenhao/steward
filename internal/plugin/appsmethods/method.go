package appsmethods

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.connected-apps", Version: "1.0.2", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Method: method, contract.Guidance: guidance, contract.MailReview: mailReview}}
}

// The method owns the search/execute workflow and full-result presentation.
// Each phase is a capability for this invocation's original operation, never
// a generic HTTP client. The Host independently verifies all preconditions.
func method(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input contract.Request
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	if input.Operation != "search" && input.Operation != "execute" {
		return rsiplugin.Result{}, errors.New("unknown Apps method")
	}
	hasCursor := false
	source := func(phase string) (contract.SourceReply, error) {
		var facts contract.SourceReply
		request := contract.SourceRequest{Phase: phase}
		if phase == "search" && !hasCursor {
			request.Limit = 8
		}
		err := rsiplugin.CallResource(ctx, contract.Source, phase, request, &facts)
		return facts, err
	}
	binding, err := source("binding")
	if err != nil {
		return rsiplugin.Result{}, err
	}
	hasCursor = binding.HasCursor
	name := "app-tools.json"
	if input.Operation == "execute" {
		name = "app-result.json"
		if binding.NeedsAccount {
			if _, err := source("account"); err != nil {
				return rsiplugin.Result{}, err
			}
		}
		if _, err := source("descriptor"); err != nil {
			return rsiplugin.Result{}, err
		}
	}
	result, err := source(input.Operation)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	mode := "inline"
	if result.Bytes > 60000 {
		mode = "artifact"
	}
	return rsiplugin.JSONResult(ctx, contract.Plan{Mode: mode, Name: name, Message: "The complete app response is in this artifact."})
}
