package web

import "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"

// legacyModelSettingsV1 serves only installations which have never activated
// Settings, including the initial menu-only migration graph. Remove when
// those retained installations no longer need the historical interpreter.
func legacyModelSettingsV1(f contract.SettingsFacts) contract.SettingsProjection {
	out := contract.SettingsProjection{Rows: []contract.SettingsChoice{}}
	if f.Draft != nil {
		out.Draft = legacyModelDraftV1(*f.Draft)
	}
	for _, r := range f.Rows {
		status := r.AuthStatus
		if !r.Verified && status != "logged_out" {
			status = "Needs attention"
		}
		group := ""
		if r.KnownAccount {
			group = r.Account
		}
		out.Rows = append(out.Rows, contract.SettingsChoice{Ref: r.Ref, Account: group, Open: r.Ref == f.Requested && f.Notice != "active", Status: status})
		if r.Ref == f.Requested {
			if f.Notice == "tested" && !r.Verified && status != "logged_out" {
				out.SuppressNotice = true
			}
			if f.Notice == "active" && !r.Current {
				out.SuppressNotice = true
			}
		}
	}
	return out
}

func legacyModelDraftV1(f contract.DraftFacts) *contract.DraftProjection {
	out := &contract.DraftProjection{Method: f.Method, Endpoint: "https://api.openai.com/v1", Service: "openai", WireAPI: "openai_completions"}
	if f.HasSource {
		out.UseSource = true
		out.Endpoint = ""
		out.WireAPI = ""
		out.Service = "custom"
		if f.OpenAIEndpoint {
			out.Service = "openai"
		}
	}
	return out
}
