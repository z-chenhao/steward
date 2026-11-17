package appsaccounts

import (
	"bytes"
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func authConfig(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.AuthRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.AuthConfigInputID {
		return rsiplugin.Result{}, errors.New("invalid auth input")
	}
	var raw bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &raw); err != nil {
		return rsiplugin.Result{}, err
	}
	rows, err := contract.DecodeAuthRows(raw.Bytes(), request.Count)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	plan := chooseAuthConfig(request, rows)
	return rsiplugin.JSONResult(ctx, plan)
}
func chooseAuthConfig(request contract.AuthRequest, rows []contract.AuthRow) contract.AuthPlan {
	switch request.Step {
	case "begin":
		if request.Explicit {
			return contract.AuthPlan{Kind: "fetch", Source: "explicit"}
		}
		if request.Saved {
			return contract.AuthPlan{Kind: "fetch", Source: "saved"}
		}
		return contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: 200}
	case "error":
		if !request.Override && (request.Status == 401 || request.Status == 403) {
			return contract.AuthPlan{Kind: "unreadable"}
		}
		return contract.AuthPlan{Kind: "reject"}
	case "result":
		var chosen *int
		required := contract.AuthToolkit | contract.AuthEnabled | contract.AuthRouter | contract.AuthValidID
		if request.Override {
			required |= contract.AuthExplicitMatch
		} else {
			required |= contract.AuthUnmanaged
		}
		for i, row := range rows {
			if row.Facts&required != required {
				continue
			}
			if chosen == nil || row.Created > rows[*chosen].Created || row.Created == rows[*chosen].Created && row.ID > rows[*chosen].ID {
				value := i
				chosen = &value
			}
		}
		if request.Override {
			if chosen == nil {
				return contract.AuthPlan{Kind: "reject"}
			}
			return contract.AuthPlan{Kind: "done", Row: chosen}
		}
		if request.CursorSeen || request.HasNext && request.Page >= 20 {
			return contract.AuthPlan{Kind: "reject"}
		}
		if request.HasNext {
			return contract.AuthPlan{Kind: "next", Limit: 200, Row: chosen}
		}
		if chosen == nil && request.NoHosted {
			return contract.AuthPlan{Kind: "requires_config"}
		}
		return contract.AuthPlan{Kind: "done", Row: chosen}
	default:
		return contract.AuthPlan{Kind: "reject"}
	}
}
