// Package traceinspector owns independently versioned diagnostic policies.
package traceinspector

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.1.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Workbench: workbench, contract.Activity: activity, contract.Contract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(call.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		if len(call.Inputs) != 1 || req.InputID != call.Inputs[0].ID {
			return rsiplugin.Result{}, errors.New("missing authorized diagnostic facts")
		}
		raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
		if err != nil {
			return rsiplugin.Result{}, err
		}
		var facts contract.Facts
		if err = rsiplugin.DecodeInput(raw, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, Project(facts))
	}, contract.DiffContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(call.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		if len(call.Inputs) != 1 || req.InputID != call.Inputs[0].ID {
			return rsiplugin.Result{}, errors.New("missing scoped context fingerprints")
		}
		raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
		if err != nil {
			return rsiplugin.Result{}, err
		}
		var facts contract.DiffFacts
		if err = rsiplugin.DecodeInput(raw, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		if facts.After < 0 || facts.Section != "messages" && facts.Section != "tools" {
			return rsiplugin.Result{}, errors.New("invalid comparison")
		}
		return rsiplugin.JSONResult(ctx, Diff(facts))
	}, contract.ToolContract: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var req contract.Request
		if err := rsiplugin.Decode(call.Input, &req); err != nil {
			return rsiplugin.Result{}, err
		}
		if len(call.Inputs) != 1 || call.Inputs[0].ID != req.InputID {
			return rsiplugin.Result{}, errors.New("missing authorized receipt")
		}
		raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
		if err != nil {
			return rsiplugin.Result{}, err
		}
		var facts contract.ToolFacts
		if err = rsiplugin.DecodeInput(raw, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, ToolPreview(facts))
	}}}
}

// Project preserves actual ancestry and timing; missing observations are not
// reconstructed as facts, and cycles cannot recurse indefinitely.
func Project(f contract.Facts) contract.Layout {
	end := f.Now
	if f.Ended != nil {
		end = *f.Ended
	}
	end = max(f.Started+1, end)
	span := end - f.Started
	out := contract.Layout{Timeline: &contract.Timeline{Start: f.Started, End: end}, Nodes: []contract.Node{}, Tabs: []string{"overview"}}
	present := map[string]bool{}
	children := map[string][]contract.Observation{}
	for _, o := range f.Items {
		present[o.ID] = true
		children[o.Parent] = append(children[o.Parent], o)
	}
	primary := primaryNodes(f.Items, f.SelectedID)
	primaryParents := map[string]bool{}
	for _, o := range f.Items {
		if primary[o.ID] {
			primaryParents[o.Parent] = true
		}
	}
	seen := map[string]bool{}
	var visit func(contract.Observation, int, []bool)
	visit = func(o contract.Observation, depth int, guides []bool) {
		if seen[o.ID] {
			return
		}
		seen[o.ID] = true
		x := max(0, min(990, float64(o.Started-f.Started)/float64(span)*1000))
		width := float64(4)
		if o.Ended != nil {
			width = max(4, min(1000-x, float64(*o.Ended-o.Started)/float64(span)*1000))
		}
		running := o.Ended == nil && f.Ended == nil && (o.Type == "agent" || o.Type == "generation" && o.Status == "pending" || o.Type == "tool" && (o.Status == "running" || o.Status == "queued"))
		if running {
			width = max(0, min(1000-x, float64(end-o.Started)/float64(span)*1000))
		}
		out.Nodes = append(out.Nodes, contract.Node{Primary: primary[o.ID], PrimaryHasChildren: len(children[o.ID]) > 0 && primaryParents[o.ID], Running: running, ID: o.ID, Depth: min(16, depth), ParentOutside: o.Parent != "" && !present[o.Parent], HasChildren: len(children[o.ID]) > 0, Guides: append([]bool{}, guides[:min(16, len(guides))]...), X: x, Width: width})
		for i, child := range children[o.ID] {
			visit(child, depth+1, append(append([]bool(nil), guides...), i < len(children[o.ID])-1))
		}
	}
	for _, o := range f.Items {
		if o.Parent == "" || !present[o.Parent] {
			visit(o, 0, nil)
		}
	}
	for _, o := range f.Items {
		visit(o, 0, nil)
	}
	if f.SelectedType == "generation" {
		out.Tabs = []string{"overview", "context", "wire", "diff"}
	} else if f.SelectedName == "Provider HTTP" {
		out.Tabs = []string{"overview", "wire"}
	}
	var started, firstText int64
	starts := map[string]int64{}
	for _, mark := range f.Timings {
		if mark.Phase == "started" {
			starts[mark.Attempt] = mark.At
			if started == 0 || mark.At < started {
				started = mark.At
			}
		}
	}
	for _, mark := range f.Timings {
		if at, ok := starts[mark.Attempt]; ok && (mark.Phase == "first_text" || mark.Phase == "first_tool_call") && mark.At >= at && (firstText == 0 || mark.At < firstText) {
			firstText = mark.At
		}
	}
	if started > 0 && firstText >= started {
		ms := (firstText - started) / 1000
		out.TTFTMS = &ms
	}
	if firstText > 0 {
		out.FirstOutputAt = &firstText
	}
	out.OutputRate = outputRate(f)
	return out
}

// Primary visibility is display policy. Source records and selection remain
// authoritative Host facts; all rows are returned, including hidden noise.
func primaryNodes(items []contract.Observation, selected string) map[string]bool {
	byID := map[string]contract.Observation{}
	keep := map[string]bool{}
	for _, o := range items {
		byID[o.ID] = o
	}
	for _, o := range items {
		internal := o.Name == "Prepare context" || o.Name == "Provider HTTP" || o.Name == "Execute tool" || o.Name == "Enter / resume Agent" || o.Name == "Agent finished"
		if internal && o.ID != selected && o.Status != "failed" && o.Status != "http_error" {
			continue
		}
		keep[o.ID] = true
		parent := o.Parent
		for n := 0; parent != "" && n < len(items); n++ {
			if keep[parent] {
				break
			}
			keep[parent] = true
			p, ok := byID[parent]
			if !ok {
				break
			}
			parent = p.Parent
		}
	}
	return keep
}

// Rate selects only a recorded terminal attempt and its first meaningful
// output. Missing evidence stays unknown; acceptance/DB latency is excluded.
// This is display interpretation, never a usage/timing ledger write.
func outputRate(f contract.Facts) *float64 {
	if f.Usage == nil || f.Usage.KnownUsage == 0 || f.SelectedStatus != "completed" {
		return nil
	}
	starts := map[string]int64{}
	first := map[string]int64{}
	terminal := map[string]int64{}
	for _, m := range f.Timings {
		if m.Phase == "started" {
			starts[m.Attempt] = m.At
		}
	}
	for _, m := range f.Timings {
		at, ok := starts[m.Attempt]
		if !ok || m.At < at {
			continue
		}
		if m.Phase == "first_text" || m.Phase == "first_tool_call" {
			if first[m.Attempt] == 0 || m.At < first[m.Attempt] {
				first[m.Attempt] = m.At
			}
		}
		if m.Phase == "stream_terminal" {
			terminal[m.Attempt] = m.At
		}
	}
	var last int64
	var rate *float64
	for attempt, end := range terminal {
		begin := first[attempt]
		if begin > 0 && end > begin && end > last {
			v := float64(f.Usage.OutputTokens) * 1e6 / float64(end-begin)
			rate = &v
			last = end
		}
	}
	return rate
}
