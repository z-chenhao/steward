package modelconnections

import (
	_ "embed"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed view/models.html
var modelViewSource string

//go:embed view/copy.json
var modelCopySource string

func ProjectSettings(f contract.SettingsFacts) contract.SettingsProjection {
	out := contract.SettingsProjection{Rows: []contract.SettingsChoice{}}
	if f.Draft != nil {
		out.Draft = projectDraft(*f.Draft)
	}
	if f.WithView {
		out.Copy = &contract.View{Resource: contract.CopyResource, Digest: rsiplugin.FileDigest([]byte(modelCopySource)), Source: modelCopySource}
		out.View = &contract.View{Resource: contract.ViewResource, Digest: rsiplugin.FileDigest([]byte(modelViewSource)), Source: modelViewSource}
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

func projectDraft(f contract.DraftFacts) *contract.DraftProjection {
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
