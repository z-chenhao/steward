package webworkbench

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed view/workbench.html
var view string

//go:embed view/workbench.css
var styles string

//go:embed view/copy.json
var copyText string

//go:embed view/policy.json
var policyText []byte

func presentation() (contract.Presentation, error) {
	var policy contract.Policy
	if err := rsiplugin.DecodeInput(policyText, &policy); err != nil {
		return contract.Presentation{}, err
	}
	asset := func(s string) contract.Asset {
		return contract.Asset{Source: s, Digest: rsiplugin.FileDigest([]byte(s))}
	}
	return contract.Presentation{View: asset(view), Stylesheet: asset(styles), Copy: asset(copyText), Policy: policy}, nil
}
func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.5", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: func(_ context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var req struct{}
		if err := rsiplugin.Decode(c.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		p, err := presentation()
		if err != nil {
			return rsiplugin.Result{}, err
		}
		raw, err := json.Marshal(p)
		return rsiplugin.Result{Output: raw}, err
	}}}
}
