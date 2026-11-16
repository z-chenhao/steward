package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func contextFacts(in []llm.Message, b contextbuilder.Budget, p contextbuilder.Policy, aggressive bool) contract.Facts {
	f := contract.Facts{Messages: []contract.Message{}, ModelBytes: b.ModelBytes, HardModelBytes: b.HardModelBytes, FrameBytes: b.FrameBytes, Recent: p.Recent, SummaryBytes: p.SummaryBytes, ImageLimit: llm.MaxInputImages, FullWindowFallback: p.FullWindowFallback, Aggressive: aggressive}
	if aggressive {
		f.ImageLimit = min(f.ImageLimit, 4)
	}
	cost := func(m llm.Message) contract.Cost {
		return contract.Cost{Model: contextbuilder.ModelSize([]llm.Message{m}) - 2, Frame: contextbuilder.Size([]llm.Message{m}) - 2}
	}
	for _, m := range in {
		x := contract.Message{Role: m.Role, Input: contextbuilder.IsInput(m), Full: cost(m), Images: []contract.Image{}}
		if m.Source != nil {
			x.Protected = m.Source.Protected
			x.Recall = m.Source.Kind == "recall"
		}
		for _, ref := range m.ImageRefs {
			raw, _ := json.Marshal(ref)
			x.Images = append(x.Images, contract.Image{Key: rsiplugin.FileDigest([]byte(ref)), Bytes: len(raw)})
		}
		if len(m.ImageRefs) > 0 {
			m.Text += contract.OmittedImageNotice
			m.ImageRefs = nil
		}
		x.Reduced = cost(m)
		f.Messages = append(f.Messages, x)
	}
	return f
}

func (r *Runs) prepareContext(ctx context.Context, c agent.RunClaim, run agent.Run, in []llm.Message, b contextbuilder.Budget, p contextbuilder.Policy, aggressive bool) (contextbuilder.Preparation, error) {
	managed, err := r.managedRunService(ctx, run.ID, contract.Contract)
	if err != nil {
		return contextbuilder.Preparation{}, err
	}
	if !managed {
		return contextbuilder.Prepare(in, b, p, aggressive)
	}
	facts := contextFacts(in, b, p, aggressive)
	data, err := json.Marshal(facts)
	if err != nil {
		return contextbuilder.Preparation{}, err
	}
	raw, err := r.readContextSelection(ctx, c, run, data)
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return contextbuilder.Preparation{}, fmt.Errorf("%w: context policy unavailable", llm.ProviderUnavailable)
	}
	if err != nil {
		return contextbuilder.Preparation{}, err
	}
	var plan contract.Plan
	if err = rsiplugin.DecodeResult(raw, &plan); err != nil {
		return contextbuilder.Preparation{}, err
	}
	return applyContextPlan(in, b, p, aggressive, plan)
}

// New context selection is a pure, pre-admission read. context_compaction
// atomically accepts the reconstructed messages and provenance; until then a
// stopped computation may be evaluated again on the same frozen Run graph.
// Existing opaque calls remain their own historical boundary: completed output
// is reused with its original input hash, while unknown is never replayed.
func (r *Runs) readContextSelection(ctx context.Context, c agent.RunClaim, run agent.Run, data []byte) ([]byte, error) {
	if r.plugins == nil {
		return nil, rsi.ErrNotFound
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return nil, rsi.ErrNotFound
	}
	binding, err := h.ServiceInstance(ctx, revision, contract.Contract)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("context-select:%d", run.Checkpoint.Step)
	var retained bool
	if err = r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_call WHERE run_id=? AND instance_id=? AND operation_id=?)`, run.ID, binding.InstanceID, key).Scan(&retained); err != nil {
		return nil, err
	}
	if retained {
		return r.featureService(ctx, c, contract.Contract, key, data)
	}
	raw, err := h.ReadService(ctx, revision, contract.Contract, json.RawMessage(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: data}})
	if err != nil {
		return nil, err
	}
	r.host.boundary("after_context_policy_read")
	if err = h.Store.CheckGraph(ctx, revision); err != nil {
		return nil, err
	}
	return raw, nil
}

// applyContextPlan reconstructs exact authorized sources. A plugin cannot write
// a system message, promote evidence, omit protected input, split a tool group,
// invent an image, exceed budgets or edit a frozen request.
func applyContextPlan(in []llm.Message, b contextbuilder.Budget, p contextbuilder.Policy, aggressive bool, plan contract.Plan) (contextbuilder.Preparation, error) {
	bad := func() (contextbuilder.Preparation, error) { return contextbuilder.Preparation{}, rsi.ErrInvalid }
	if len(plan.Images) != len(in) || plan.Through < 0 || plan.Through > len(in) {
		return bad()
	}
	if plan.ErrorCode != "" {
		if plan.ErrorCode != "CONTEXT_LENGTH_EXCEEDED" {
			return bad()
		}
		return contextbuilder.Preparation{}, llm.ContextLengthExceeded
	}
	out := contextbuilder.Preparation{Messages: append([]llm.Message(nil), in...)}
	seen := map[string]bool{}
	limit := llm.MaxInputImages
	if aggressive {
		limit = min(limit, 4)
	}
	for i, m := range in {
		images := []string{}
		last := -1
		chosen := map[int]bool{}
		for _, j := range plan.Images[i] {
			if j <= last || j < 0 || j >= len(m.ImageRefs) {
				return bad()
			}
			last = j
			ref := m.ImageRefs[j]
			if seen[ref] || len(seen) >= limit {
				return bad()
			}
			seen[ref] = true
			chosen[j] = true
			images = append(images, ref)
		}
		if len(images) != len(m.ImageRefs) {
			m.Text += contract.OmittedImageNotice
			m.ImageRefs = images
			for j, ref := range in[i].ImageRefs {
				if !chosen[j] {
					out.OmittedImages = append(out.OmittedImages, ref)
				}
			}
		}
		out.Messages[i] = m
	}
	latest := -1
	for i := len(in) - 1; i >= 0; i-- {
		if contextbuilder.IsInput(in[i]) {
			latest = i
			break
		}
	}
	kept := []llm.Message{}
	for i, m := range out.Messages {
		eligible := m.Role != "system" && i != latest && (m.Source == nil || !m.Source.Protected && m.Source.Kind != "recall")
		if i < plan.Through && eligible {
			out.Sources = append(out.Sources, m)
		} else {
			kept = append(kept, m)
		}
	}
	if plan.Through > 0 && (len(out.Sources) == 0 || len(plan.Instructions) == 0 || len(plan.Instructions) > 8192 || !utf8.ValidString(plan.Instructions)) {
		return bad()
	}
	if plan.Through < len(in) && plan.Through > 0 && in[plan.Through].Role == "tool" {
		return bad()
	}
	out.SummaryInstructions = plan.Instructions
	fits := func(model int) bool {
		if len(out.Sources) == 0 {
			return contextbuilder.ModelSize(out.Messages) <= model && contextbuilder.Size(out.Messages) <= b.FrameBytes
		}
		reserve := min(p.SummaryBytes+512, max(1024, model/4))
		return contextbuilder.ModelSize(kept)+reserve <= model && contextbuilder.Size(kept)+reserve+200 <= b.FrameBytes
	}
	if !fits(b.ModelBytes) && !(p.FullWindowFallback && !aggressive && b.HardModelBytes > b.ModelBytes && fits(b.HardModelBytes)) {
		return bad()
	}
	return out, nil
}
