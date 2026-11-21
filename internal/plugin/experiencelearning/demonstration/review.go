// Package demonstration reviews an operator's private procedure using a finite,
// inert sample. Transitions only advance an integer in memory.
package demonstration

import (
	"context"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validStep(s contract.Step) bool {
	return (s.Action == "activate" || s.Action == "enter" || s.Action == "select") && strings.TrimSpace(s.Label) != "" && utf8.RuneCountInString(s.Label) <= 120 && strings.IndexFunc(s.Label, unicode.IsControl) < 0
}

func Review(q contract.DemoRequest) (contract.DemoResult, error) {
	var out contract.DemoResult
	invalid := errors.New("invalid demonstration or sample")
	if len(q.Steps) == 0 || len(q.Steps) > 32 || len(q.Sample) > 64 || q.Goal < 0 || q.Goal > 64 || strings.TrimSpace(q.Name) == "" || utf8.RuneCountInString(q.Name) > 100 || strings.IndexFunc(q.Name, unicode.IsControl) >= 0 {
		return out, invalid
	}
	type key struct {
		state int
		step  contract.Step
	}
	transitions := map[key]int{}
	for _, edge := range q.Sample {
		k := key{edge.From, edge.Step}
		if _, dup := transitions[k]; dup || !validStep(edge.Step) || edge.From < 0 || edge.From > 64 || edge.To < 0 || edge.To > 64 {
			return out, invalid
		}
		transitions[k] = edge.To
	}
	state := 0
	for i, step := range q.Steps {
		if !validStep(step) {
			return contract.DemoResult{}, invalid
		}
		action := map[string]string{"activate": "Activate", "enter": "Enter the requested value in", "select": "Choose the requested option in"}[step.Action]
		out.Steps = append(out.Steps, fmt.Sprintf("%d. %s %q.", i+1, action, step.Label))
		if i == out.Matched {
			if next, exists := transitions[key{state, step}]; exists {
				state = next
				out.Matched++
			}
		}
	}
	out.Verified = len(q.Sample) > 0 && out.Matched == len(q.Steps) && state == q.Goal
	return out, nil
}

func Call(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var q contract.DemoRequest
	if err := rsiplugin.Decode(call.Input, &q); err != nil {
		return rsiplugin.Result{}, err
	}
	result, err := Review(q)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, result)
}
