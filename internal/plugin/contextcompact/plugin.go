// Package contextcompact owns versioned working-set selection policy. Host owns
// source reconstruction, protected facts, budgets, summary model calls and replay.
package contextcompact

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(c.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		if len(c.Inputs) != 1 || c.Inputs[0].ID != req.InputID {
			return rsiplugin.Result{}, errors.New("missing authorized costs")
		}
		raw, err := rsiplugin.ReadInput(ctx, c.Inputs[0])
		if err != nil {
			return rsiplugin.Result{}, err
		}
		var facts contract.Facts
		if err = rsiplugin.DecodeInput(raw, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		if len(facts.Messages) > 8192 || facts.Recent < 2 || facts.Recent > 128 || facts.SummaryBytes < 256 || facts.SummaryBytes > 16384 || facts.ImageLimit < 1 || facts.ImageLimit > 32 {
			return rsiplugin.Result{}, errors.New("invalid context costs")
		}
		return rsiplugin.JSONResult(ctx, Select(facts))
	}}}
}

// Select first chooses newest distinct image occurrences, then the smallest
// complete history prefix that can be summarized with reserved summary space.
func Select(f contract.Facts) contract.Plan {
	out := contract.Plan{Images: make([][]int, len(f.Messages)), Through: 0}
	costs := make([]contract.Cost, len(f.Messages))
	seen := map[string]bool{}
	for i := len(f.Messages) - 1; i >= 0; i-- {
		m := f.Messages[i]
		out.Images[i] = []int{}
		for j, img := range m.Images {
			if !seen[img.Key] && len(seen) < f.ImageLimit {
				seen[img.Key] = true
				out.Images[i] = append(out.Images[i], j)
			}
		}
		costs[i] = m.Full
		if len(out.Images[i]) != len(m.Images) {
			costs[i] = m.Reduced
			if len(out.Images[i]) > 0 {
				costs[i].Frame += len(`,"image_refs":[]`) + len(out.Images[i]) - 1
				for _, j := range out.Images[i] {
					costs[i].Frame += m.Images[j].Bytes
				}
			}
		}
	}
	selectWithBudget := func(model int) (int, bool) {
		total := contract.Cost{Model: 2, Frame: 2}
		for i, c := range costs {
			total.Model += c.Model
			total.Frame += c.Frame
			if i > 0 {
				total.Model++
				total.Frame++
			}
		}
		if total.Model <= model && total.Frame <= f.FrameBytes {
			return 0, true
		}
		latest := -1
		for i := len(f.Messages) - 1; i >= 0; i-- {
			if f.Messages[i].Input {
				latest = i
				break
			}
		}
		cut := max(0, len(f.Messages)-f.Recent)
		for cut > 0 && cut < len(f.Messages) && f.Messages[cut].Role == "tool" {
			cut--
		}
		for {
			sources := []int{}
			kept := contract.Cost{Model: 2, Frame: 2}
			count := 0
			for i, m := range f.Messages {
				if i < cut && m.Role != "system" && !m.Protected && !m.Recall && i != latest {
					sources = append(sources, i)
					continue
				}
				kept.Model += costs[i].Model
				kept.Frame += costs[i].Frame
				if count > 0 {
					kept.Model++
					kept.Frame++
				}
				count++
			}
			reserve := min(f.SummaryBytes+512, max(1024, model/4))
			if len(sources) > 0 && kept.Model+reserve <= model && kept.Frame+reserve+200 <= f.FrameBytes {
				return cut, true
			}
			if cut >= len(f.Messages) {
				return 0, false
			}
			cut++
			for cut < len(f.Messages) && f.Messages[cut].Role == "tool" {
				cut++
			}
		}
	}
	sources, ok := selectWithBudget(f.ModelBytes)
	if !ok && f.FullWindowFallback && !f.Aggressive && f.HardModelBytes > f.ModelBytes {
		sources, ok = selectWithBudget(f.HardModelBytes)
	}
	if !ok {
		out.ErrorCode = "CONTEXT_LENGTH_EXCEEDED"
		return out
	}
	out.Through = sources
	if sources > 0 {
		out.Instructions = instructions(f.SummaryBytes)
	}
	return out
}
