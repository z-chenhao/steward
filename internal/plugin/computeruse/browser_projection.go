package computeruse

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func browserProjection(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var f contract.BrowserProjectionFacts
	if err := rsiplugin.Decode(call.Input, &f); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, planBrowserProjection(f))
}

func planBrowserProjection(f contract.BrowserProjectionFacts) contract.BrowserProjection {
	p := contract.BrowserProjection{BodyUnits: f.BodyUnits, Rows: make([]contract.BrowserRowView, len(f.Rows))}
	for i, r := range f.Rows {
		label, units := "role", min(r.RoleUnits, 256)
		switch {
		case r.AriaUnits > 0:
			label, units = "aria", r.AriaUnits
		case r.InnerUnits > 0:
			label, units = "inner", r.InnerUnits
		case r.PlaceholderUnits > 0:
			label, units = "placeholder", r.PlaceholderUnits
		}
		p.Rows[i] = contract.BrowserRowView{Index: i, Label: label, LabelUnits: units, ValueUnits: r.ValueUnits}
	}
	return p
}
