package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/recallpolicy/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ConfigureRecallSnapshotVault supplies only trusted key resolution. Plugins
// never receive its reference, key or SecretStore.
func (r *Runs) ConfigureRecallSnapshotVault(resolve func(context.Context, string) (memory.Vault, error)) {
	r.recallSnapshotVault = resolve
}
func (r *Runs) recallVault(ctx context.Context, ref string) (memory.Vault, error) {
	if r.recallSnapshotVault != nil {
		return r.recallSnapshotVault(ctx, ref)
	}
	v := r.vault
	if ref != "" {
		v.Ref = ref
	}
	if v.Ref == "" {
		return v, recall.ErrUnavailable
	}
	return v, nil
}

type recallCandidateRecord struct {
	Hit       recall.Hit `json:"hit"`
	Sources   []string   `json:"sources"`
	Relevance float64    `json:"relevance"`
	Score     float64    `json:"score"`
}
type recallReadSnapshot struct {
	Query      recall.Query            `json:"query"`
	Candidates []recallCandidateRecord `json:"candidates"`
}

func (r *Runs) recallPluginHost(ctx context.Context, c agent.RunClaim, invocation string) (rsi.Host, evidenceAccess, error) {
	var a evidenceAccess
	a.claim = c
	a.scope.UserID = r.host.OwnerID()
	a.scope.RunID = c.RunID()
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT agent_id,coalesce((SELECT epoch FROM memory_privacy p WHERE p.agent_id=r.agent_id),0) FROM run r WHERE run_id=?`, c.RunID()).Scan(&a.scope.AgentID, &a.epoch)
	})
	if err != nil {
		return rsi.Host{}, a, err
	}
	if r.plugins == nil {
		return rsi.Host{}, a, recall.ErrUnavailable
	}
	h := *r.plugins
	store := r.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		if invocation != "" {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND run_id=? AND status='running' AND tool_name=?`, invocation, c.RunID(), recall.ToolName).Scan(&n); err != nil {
				return err
			}
			if n != 1 {
				return tool.ErrApprovalDenied
			}
		}
		return nil
	}
	h.Store = store
	revision, err := store.Binding(ctx, c.RunID())
	if err != nil {
		return h, a, err
	}
	if revision == "" {
		return h, a, recall.ErrUnavailable
	}
	if err = store.CheckGraph(ctx, revision); err != nil {
		return h, a, err
	}
	if _, err = h.ServiceInstance(ctx, revision, contract.Contract); err != nil {
		return h, a, err
	}
	a.pluginGraph = revision
	return h, a, nil
}

func (r *Runs) searchRecall(ctx context.Context, c agent.RunClaim, operation, invocation string, scope recall.Scope, q recall.Query, budget int) ([]recall.Hit, error) {
	if !q.Valid() || budget < 0 {
		return nil, recall.ErrInvalid
	}
	managed, err := r.managedRunService(ctx, c.RunID(), contract.Contract)
	if err != nil {
		return nil, err
	}
	if !managed {
		return r.recall.Search(ctx, scope, q, budget)
	}
	h, a, err := r.recallPluginHost(ctx, c, invocation)
	if err != nil {
		return nil, err
	}
	if a.scope.AgentID != scope.AgentID || a.scope.UserID != scope.UserID {
		return nil, recall.ErrInvalid
	}
	identityQuery := q
	if invocation == "" && q.TimeRange != nil {
		tr := *q.TimeRange
		tr.End = nil
		identityQuery.TimeRange = &tr
	}
	requestHash, err := rsiplugin.Digest("steward.recall.read/1", struct {
		Query           recall.Query
		EvidenceFirst   bool
		Excerpt, Budget int
	}{identityQuery, q.EvidenceFirst, q.ExcerptBytes, budget})
	if err != nil {
		return nil, err
	}
	var savedHash, keyRef string
	var epoch int64
	var sealed bool
	var payload sql.NullString
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if e := r.evidenceFence(ctx, tx, a); e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, `SELECT request_hash,privacy_epoch,sealed,key_ref,payload FROM recall_candidate_snapshot WHERE run_id=? AND operation_id=?`, c.RunID(), operation).Scan(&savedHash, &epoch, &sealed, &keyRef, &payload)
	})
	var snapshot recallReadSnapshot
	binding := "recall-candidates:" + c.RunID() + ":" + operation
	if errors.Is(err, sql.ErrNoRows) {
		candidates, e := r.recall.Collect(ctx, scope, q)
		if e != nil {
			return nil, e
		}
		snapshot = recallReadSnapshot{Query: q, Candidates: []recallCandidateRecord{}}
		hasMemory := false
		for _, v := range candidates {
			if v.Hit.SourceType != "memory" && v.Hit.SourceType != "conversation" {
				return nil, recall.ErrInvalid
			}
			hasMemory = hasMemory || v.Hit.SourceType == "memory"
			snapshot.Candidates = append(snapshot.Candidates, recallCandidateRecord{v.Hit, v.Hit.Sources, v.Hit.Relevance, v.Score})
		}
		raw, e := json.Marshal(snapshot)
		if e != nil || len(raw) > rsiplugin.MaxInputBytes {
			return nil, recall.ErrUnavailable
		}
		payload = sql.NullString{String: string(raw), Valid: true}
		sealed = false
		keyRef = ""
		if hasMemory || r.vault.Ref != "" {
			v, e := r.recallVault(ctx, "")
			if e == nil {
				cipher, e := v.Seal(ctx, string(raw), binding)
				if e == nil {
					payload.String = cipher
					sealed = true
					keyRef = v.Ref
				} else if hasMemory {
					return nil, e
				}
			} else if hasMemory {
				return nil, e
			}
		}
		if hasMemory && !sealed {
			return nil, recall.ErrUnavailable
		}

		err = r.write(ctx, func(tx *sql.Tx) error {
			if e := r.evidenceFence(ctx, tx, a); e != nil {
				return e
			}
			_, e := tx.ExecContext(ctx, `INSERT INTO recall_candidate_snapshot VALUES(?,?,?,?,?,?,?,?)`, c.RunID(), operation, requestHash, a.epoch, sealed, keyRef, payload.String, time.Now().UnixMicro())
			return e
		})
		if err != nil {
			return nil, err
		}
		r.host.boundary("after_recall_candidates_commit")
	} else if err != nil {
		return nil, err
	} else {
		if savedHash != requestHash || epoch != a.epoch || !payload.Valid {
			return nil, recall.ErrConflict
		}
		plain := payload.String
		if sealed {
			v, e := r.recallVault(ctx, keyRef)
			if e != nil {
				return nil, e
			}
			plain, err = v.Open(ctx, plain, binding)
			if err != nil {
				return nil, err
			}
		}
		if err = rsiplugin.DecodeInput([]byte(plain), &snapshot); err != nil {
			return nil, err
		}
	}
	candidates := make([]recall.Candidate, len(snapshot.Candidates))
	for i, v := range snapshot.Candidates {
		v.Hit.Sources = v.Sources
		v.Hit.Relevance = v.Relevance
		candidates[i] = recall.Candidate{Hit: v.Hit, Score: v.Score}
	}
	// Non-JSON query policy belongs to this frozen operation, never to candidates.
	frozen := snapshot.Query
	frozen.ExcludeRefs = q.ExcludeRefs
	frozen.SeenRefs = q.SeenRefs
	frozen.EvidenceFirst = q.EvidenceFirst
	frozen.ExcerptBytes = q.ExcerptBytes
	hits, err := recall.SelectManaged(ctx, frozen, candidates, budget, func(ctx context.Context, q recall.Query, candidates []recall.Candidate) ([]recall.Selection, error) {
		facts := contract.Facts{Query: q.Query, EvidenceFirst: q.EvidenceFirst, ExcerptBytes: q.ExcerptBytes, Candidates: []contract.Candidate{}}
		for _, v := range candidates {
			quotes := []string{}
			for _, e := range v.Hit.Evidence {
				quotes = append(quotes, e.Quote)
			}
			facts.Candidates = append(facts.Candidates, contract.Candidate{Content: v.Hit.Content, Layer: v.Hit.Layer, SourceType: v.Hit.SourceType, Relevance: v.Hit.Relevance, Score: v.Score, Quotes: quotes})
		}
		raw, e := json.Marshal(facts)
		if e != nil {
			return nil, e
		}
		b, e := h.ServiceInstance(ctx, a.pluginGraph, contract.Contract)
		if e != nil {
			return nil, e
		}
		out, e := h.CallWithInput(ctx, rsiplugin.Execution{RunID: c.RunID()}, b.InstanceID, contract.Contract, operation+":select", json.RawMessage(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: raw}})
		if e != nil {
			return nil, e
		}
		var plan contract.Plan
		if e = rsiplugin.DecodeResult(out, &plan); e != nil {
			return nil, e
		}
		selections := make([]recall.Selection, len(plan.Entries))
		for i, p := range plan.Entries {
			selections[i] = recall.Selection{Index: p.Index, Offset: p.Offset, Length: p.Length, Evidence: p.Evidence}
		}
		return selections, nil
	})
	if err != nil {
		return nil, err
	}
	if err = h.Store.CheckGraph(ctx, a.pluginGraph); err != nil {
		return nil, err
	}
	return hits, nil
}

func recallQueryParts(messages []llm.Message, latest string) contract.QueryFacts {
	f := contract.QueryFacts{Latest: latest}
	current := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Origin != "runtime" {
			current = i
			break
		}
	}
	for i := current - 1; i >= 0; i-- {
		m := messages[i]
		if m.Origin == "runtime" {
			continue
		}
		if m.Role == "assistant" && f.Answer == "" && m.Text != "" {
			f.Answer = m.Text
		}
		if m.Role == "user" {
			f.Previous = m.Text
			break
		}
	}
	return f
}
func (r *Runs) conversationalRecall(ctx context.Context, c agent.RunClaim, run agent.Run, messages []llm.Message, latest string) (string, error) {
	managed, err := r.managedRunService(ctx, run.ID, contract.QueryContract)
	if err != nil {
		return "", err
	}
	if !managed {
		return conversationalRecallQuery(messages, latest), nil
	}
	facts := recallQueryParts(messages, latest)
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	out, err := r.featureService(ctx, c, contract.QueryContract, fmt.Sprintf("recall-query:%d:%s", run.Checkpoint.Step, digest(recallInputRef(messages))), raw)
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return "", llm.ProviderUnavailable
	}
	if err != nil {
		return "", err
	}
	var plan contract.QueryPlan
	if err = rsiplugin.DecodeResult(out, &plan); err != nil {
		return "", err
	}
	if len(plan.Query) > 4096 || !strings.HasPrefix(plan.Query, recallExcerpt(latest, 2048)) || facts.Previous == "" && facts.Answer == "" && plan.Query != latest {
		return "", rsi.ErrInvalid
	}
	return plan.Query, nil
}
