package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

type workingPreparation struct {
	Messages   []llm.Message     `json:"messages,omitempty"`
	Compaction *agent.Compaction `json:"compaction,omitempty"`
}

func (r *Runs) workingMessages(run agent.Run, userImages, plainIDs bool) ([]llm.Message, map[string]int, error) {
	all, e := agent.ProviderMessages(run.Checkpoint.Messages, userImages, plainIDs)
	all = memoryReadProjection(all)
	if e != nil {
		return nil, nil, e
	}
	positions := map[string]int{}
	out := []llm.Message{}
	state := run.Checkpoint.WorkingState
	latestInput := -1
	for i := len(all) - 1; i >= 0; i-- {
		if contextbuilder.IsInput(all[i]) {
			latestInput = i
			break
		}
	}
	if state != nil && (state.Through < 0 || state.Through > len(all)) {
		return nil, nil, agent.ErrStorage
	}
	for i, m := range all {
		if userImages && !run.Snapshot.Capabilities.ImageInput && m.Role == "user" && len(m.ImageRefs) > 0 {
			m.Text += "\nImage pixels are unavailable: the selected model has no verified image input. Use the references only; do not claim to have viewed the images."
		}
		ref := "trace:" + digest(fmt.Sprintf("%s:%d", run.ID, i))
		if id := run.Checkpoint.Messages[i].MessageID; id != "" && run.Kind == "main" && run.Checkpoint.Messages[i].Type != "custom" {
			ref = "conversation:" + id
		}
		positions[ref] = i
		if state != nil && i < state.Through && i != latestInput && m.Role != "system" && (m.Source == nil || !m.Source.Protected) {
			continue
		}
		if state != nil && i == state.Through {
			out = append(out, workingStateMessage(state, plainIDs))
		}
		if m.Source == nil {
			kind := m.Role
			if run.Checkpoint.Messages[i].Type == "custom" {
				kind = "task_update"
			}
			m.Source = &llm.ContextSource{Kind: kind, Ref: ref}
		} else {
			source := *m.Source
			source.Ref = ref
			m.Source = &source
		}
		if i == latestInput {
			m.Source.Protected = true
		}
		out = append(out, m)
	}
	if state != nil && state.Through == len(all) {
		out = append(out, workingStateMessage(state, plainIDs))
	}
	return out, positions, nil
}

func workingStateMessage(s *agent.WorkingState, plainIDs bool) llm.Message {
	m := agent.RuntimeContext("working_state", s.ArchiveRef, false, struct {
		Summary string `json:"summary"`
		History string `json:"history_artifact"`
	}{s.Text, s.ArchiveRef})
	out, _ := agent.ProviderMessages([]agent.AgentMessage{m}, false, plainIDs)
	return out[0]
}

// prepareWorkingContext freezes both semantic-summary requests and ordinary
// working sets before model admission. They use the same fenced generation,
// spend, auth, cancellation and replay pipeline.
func (r *Runs) prepareWorkingContext(ctx context.Context, c agent.RunClaim, run agent.Run) (workingPreparation, error) {
	p := contextbuilder.Policy{}
	var policy string
	if e := r.host.db.QueryRowContext(ctx, `SELECT policy_json FROM run_context_policy WHERE run_id=?`, run.ID).Scan(&policy); e != nil {
		return workingPreparation{}, agent.ErrStorage
	}
	if json.Unmarshal([]byte(policy), &p) != nil || !p.Valid() {
		return workingPreparation{}, llm.ModelIncompatible
	}
	in, positions, e := r.workingMessages(run, p.UserImages, p.InputRecall)
	if e != nil {
		return workingPreparation{}, e
	}
	in, e = r.codeMemoryProjection(ctx, run, in)
	if e != nil {
		return workingPreparation{}, e
	}
	if p.LayeredMemory {
		in, e = r.injectMemory(ctx, c, run, in, p.MemoryNavigation, p.MetadataNavigation, p.SeparateEvidence)
		if e != nil {
			return workingPreparation{}, e
		}
	}
	tools := contextbuilder.Size(agent.ProviderTools(run.Tools, run.Checkpoint.LoadedTools))
	budget := contextbuilder.BudgetFor(run.Snapshot.ContextWindow, run.Snapshot.MaxOutputTokens, tools, p, run.Checkpoint.ContextRetries > 0)
	required := []llm.Message{}
	for _, m := range in {
		if m.Role == "system" || m.Source != nil && m.Source.Protected {
			required = append(required, m)
		}
	}
	remaining := min(budget.ModelBytes-contextbuilder.ModelSize(required), budget.FrameBytes-contextbuilder.Size(required))
	recallBudget := max(0, min(16<<10, remaining/4))
	in, e = r.recallMessages(ctx, c, run, in, recallBudget, p.ContextualRecall, p.InputRecall)
	if e != nil {
		return workingPreparation{}, e
	}
	if len(in) > 0 && in[len(in)-1].Source == nil && in[len(in)-1].Origin == "runtime" {
		in[len(in)-1].Source = &llm.ContextSource{Kind: "recall", Ref: "recall:" + digest(fmt.Sprintf("%s:%d", run.ID, run.Checkpoint.Step))}
	}
	input, _ := encoded(in)
	var oldInput, oldPolicy, raw, provenance string
	e = r.host.db.QueryRowContext(ctx, `SELECT input_hash,policy_hash,messages_json,provenance_json FROM context_compaction WHERE run_id=? AND step_index=?`, run.ID, run.Checkpoint.Step).Scan(&oldInput, &oldPolicy, &raw, &provenance)
	if e == nil {
		if oldInput != digest(input) || oldPolicy != digest(policy) {
			return workingPreparation{}, llm.ModelIncompatible
		}
		var prepared workingPreparation
		if json.Unmarshal([]byte(raw), &prepared.Messages) != nil || json.Unmarshal([]byte(provenance), &prepared) != nil {
			return workingPreparation{}, agent.ErrStorage
		}
		return prepared, nil
	}
	if e != sql.ErrNoRows {
		return workingPreparation{}, agent.ErrStorage
	}
	selection, e := contextbuilder.Prepare(in, budget, p, run.Checkpoint.ContextRetries > 0)
	if e != nil {
		return workingPreparation{}, e
	}
	prepared := workingPreparation{Messages: selection.Messages}
	if len(selection.Sources) > 0 {
		anchors := []llm.Message{}
		for _, m := range in {
			if m.Source != nil && m.Source.Protected {
				anchors = append(anchors, m)
			}
		}
		// No Tools are declared to the summarizer; the snapshot and provider stay
		// unchanged, including monetary admission and usage accounting.
		summaryLimits := contextbuilder.BudgetFor(run.Snapshot.ContextWindow, run.Snapshot.MaxOutputTokens, 0, p, run.Checkpoint.ContextRetries > 0)
		summaryBudget := min(summaryLimits.ModelBytes, summaryLimits.FrameBytes) - 1024
		sources := memorySummarySources(selection.Sources)
		if p.InputRecall {
			sources, anchors = summaryObjectIDs(sources), summaryObjectIDs(anchors)
		}
		messages, count, err := contextbuilder.SummaryRequest(sources, anchors, summaryBudget, p.SummaryBytes)
		if err != nil {
			return workingPreparation{}, err
		}
		plan := &agent.Compaction{MaxBytes: min(64<<10, max(4096, p.SummaryBytes*4))}
		if run.Checkpoint.WorkingState != nil {
			plan.Through = run.Checkpoint.WorkingState.Through
		}
		for _, m := range selection.Sources[:count] {
			if m.Source == nil {
				return workingPreparation{}, agent.ErrStorage
			}
			plan.SourceRefs = append(plan.SourceRefs, m.Source.Ref)
			if i, ok := positions[m.Source.Ref]; ok {
				plan.Through = max(plan.Through, i+1)
			}
		}
		if run.Checkpoint.WorkingState != nil && plan.Through <= run.Checkpoint.WorkingState.Through {
			if count != 1 || selection.Sources[0].Source.Kind != "working_state" || len(run.Checkpoint.WorkingState.Text) <= p.SummaryBytes+512 {
				return workingPreparation{}, llm.ContextLengthExceeded
			}
			// A model may exceed the requested summary target. Permit a strictly
			// smaller replacement without discarding additional source messages.
			plan.MaxBytes = min(plan.MaxBytes, p.SummaryBytes+512, len(run.Checkpoint.WorkingState.Text)-1)
		}
		originals := map[string]llm.Message{}
		for _, m := range in {
			if m.Source != nil {
				originals[m.Source.Ref] = m
			}
		}
		archived := []llm.Message{}
		for _, m := range selection.Sources[:count] {
			archived = append(archived, originals[m.Source.Ref])
		}
		archive, _ := encoded(archived)
		staged, err := r.host.Artifacts().Stage(ctx, artifact.Input{Name: "context-history.json", MediaType: "application/json", RunOnly: true}, strings.NewReader(archive))
		if err != nil {
			return workingPreparation{}, err
		}
		defer staged.Close()
		err = r.write(ctx, func(tx *sql.Tx) error {
			if err := fence(ctx, tx, c); err != nil {
				return err
			}
			scope, err := runScope(ctx, tx, run.ID)
			if err != nil {
				return err
			}
			plan.ArchiveRef, err = r.host.Artifacts().CommitTx(ctx, tx, scope, staged, "context:"+run.ID+":"+digest(archive))
			return err
		})
		if err != nil {
			return workingPreparation{}, err
		}
		id := plan.ArchiveRef
		if p.InputRecall {
			id = tool.ObjectID(id)
		}
		messages[1].Text += "\nHistory Artifact: " + id
		prepared = workingPreparation{Messages: messages, Compaction: plan}
	}
	raw, _ = encoded(prepared.Messages)
	stage := "working_set"
	if prepared.Compaction != nil {
		stage = "summary_request"
	}
	inputRef := ""
	var recallStep *int64
	if p.ContextualRecall {
		inputRef = recallInputRef(in)
		var step int64
		if err := r.host.db.QueryRowContext(ctx, `SELECT step_index FROM run_recall WHERE run_id=? AND step_index<=? ORDER BY step_index DESC LIMIT 1`, run.ID, run.Checkpoint.Step).Scan(&step); err == nil {
			recallStep = &step
		} else if err != sql.ErrNoRows {
			return workingPreparation{}, agent.ErrStorage
		}
	}
	provenance, _ = encoded(struct {
		Compaction     *agent.Compaction `json:"compaction,omitempty"`
		Stage          string            `json:"stage"`
		OmittedImages  []string          `json:"omitted_images,omitempty"`
		RecallInputRef string            `json:"recall_input_ref,omitempty"`
		RecallStep     *int64            `json:"recall_step,omitempty"`
	}{prepared.Compaction, stage, selection.OmittedImages, inputRef, recallStep})
	if len(selection.Sources) > 0 || len(selection.OmittedImages) > 0 {
		saver, ok := r.history.(interface {
			SaveCompaction(context.Context, string, string, string, string) error
		})
		if !ok {
			return workingPreparation{}, llm.ModelIncompatible
		}
		if e = saver.SaveCompaction(ctx, fmt.Sprintf("compaction:%s:%d", run.ID, run.Checkpoint.Step), run.ConversationID, raw, provenance); e != nil {
			return workingPreparation{}, e
		}
	}
	e = r.write(ctx, func(tx *sql.Tx) error {
		if e := fence(ctx, tx, c); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO context_compaction VALUES(?,?,?,?,?,?,?)`, run.ID, run.Checkpoint.Step, digest(policy), digest(input), raw, provenance, time.Now().UnixMicro())
		return e
	})
	if e == nil {
		r.host.boundary("after_compaction_commit")
	}
	return prepared, e
}

func summaryObjectIDs(in []llm.Message) []llm.Message {
	out := append([]llm.Message(nil), in...)
	for i := range out {
		if out[i].Source != nil {
			source := *out[i].Source
			source.Ref = tool.ObjectID(source.Ref)
			out[i].Source = &source
		}
		out[i].ImageRefs = tool.ObjectIDs(out[i].ImageRefs)
	}
	return out
}

// Exact memory reads remain encrypted in the archive. Compaction carries a
// stable reopening hint instead of copying sealed payloads into summary prose.
func memorySummarySources(in []llm.Message) []llm.Message {
	out := append([]llm.Message(nil), in...)
	for i, m := range out {
		if m.Memory != nil {
			out[i].Memory = nil
			if m.Memory.CodeMode {
				text, changed, err := llm.SummaryCodeMemory(m.Memory.Sealed)
				if err == nil && changed {
					out[i].Text += text
					continue
				}
				// Preserve an unrecognized mixed frame rather than silently losing
				// its business outcomes. The ordinary summary budget still applies.
				out[i].Text += m.Memory.Sealed
				continue
			}
			if m.Memory.Ref != "" {
				out[i].Text += "\nPrivate memory content omitted; use memory_manage read with memory_ref=" + m.Memory.Ref + " to reopen the document."
			} else {
				out[i].Text += "\nPrivate memory listing/injected content omitted; use memory_manage list to find current documents."
			}
		}
	}
	return out
}
