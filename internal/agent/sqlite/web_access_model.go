package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (t *tools) webModelGeneration(ctx context.Context, inv tool.Invocation, run agent.Run, key string, m webaccess.ModelCall) (llm.GenerationResult, error) {
	req := llm.GenerationRequest{RunID: run.ID, SessionID: run.ID, Snapshot: run.Snapshot, OutputLimitBytes: 32000, ToolGeneration: &llm.ToolGeneration{InvocationID: inv.ID, CallKey: key, Kind: m.Kind}}
	switch m.Kind {
	case "hosted_search":
		if m.Query == nil || m.Query.Validate() != nil || run.Snapshot.ProviderKind != "openai_account" {
			return llm.GenerationResult{}, llm.ModelIncompatible
		}
		req.WebSearch = &llm.WebSearchRequest{InvocationID: inv.ID, Query: m.Query.Query, DomainFilter: m.Query.DomainFilter, RecencyFilter: m.Query.RecencyFilter, NumResults: m.Query.NumResults}
		if len(m.Messages) > 0 {
			if len(m.Messages) != 2 || m.Messages[0].Role != "system" || m.Messages[1].Role != "user" || m.Messages[1].Text != m.Query.Query || len(m.Messages[0].Text) > 8192 || m.Messages[0].Text == "" {
				return llm.GenerationResult{}, llm.ModelIncompatible
			}
			req.Messages = m.Messages
		} else {
			// Original pre-instruction snapshots retain their published method.
			req.Messages = webaccess.LegacySearchMessagesV2(*m.Query)
		}
	case "page_answer", "summary":
		if len(m.Messages) != 2 || m.Messages[0].Role != "system" || m.Messages[1].Role != "user" {
			return llm.GenerationResult{}, llm.ModelIncompatible
		}
		req.Messages = m.Messages
	default:
		return llm.GenerationResult{}, llm.ModelIncompatible
	}
	if t.runs.webProvider == nil {
		return llm.GenerationResult{}, llm.ProviderUnavailable
	}
	encodedMessages, _ := json.Marshal(req.Messages)
	if int64(len(encodedMessages)) > run.Snapshot.ContextWindow*3 {
		return llm.GenerationResult{}, llm.ContextLengthExceeded
	}
	pricePlan, err := t.runs.prepareReferenceRates(ctx, t.claim, req, 0)
	if err != nil {
		return llm.GenerationResult{}, err
	}
	var cached *llm.GenerationResult
	var admission error
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		_, name, acceptedArgs, e := t.acceptedBuiltin(ctx, tx, inv)
		if e != nil {
			return e
		}
		if (m.Kind == "hosted_search" || m.Kind == "summary") && name != webaccess.SearchName && name != webaccess.CheckName || m.Kind == "page_answer" && name != webaccess.ContentName {
			return agent.ErrConflict
		}
		if m.Kind == "hosted_search" && !acceptedWebQuery(name, acceptedArgs, req.WebSearch.Query) {
			return tool.ErrApprovalDenied
		}
		var status, raw, hash string
		e = tx.QueryRowContext(ctx, `SELECT generation_id,status,coalesce(result_json,''),request_hash FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.invocation_id')=? AND json_extract(request_json,'$.tool_generation.call_key')=?`, run.ID, inv.ID, key).Scan(&req.GenerationID, &status, &raw, &hash)
		if e == nil {
			expected, _ := encoded(req)
			if digest(expected) != hash {
				return agent.ErrConflict
			}
			if status == "completed" {
				var result llm.GenerationResult
				if json.Unmarshal([]byte(raw), &result) != nil {
					return agent.ErrStorage
				}
				cached = &result
			} else {
				admission = llm.ProviderUnavailable
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		req.GenerationID = newID()
		raw, e = encoded(req)
		if e != nil {
			return e
		}
		var step int64
		if e = tx.QueryRowContext(ctx, `SELECT min(coalesce(min(step_index),0),0)-1 FROM model_generation WHERE run_id=?`, run.ID).Scan(&step); e != nil {
			return e
		}
		owner, ok := t.claim.(*claim)
		if !ok {
			return agent.ErrClaimLost
		}
		req.OwnerToken = owner.token
		if _, e = tx.ExecContext(ctx, `INSERT INTO model_generation(generation_id,run_id,step_index,request_json,request_hash,owner_token,status,created_at,reference_rates_managed) VALUES(?,?,?,?,?,?,'pending',?,?)`, req.GenerationID, run.ID, step, raw, digest(raw), owner.token, time.Now().UnixMicro(), pricePlan != nil); e != nil {
			return e
		}
		if pricePlan != nil {
			if e = t.runs.pluginDispatchFence(ctx, tx, run.ID); e != nil {
				return e
			}
		}
		if e = acceptReferenceRates(ctx, tx, req.GenerationID, pricePlan); e != nil {
			return e
		}
		waiting, denied, e := t.runs.gateSpend(ctx, tx, req)
		if e != nil {
			return e
		}
		if waiting || denied {
			admission = errors.New("BUDGET_APPROVAL_REQUIRED")
			if denied {
				admission = errors.New("BUDGET_DENIED")
			}
			_, e = tx.ExecContext(ctx, `UPDATE model_generation SET status='failed',error_code=?,completed_at=? WHERE generation_id=?`, admission.Error(), time.Now().UnixMicro(), req.GenerationID)
		}
		return e
	})
	if err != nil {
		return llm.GenerationResult{}, err
	}
	if cached != nil {
		return *cached, nil
	}
	if admission != nil {
		return llm.GenerationResult{}, admission
	}
	return t.runs.webProvider.Generate(ctx, req, nil)
}

func acceptedWebQuery(name, args, query string) bool {
	var v struct {
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		Claim   string   `json:"claim"`
	}
	if json.Unmarshal([]byte(args), &v) != nil {
		return false
	}
	candidates := v.Queries
	if len(candidates) == 0 {
		if name == webaccess.CheckName {
			candidates = []string{v.Claim}
		} else {
			candidates = []string{v.Query}
		}
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == query {
			return true
		}
	}
	return false
}
