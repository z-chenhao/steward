package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
)

func (r *Runs) ConfigureContext(p contextbuilder.Policy) { r.contextPolicy = p }
func (r *Runs) freezeContext(ctx context.Context, tx *sql.Tx, id string) error {
	p := r.contextPolicy
	if !p.Valid() {
		p = contextbuilder.DefaultPolicy()
	}
	p.FullWindowFallback = true
	p.MemoryNavigation = true
	p.MetadataNavigation = true
	p.EvidenceReuse = true
	p.SeparateEvidence = true
	p.UserImages = true
	p.DOCXText = true
	p.ContextualRecall = true
	p.InputRecall = true
	p.LayeredMemory = r.memory != nil && r.vault.Ref != ""
	raw, _ := encoded(p)
	var parent sql.NullString
	if e := tx.QueryRowContext(ctx, `SELECT parent_run_id FROM run WHERE run_id=?`, id).Scan(&parent); e != nil {
		return e
	}
	if parent.Valid {
		e := tx.QueryRowContext(ctx, `SELECT policy_json FROM run_context_policy WHERE run_id=?`, parent.String).Scan(&raw)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO run_context_policy VALUES(?,?)`, id, raw)
	return e
}
func (r *Runs) compactMessages(ctx context.Context, c agent.RunClaim, run agent.Run, in []llm.Message) ([]llm.Message, error) {
	var legacy bool
	if e := r.host.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_context_generation l JOIN model_generation g USING(generation_id) WHERE g.run_id=? AND g.step_index=?)`, run.ID, run.Checkpoint.Step).Scan(&legacy); e != nil {
		return nil, agent.ErrStorage
	}
	if legacy {
		return in, nil
	}
	p := contextbuilder.Policy{}
	var policy string
	e := r.host.db.QueryRowContext(ctx, `SELECT policy_json FROM run_context_policy WHERE run_id=?`, run.ID).Scan(&policy)
	if e == nil {
		if json.Unmarshal([]byte(policy), &p) != nil {
			return nil, agent.ErrStorage
		}
	} else if e == sql.ErrNoRows {
		p = contextbuilder.DefaultPolicy()
	} else {
		return nil, agent.ErrStorage
	}
	if !p.Valid() {
		return nil, llm.ModelIncompatible
	}
	policy, _ = encoded(p)
	input, _ := encoded(in)
	var oldPolicy, oldInput, raw string
	e = r.host.db.QueryRowContext(ctx, `SELECT policy_hash,input_hash,messages_json FROM context_compaction WHERE run_id=? AND step_index=?`, run.ID, run.Checkpoint.Step).Scan(&oldPolicy, &oldInput, &raw)
	if e == nil {
		if oldPolicy != digest(policy) || oldInput != digest(input) {
			return nil, llm.ModelIncompatible
		}
		var out []llm.Message
		if json.Unmarshal([]byte(raw), &out) != nil {
			return nil, agent.ErrStorage
		}
		return out, nil
	}
	if e != sql.ErrNoRows {
		return nil, agent.ErrStorage
	}
	refs := []string{}
	for i, m := range run.Checkpoint.Messages {
		ref := m.MessageID
		if ref != "" && run.Kind == "main" && m.Type != "custom" {
			ref = "conversation:" + ref
		} else {
			ref = "trace:" + digest(fmt.Sprintf("%s:%d", run.ID, i))
		}
		refs = append(refs, ref)
	}
	if len(in) == len(refs)+1 {
		refs = append(refs, "recall:"+digest(fmt.Sprintf("%s:%d", run.ID, run.Checkpoint.Step)))
	}
	result, e := contextbuilder.Compact(in, refs, run.Snapshot.ContextWindow, contextbuilder.Size(agent.ProviderTools(run.Tools, run.Checkpoint.LoadedTools)), p, run.Checkpoint.ContextRetries > 0)
	if e != nil {
		return nil, e
	}
	raw, _ = encoded(result.Messages)
	provenance, _ := encoded(struct {
		Stage string   `json:"stage"`
		Refs  []string `json:"source_refs"`
	}{result.Stage, result.Omitted})
	if result.Stage != "none" {
		saver, ok := r.history.(interface {
			SaveCompaction(context.Context, string, string, string, string) error
		})
		if !ok {
			return nil, llm.ModelIncompatible
		}
		if e = saver.SaveCompaction(ctx, fmt.Sprintf("compaction:%s:%d", run.ID, run.Checkpoint.Step), run.ConversationID, raw, provenance); e != nil {
			return nil, e
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
	return result.Messages, e
}
