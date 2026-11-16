package terminalinterface

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func View() contract.Copy {
	return contract.Copy{
		Help:               "Steward — /setup /models /test ID /select ID /login [ACCOUNT_ID] /logout ACCOUNT_ID /history /status /approve ID HASH /deny ID HASH /quit",
		State:              "State: %s",
		InputPrompt:        "You> ",
		Repair:             "State: %s. Use /setup to repair the connection; history remains available.",
		Busy:               "Steward: busy",
		Idle:               "Steward: idle",
		InputRejected:      "Input rejected: %s",
		Ask:                "%s (/cancel to cancel)> ",
		Cancelled:          "Setup cancelled. Active model unchanged.",
		Retained:           "Profile retained. Retry with /test %s; after it passes, use /select %s. Use /setup to change settings.",
		BridgeMissing:      "Provider Bridge is not configured. Check the connection settings.",
		ProbeFailed:        "Connection test failed: %s",
		ProbeTimeout:       "The model did not finish this check in time. Check server load and model readiness, then retry explicitly.",
		ProbeIncompatible:  "This model must support streaming, Tool calls and Tool results. Check the server's API and chat template.",
		KnownFailure:       "Operation failed: %s",
		UnknownFailure:     "Operation failed. Check the connection and model settings, then retry explicitly.",
		Message:            "%s: %s",
		Attachment:         "%s attachment: %s",
		OtherPart:          "%s: [%s]",
		LoginURL:           "Open this private login URL in your browser (Ctrl-C cancels login): %s",
		LoginCode:          "Login code: %s",
		LoginProgress:      "Login in progress…",
		Workspace:          "Workspace: %s",
		LoginRequired:      "Complete the login prompt in your browser.",
		ApprovalSaved:      "Approval decision saved.",
		Task:               "%s: %s %s",
		Profile:            "%s %s %s",
		ProbePassed:        "Probe passed.",
		ActivateCommand:    "Activate with /select %s",
		CredentialStorage:  "Check your credential storage settings and try again.",
		AccountConnected:   "Account connected: %s",
		AccountLoggedOut:   "Account logged out.",
		UnknownCommand:     "Unknown command. Use /setup /models /test ID /select ID /login [ACCOUNT_ID] /logout ACCOUNT_ID /history /status /approve ID HASH /deny ID HASH /quit",
		Privacy:            "Privacy: requests and Tool schemas go to the selected provider or local endpoint. localhost is the Go Host. Credentials stay in SecretStore. Steward never switches providers automatically.",
		Activate:           "Activate %s via %s? Type yes",
		Selected:           "Model selected. New Runs use this model.",
		ConnectionMenu:     "Choose model connection\n  1. Local / OpenAI-compatible endpoint\n  2. OpenAI — Sign in with ChatGPT",
		LocalHelp:          "Local endpoints do not require ChatGPT login. Use /cancel to leave setup.",
		ConnectionPrompt:   "Connection [1/2]",
		Endpoint:           "Endpoint URL [%s]",
		KeyEnvironment:     "API key environment variable name [blank for no key; never paste a key]",
		CatalogUnavailable: "Catalog unavailable. Enter a model ID manually; Probe is still required.",
		AccountPrompt:      "Account ID [blank for new ChatGPT login]",
		ContextTokens:      "Context tokens",
		OutputTokens:       "Max output tokens",
		TokenPrompt:        "%s [%d]",
		TokenInvalid:       "Enter a whole number from 1 to %d.",
		ProfileSaved:       "Profile saved: %s",
		ProbeCapabilities:  "Probe passed: text, streaming, Tool calls, continuation, cancellation and clipping. Additional modalities and reasoning settings are unverified.",
		Testing:            "Testing connection and Agent capabilities… Each check allows up to %s; cold local models may need time to load. Ctrl-C exits.",
		Checking:           "Checking %s…",
		Model:              "  %d. Model: %s",
		ModelPrompt:        "Model ID or number",
		SingleModel:        " [Enter for %s]",
		ChooseListed:       "Choose a listed model by number or ID.",
		RecoveredState:     "\nState: %s"}
}
func Choose(f contract.ChoiceFacts) contract.Choice {
	if f.Empty && f.Count == 1 {
		return contract.Choice{Kind: "index", Index: 0}
	}
	if f.Exact >= 0 {
		return contract.Choice{Kind: "index", Index: f.Exact}
	}
	if f.Number >= 0 {
		return contract.Choice{Kind: "index", Index: f.Number}
	}
	if f.Manual && !f.Empty {
		return contract.Choice{Kind: "manual"}
	}
	return contract.Choice{Kind: "retry"}
}

func Project(q contract.Query) (contract.Result, error) {
	if q.Operation == "view" && q.Facts == nil {
		v := View()
		return contract.Result{View: &v}, nil
	}
	if q.Operation != "choose-model" || q.Facts == nil {
		return contract.Result{}, errors.New("invalid terminal operation")
	}
	f := *q.Facts
	if f.Count < 0 || f.Exact < -1 || f.Exact >= f.Count || f.Number < -1 || f.Number >= f.Count {
		return contract.Result{}, errors.New("invalid terminal facts")
	}
	c := Choose(f)
	return contract.Result{Choice: &c}, nil
}
func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var q contract.Query
		if err := rsiplugin.Decode(call.Input, &q); err != nil {
			return rsiplugin.Result{}, err
		}
		out, err := Project(q)
		if err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, out)
	}}}
}
