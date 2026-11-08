package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (r *Runs) ConfigureSemanticMemory(provider llm.LLMProvider) { r.memoryProvider = provider }

type semanticWriter interface {
	SemanticContext(context.Context, recall.Scope, int64) ([]memory.Fact, error)
	MergeSemantic(context.Context, recall.Scope, memory.CurationInput, memory.Proposal) error
	RetireAutomatic(context.Context, []string) error
	AgeSemantic(context.Context, time.Time) error
}
type curationWork struct {
	Input   memory.CurationInput
	Request llm.GenerationRequest
}
type memoryFailure struct {
	Reason string `json:"validation_error"`
}

func curationModelText(in memory.CurationInput) string {
	if in.Mode == "dream" {
		projected := in
		projected.Observation.EventID = ""
		projected.Observation.Source = memory.SourceRef{}
		projected.Observations = append([]memory.Observation(nil), in.Observations...)
		for i := range projected.Observations {
			projected.Observations[i].EventID = ""
			projected.Observations[i].Source = memory.SourceRef{}
		}
		projected.Documents = append([]memory.Document(nil), in.Documents...)
		for i := range projected.Documents {
			projected.Documents[i].Ref = tool.ObjectID(projected.Documents[i].Ref)
			projected.Documents[i].Sources = nil
			projected.Documents[i].Evidence = append([]recall.Evidence(nil), projected.Documents[i].Evidence...)
			for j := range projected.Documents[i].Evidence {
				projected.Documents[i].Evidence[j].SourceRef = ""
			}
			projected.Documents[i].Links = tool.ObjectIDs(projected.Documents[i].Links)
		}
		raw, _ := json.Marshal(projected)
		return string(raw)
	}

	// Private event/source keys authorize Host maintenance, not model reasoning.
	input := struct {
		Observation struct {
			Author string    `json:"author"`
			Text   string    `json:"text"`
			At     time.Time `json:"occurred_at"`
		} `json:"observation"`
		Existing []memory.Fact `json:"existing"`
	}{Existing: append([]memory.Fact{}, in.Existing...)}
	input.Observation.Author, input.Observation.Text, input.Observation.At = in.Observation.Author, in.Observation.Text, in.Observation.At
	for i := range input.Existing {
		input.Existing[i].Ref = tool.ObjectID(input.Existing[i].Ref)
	}
	raw, _ := json.Marshal(input)
	return string(raw)
}

func (r *Runs) memoryGuard(ctx context.Context, req llm.GenerationRequest, token string) (func(), error) {
	tx, e := r.host.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, recall.ErrUnavailable
	}
	var n int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_generation g JOIN memory_mutation m USING(event_id) WHERE g.generation_id=? AND g.owner_token=? AND g.status='pending' AND g.lease_until>? AND g.privacy_epoch=coalesce((SELECT epoch FROM memory_privacy p WHERE p.agent_id=g.agent_id),0) AND m.accepted_at>coalesce((SELECT updated_at FROM memory_privacy p WHERE p.agent_id=g.agent_id),0)`, req.GenerationID, token, time.Now().UnixMicro()).Scan(&n)
	if e != nil || n != 1 {
		tx.Rollback()
		return nil, recall.ErrConflict
	}
	return func() { _ = tx.Rollback() }, nil
}

// prepareCuration freezes the source, candidate context, model and private
// encrypted request once. Accepted output is reused across Memory DB retries.
func (r *Runs) prepareCuration(ctx context.Context, scope recall.Scope, in memory.CurationInput) (curationWork, string, *memory.Proposal, error) {
	var work curationWork
	var token string
	var cached *memory.Proposal
	e := r.write(ctx, func(tx *sql.Tx) error {
		var priorID, sealed, result, status string
		var lease int64
		err := tx.QueryRowContext(ctx, `SELECT generation_id,request_sealed,coalesce(result_sealed,''),status,lease_until FROM memory_generation WHERE event_id=? AND purpose=? AND pass_index=? ORDER BY created_at DESC LIMIT 1`, in.Observation.EventID, curationPurpose(in), in.Pass).Scan(&priorID, &sealed, &result, &status, &lease)
		if err == nil {
			plain, e := r.vault.Open(ctx, sealed, priorID)
			if e != nil || json.Unmarshal([]byte(plain), &work) != nil {
				return recall.ErrUnavailable
			}
			if status == "completed" {
				raw, e := r.vault.Open(ctx, result, priorID+":result")
				if e != nil {
					return e
				}
				p, e := memory.ParseProposal(raw, work.Input)
				if e != nil {
					return e
				}
				cached = &p
				return nil
			}
			if status == "pending" && lease > time.Now().UnixMicro() {
				return agent.ErrBusy
			}
			if status == "failed" {
				work.Input = in
				work.Request.Messages = []llm.Message{{Role: "system", Text: curationInstruction(in)}, {Role: "user", Text: curationModelText(in)}}
				if result != "" {
					plain, err := r.vault.Open(ctx, result, priorID+":result")
					var failure memoryFailure
					if err != nil || json.Unmarshal([]byte(plain), &failure) != nil {
						return recall.ErrUnavailable
					}
					if failure.Reason != "" {
						work.Request.Messages[0].Text += "\nThe previous attempt was rejected: " + failure.Reason + " Correct this in the new proposal; preserve all evidence and schema requirements."
					}
				}
			}
		} else if err != sql.ErrNoRows {
			return agent.ErrStorage
		} else {
			var profile string
			var version int64
			if tx.QueryRowContext(ctx, `SELECT p.profile_id,p.version FROM agent_config a JOIN model_profile p ON p.profile_id=a.active_model_profile_id WHERE a.agent_id=? AND a.setup_status='ready' AND p.status='ready'`, scope.AgentID).Scan(&profile, &version) != nil {
				return agent.ErrSetup
			}
			snapshot, e := r.host.Models().FreezeProfile(ctx, tx, profile, version)
			if e != nil {
				return e
			}
			if e = r.freezeSpend(ctx, tx, snapshot); e != nil {
				return e
			}
			work = curationWork{Input: in, Request: llm.GenerationRequest{RunID: "memory:" + in.Observation.EventID, SessionID: "memory:" + in.Observation.EventID, Snapshot: snapshot, OutputLimitBytes: 32 << 10, Messages: []llm.Message{{Role: "system", Text: curationInstruction(in)}, {Role: "user", Text: curationModelText(in)}}}}
		}
		// Private maintenance has the same model-window admission boundary as
		// user work. Refuse before journaling or contacting the provider.
		if int64(contextbuilder.ModelSize(work.Request.Messages)) > work.Request.Snapshot.ContextWindow*3 {
			return llm.ContextLengthExceeded
		}
		var privacy int64
		if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0)`, scope.AgentID).Scan(&privacy) != nil {
			return agent.ErrStorage
		}
		work.Request.GenerationID = "memory:" + newID()
		// Dispose is terminal in the real Bridge. Each fresh attempt/pass owns
		// its own transport session; the event remains the durable receipt key.
		work.Request.RunID = work.Request.GenerationID
		work.Request.SessionID = work.Request.RunID
		token = newID()
		raw, _ := json.Marshal(work)
		encrypted, e := r.vault.Seal(ctx, string(raw), work.Request.GenerationID)
		if e != nil {
			return e
		}
		hash, e := r.vault.Digest(ctx, string(raw))
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO memory_generation(generation_id,event_id,agent_id,privacy_epoch,model_snapshot_id,input_hash,request_sealed,result_sealed,status,owner_token,lease_until,created_at,purpose,pass_index) VALUES(?,?,?,?,?,?,?,NULL,'pending',?,?,?,?,?)`, work.Request.GenerationID, in.Observation.EventID, scope.AgentID, privacy, work.Request.Snapshot.SnapshotID, hash, encrypted, token, time.Now().Add(2*curationTimeout(in)).UnixMicro(), time.Now().UnixMicro(), curationPurpose(in), in.Pass); e != nil {
			return e
		}
		return r.reserveMemoryCost(ctx, tx, work.Request)
	})
	return work, token, cached, e
}

func (r *Runs) deriveMemory(ctx context.Context, scope recall.Scope, in memory.CurationInput) (memory.CurationInput, memory.Proposal, error) {
	if in.Mode != "dream" && !memory.EligibleObservation(in.Observation) {
		return in, memory.Proposal{Facts: []memory.Candidate{}}, nil
	}
	if r.memoryProvider == nil {
		return in, memory.Proposal{}, recall.ErrUnavailable
	}
	work, token, cached, e := r.prepareCuration(ctx, scope, in)
	if e != nil {
		return in, memory.Proposal{}, e
	}
	if cached != nil {
		return work.Input, *cached, nil
	}
	req := work.Request
	req.DispatchGuard = func(c context.Context) (func(), error) { return r.memoryGuard(c, req, token) }
	call, cancel := context.WithTimeout(ctx, curationTimeout(work.Input))
	defer cancel()
	result, e := r.memoryProvider.Generate(call, req, func(llm.GenerationEvent) {})
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	_ = r.memoryProvider.Dispose(cleanup, req.SessionID)
	if e != nil {
		return work.Input, memory.Proposal{}, e
	}
	if result.Clipped || len(result.ToolCalls) > 0 {
		return work.Input, memory.Proposal{}, memory.ErrProposal
	}
	p, e := memory.ParseProposal(result.Text, work.Input)
	return work.Input, p, e
}

// AcceptMemoryGeneration is invoked inside the existing usage/auth/cancellation
// acceptance transaction. It seals private output rather than storing it in a
// normal Run trace. No provider completion bypasses the privacy epoch fence.
func (r *Runs) AcceptMemoryGeneration(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest, result llm.GenerationResult, cause error) error {
	var sealed, status string
	var epoch, current int64
	if tx.QueryRowContext(ctx, `SELECT request_sealed,status,privacy_epoch,coalesce((SELECT epoch FROM memory_privacy p WHERE p.agent_id=g.agent_id),0) FROM memory_generation g WHERE generation_id=?`, req.GenerationID).Scan(&sealed, &status, &epoch, &current) != nil {
		return agent.ErrStorage
	}
	if status != "pending" || epoch != current {
		return recall.ErrConflict
	}
	var owned int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM memory_generation g WHERE generation_id=? AND lease_until>? AND NOT EXISTS(SELECT 1 FROM memory_generation n WHERE n.event_id=g.event_id AND n.purpose=g.purpose AND n.pass_index=g.pass_index AND n.created_at>g.created_at)`, req.GenerationID, time.Now().UnixMicro()).Scan(&owned) != nil || owned != 1 {
		return recall.ErrConflict
	}
	plain, e := r.vault.Open(ctx, sealed, req.GenerationID)
	var work curationWork
	if e != nil || json.Unmarshal([]byte(plain), &work) != nil {
		return recall.ErrUnavailable
	}
	if cause == nil {
		if result.Clipped || len(result.ToolCalls) > 0 {
			cause = memory.ErrProposal
		} else {
			_, cause = memory.ParseProposal(result.Text, work.Input)
		}
	}
	var output any
	next := "failed"
	if cause == nil {
		next = "completed"
		output, e = r.vault.Seal(ctx, result.Text, req.GenerationID+":result")
		if e != nil {
			return e
		}
	} else if errors.Is(cause, memory.ErrProposal) {
		reason := memory.ProposalReason(cause)
		if result.Clipped {
			reason = "Provider output was clipped before the proposal completed."
		} else if len(result.ToolCalls) > 0 {
			reason = "Proposal returned tool calls instead of the required JSON."
		}
		raw, _ := json.Marshal(memoryFailure{Reason: reason})
		output, e = r.vault.Seal(ctx, string(raw), req.GenerationID+":result")
		if e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE memory_generation SET status=?,result_sealed=?,lease_until=0 WHERE generation_id=?`, next, output, req.GenerationID); e != nil {
		return e
	}
	return r.settleMemoryCost(ctx, tx, req, result)
}

func (r *Runs) reserveMemoryCost(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest) error {
	var account, currency, raw string
	var boundary int64
	if tx.QueryRowContext(ctx, `SELECT account_key,currency,price_json,boundary_micros FROM spend_snapshot WHERE snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&account, &currency, &raw, &boundary) != nil {
		return agent.ErrStorage
	}
	if raw == "null" {
		return nil
	}
	var price agent.ModelPrice
	if json.Unmarshal([]byte(raw), &price) != nil {
		return agent.ErrStorage
	}
	projected, e := projectedCost(price, req.Snapshot)
	if e != nil {
		return e
	}
	var total int64
	if tx.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN status='priced' THEN actual_micros ELSE reserved_micros END),0) FROM (SELECT account_key,currency,status,actual_micros,reserved_micros FROM spend_reservation UNION ALL SELECT account_key,currency,status,actual_micros,reserved_micros FROM memory_cost_reservation) WHERE account_key=? AND currency=?`, account, currency).Scan(&total) != nil {
		return agent.ErrStorage
	}
	// Background work defers at the same account boundary; it never opens a
	// user Task or manufactures a spend approval just for memory extraction.
	if boundary > 0 && (total >= boundary || projected >= boundary-total) {
		return agent.ErrSpendDenied
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO memory_cost_reservation VALUES(?,?,?,?,NULL,'reserved')`, req.GenerationID, account, currency, projected)
	return e
}
func (r *Runs) settleMemoryCost(ctx context.Context, tx *sql.Tx, req llm.GenerationRequest, result llm.GenerationResult) error {
	var raw string
	if tx.QueryRowContext(ctx, `SELECT price_json FROM spend_snapshot WHERE snapshot_id=?`, req.Snapshot.SnapshotID).Scan(&raw) != nil {
		return agent.ErrStorage
	}
	if raw == "null" {
		return nil
	}
	var price agent.ModelPrice
	if json.Unmarshal([]byte(raw), &price) != nil {
		return agent.ErrStorage
	}
	if result.Usage == nil {
		_, e := tx.ExecContext(ctx, `UPDATE memory_cost_reservation SET status='usage_missing' WHERE generation_id=?`, req.GenerationID)
		return e
	}
	amount, e := monetaryCost(price, *result.Usage)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE memory_cost_reservation SET status='priced',actual_micros=? WHERE generation_id=?`, amount, req.GenerationID)
	return e
}

func (r *Runs) prepareLegacyMemory(ctx context.Context) error {
	if r.memory == nil || r.memoryPrepared {
		return nil
	}
	writer, ok := r.memory.(semanticWriter)
	if !ok {
		r.memoryPrepared = true
		return nil
	}
	rows, e := r.host.db.QueryContext(ctx, `SELECT event_id FROM memory_mutation WHERE operation='observe' AND applied_at IS NOT NULL`)
	if e != nil {
		return agent.ErrStorage
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return agent.ErrStorage
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if e = writer.RetireAutomatic(ctx, ids); e != nil {
		return e
	}
	r.memoryPrepared = true
	return nil
}
func (r *Runs) curateObservation(ctx context.Context, scope recall.Scope, o memory.Observation, floor int64) error {
	writer, ok := r.memory.(semanticWriter)
	if !ok {
		return recall.ErrUnavailable
	}
	// Worker reports are not authority for personal facts. If a Task observation
	// has an original user request, revisit that exact source; the same source
	// never counts as independent support just because a Worker repeated it.
	if o.Author == "worker" {
		var raw string
		e := r.host.db.QueryRowContext(ctx, `SELECT c.message_json FROM task_request q JOIN conversation_intent c ON c.message_id=q.source_message_id WHERE json_extract(q.receipt_json,'$.taskRef')=? ORDER BY c.sequence DESC LIMIT 1`, o.Source.Ref).Scan(&raw)
		if e == nil {
			var msg struct {
				ID        string                        `json:"message_id"`
				CreatedAt time.Time                     `json:"created_at"`
				Role      string                        `json:"role"`
				Content   []struct{ Type, Text string } `json:"content"`
			}
			if json.Unmarshal([]byte(raw), &msg) != nil {
				return agent.ErrStorage
			}
			o.Author = msg.Role
			o.At = msg.CreatedAt
			o.Text = ""
			o.Source = memory.SourceRef{Type: "conversation", Ref: "conversation:" + msg.ID}
			var processed int
			if r.host.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_mutation m JOIN memory_semantic_receipt s USING(event_id) WHERE m.operation='observe' AND m.source_type='conversation' AND m.source_ref=?`, msg.ID).Scan(&processed) != nil {
				return agent.ErrStorage
			}
			if processed > 0 {
				o.Author = "duplicate"
			}
			for _, p := range msg.Content {
				if p.Type == "text" {
					o.Text += p.Text + "\n"
				}
			}
		} else if e != sql.ErrNoRows {
			return agent.ErrStorage
		}
	}
	in := memory.CurationInput{Observation: o, Existing: []memory.Fact{}}
	if memory.EligibleObservation(o) {
		var facts []memory.Fact
		var e error
		if candidates, ok := r.memory.(interface {
			SemanticCandidates(context.Context, recall.Scope, int64, string) ([]memory.Fact, error)
		}); ok {
			facts, e = candidates.SemanticCandidates(ctx, scope, floor, o.Text)
		} else {
			facts, e = writer.SemanticContext(ctx, scope, floor)
		}
		if e != nil {
			return e
		}
		in.Existing = facts
	}
	frozen, p, e := r.deriveMemory(ctx, scope, in)
	if e == nil {
		e = r.write(ctx, func(tx *sql.Tx) error {
			var current int64
			if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, scope.AgentID).Scan(&current) != nil {
				return agent.ErrStorage
			}
			if current != floor {
				return recall.ErrConflict
			}
			return writer.MergeSemantic(ctx, scope, frozen, p)
		})
	}
	if errors.Is(e, recall.ErrConflict) {
		// An explicit remember/forget or another confirmed fact changed the target.
		// Invalidate only the private proposal, not the original observation.
		_, _ = r.host.db.ExecContext(ctx, `UPDATE memory_generation SET status='failed',result_sealed=NULL,lease_until=0 WHERE event_id=? AND status='completed'`, o.EventID)
		return recall.ErrUnavailable
	}
	return e
}

// Cross-session document synthesis is larger than single-observation extraction.
// Its acceptance lease must outlive the bounded call and cleanup window.
func curationTimeout(in memory.CurationInput) time.Duration {
	if in.Mode == "dream" {
		return 2 * time.Minute
	}
	return 45 * time.Second
}

func curationPurpose(in memory.CurationInput) string {
	if in.Mode == "dream" {
		return "dream"
	}
	return "facts"
}
func curationInstruction(in memory.CurationInput) string {
	if in.Mode == "dream" {
		if in.Version >= 3 {
			return memory.WorkerDreamInstruction
		}
		if in.Version >= 2 {
			return memory.GroundedDreamInstruction
		}
		return memory.DreamInstruction
	}
	return memory.CurationInstruction
}
