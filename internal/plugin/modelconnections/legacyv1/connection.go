// Package legacyv1 interprets never-activated model connection installations and
// embeddings. Delete after those explicit consumers retire; managed calls cannot
// reach this path, including deactivation and missing current services.
package legacyv1

import "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"

func Connection(f contract.ConnectionFacts) contract.ConnectionDefaults {
	out := contract.ConnectionDefaults{WireAPI: "openai_completions", ContextWindow: 128000, MaxOutputTokens: 16384, ImageInput: true}
	if f.Preset == contract.TerminalSetup {
		out.EndpointDefault = "http://127.0.0.1:8000/v1"
		out.TokenLimit = 1048576
	}
	if f.Preset == contract.CLIShorthand {
		out.ContextWindow = 8192
		out.MaxOutputTokens = 1024
		out.ImageInput = false
	}
	if f.Service == "openai" {
		out.EndpointOverride = "https://api.openai.com/v1"
	}
	if f.WireAPI != "" {
		out.WireAPI = f.WireAPI
	}
	if f.AccountBacked {
		out.WireAPI = "openai_responses"
		out.EndpointOverride = ""
	}
	if f.Login != nil && f.Login.RestoreOwnedCurrent {
		restore := true
		out.RestoreOwnedCurrent = &restore
	}
	if f.Login != nil && len(f.Login.Models) > 0 {
		out.LoginModel = f.Login.Models[0]
	}
	if f.Reconnect != nil {
		out.ReconnectAction = "probe"
		if !f.Reconnect.HasCurrent || f.Reconnect.TargetCurrent {
			out.ReconnectAction = "activate"
		}
	}
	if f.Added != nil {
		out.AddedAction = "activate"
		if f.Added.HasCurrent {
			out.AddedAction = "probe"
			if f.AccountBacked {
				out.AddedAction = "save"
			}
		}
	}
	return out
}
