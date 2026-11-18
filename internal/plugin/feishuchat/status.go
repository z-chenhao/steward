package feishuchat

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func ProjectStatus(f contract.StatusFacts) contract.StatusProjection {
	action := ""
	switch f.State {
	case "credentials_rejected":
		action = "Check the configured application credentials or replace the external access token."
	case "credentials_unavailable":
		action = "Check SecretStore access and connectivity to the Feishu API."
	case "not_configured":
		action = "Configure feishu and bind the exact owner and chat through channel manage."
	case "ready":
		action = ""
	case "unavailable":
		action = "Check connectivity, application activation and bot permissions."
	case "unchecked":
		action = "Run channel check to verify credentials without sending a message."
	}
	return contract.StatusProjection{Action: action}
}
func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Card: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var facts contract.CardFacts
		if err := rsiplugin.Decode(call.Input, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, legacyv1.ProjectCard(facts))
	}, contract.Status: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var facts contract.StatusFacts
		if err := rsiplugin.Decode(call.Input, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, ProjectStatus(facts))
	}}}
}
