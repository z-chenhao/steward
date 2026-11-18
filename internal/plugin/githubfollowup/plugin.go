package githubfollowup

import (
	"bytes"
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: step}}
}
func step(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var req contract.Request
	if err := rsiplugin.Decode(call.Input, &req); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(req.History) > 63 || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.InputID {
		return rsiplugin.Result{}, errors.New("invalid source step")
	}
	var facts bytes.Buffer
	if err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	count := 0
	if len(req.History) > 0 {
		count = req.History[len(req.History)-1].Count
	}
	if count < 0 || facts.Len() != count {
		return rsiplugin.Result{}, errors.New("incomplete source eligibility")
	}
	plan := contract.Plan{Rows: []int{}, DisplayBytes: 16384, DisplaySuffix: "\n[truncated; open the source URL for full text]", Followup: contract.Recipe{ObservationBytes: 4096, GoalBytes: 4096, Notice: "\n[Preview truncated; consult the original Task/source before relying on missing details.]"}, IntervalSeconds: req.PollSeconds}
	if plan.IntervalSeconds == 0 {
		plan.IntervalSeconds = 60
	}
	for i, flag := range facts.Bytes() {
		if flag > 1 {
			return rsiplugin.Result{}, errors.New("invalid eligibility flag")
		}
		if flag == 1 {
			plan.Rows = append(plan.Rows, i)
		}
	}
	for _, kind := range []string{"review_comment", "review", "check_run"} {
		var last *contract.Page
		for i := range req.History {
			if req.History[i].Kind == kind {
				last = &req.History[i]
			}
		}
		if last == nil {
			plan.Next = &contract.Page{Kind: kind, Number: 1}
			break
		}
		if last.Count >= 100 {
			if last.Number >= 21 {
				plan.ErrorCode = "HISTORY_LIMIT"
				break
			}
			plan.Next = &contract.Page{Kind: kind, Number: last.Number + 1}
			break
		}
	}
	return rsiplugin.JSONResult(ctx, plan)
}
