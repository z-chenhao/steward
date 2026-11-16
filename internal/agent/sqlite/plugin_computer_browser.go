package sqlite

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type computerSnapshotResource interface {
	BrowserSnapshotResource(string) (computer.BrowserSnapshotter, error)
}

func (t *tools) computerBrowserProjection(ctx context.Context, inv tool.Invocation) (computer.BrowserSnapshotter, computer.BrowserProjector, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.BrowserProjectionContract)
	if err != nil || host == nil {
		return nil, nil, err
	}
	binding, err := host.ServiceInstance(ctx, revision, contract.BrowserProjectionContract)
	if err != nil {
		return nil, nil, err
	}
	var config struct {
		Driver string `json:"browser_snapshot_driver"`
	}
	if json.Unmarshal(binding.Config, &config) != nil || config.Driver == "" {
		return nil, nil, rsi.ErrInvalid
	}
	provider, ok := t.runs.computerProvider.(computerSnapshotResource)
	if !ok {
		return nil, nil, rsi.ErrNotFound
	}
	snapshot, err := provider.BrowserSnapshotResource(config.Driver)
	if err != nil {
		return nil, nil, errors.Join(rsi.ErrInvalid, err)
	}
	if snapshot == nil {
		return nil, nil, rsi.ErrNotFound
	}
	project := func(ctx context.Context, in computer.BrowserProjectionFacts) (computer.BrowserProjection, error) {
		facts := contract.BrowserProjectionFacts{BodyUnits: in.BodyUnits, Rows: make([]contract.BrowserRowFacts, len(in.Rows))}
		for i, r := range in.Rows {
			facts.Rows[i] = contract.BrowserRowFacts{RoleUnits: r.RoleUnits, AriaUnits: r.AriaUnits, InnerUnits: r.InnerUnits, PlaceholderUnits: r.PlaceholderUnits, ValueUnits: r.ValueUnits}
		}
		raw, err := json.Marshal(facts)
		if err != nil {
			return computer.BrowserProjection{}, err
		}
		result, err := host.ReadService(ctx, revision, contract.BrowserProjectionContract, raw, nil)
		if err != nil {
			return computer.BrowserProjection{}, err
		}
		var plan contract.BrowserProjection
		if rsiplugin.DecodeResult(result, &plan) != nil {
			return computer.BrowserProjection{}, rsi.ErrInvalid
		}
		out := computer.BrowserProjection{BodyUnits: plan.BodyUnits, Rows: make([]computer.BrowserRowView, len(plan.Rows))}
		for i, r := range plan.Rows {
			out.Rows[i] = computer.BrowserRowView{Index: r.Index, Label: r.Label, LabelUnits: r.LabelUnits, ValueUnits: r.ValueUnits}
		}
		return out, nil
	}
	return snapshot, project, nil
}
