package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/taskcontrol/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type taskLabel struct {
	Managed            bool
	Title, RequestHash string
}

func (t *tools) executeTask(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	if result, handled, err := t.searchTaskPlugin(ctx, inv); handled || err != nil {
		return result, err
	}
	label, err := t.prepareTaskLabel(ctx, inv)
	if err != nil {
		return taskPolicyFailure(err)
	}
	feedback, err := t.prepareFeedbackPolicy(ctx, inv)
	if err != nil {
		return taskPolicyFailure(err)
	}
	return t.executeTaskWithLabel(ctx, inv, label, feedback)
}

// The pure discovery path does not reserve a writer or write a second receipt.
// Its complete result is frozen by the existing accepted Tool pipeline.
func (t *tools) searchTaskPlugin(ctx context.Context, inv tool.Invocation) (tool.Result, bool, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.SearchContract)
	if err != nil || !managed {
		return tool.Result{}, false, err
	}
	var request task.SearchRequest
	var candidates []task.Candidate
	handled := false
	invalid := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != task.SearchName {
			return nil
		}
		handled = true
		if kind != "main" {
			return tool.ErrApprovalDenied
		}
		request, err = task.ParseSearch(args)
		if err != nil {
			invalid = true
			return nil
		}
		return nil
	})
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), true, nil
	}
	if err != nil || !handled {
		return tool.Result{}, handled, err
	}
	if invalid {
		return tool.Failure("INVALID_ARGUMENT"), true, nil
	}
	h, a, err := t.pureToolPolicyHost(ctx, inv)
	if err != nil {
		result, e := taskPolicyFailure(err)
		return result, true, e
	}
	if err = h.Store.CheckGraph(ctx, a.pluginGraph); err != nil {
		result, e := taskPolicyFailure(err)
		return result, true, e
	}
	if _, err = h.ServiceInstance(ctx, a.pluginGraph, contract.SearchContract); err != nil {
		result, e := taskPolicyFailure(err)
		return result, true, e
	}
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		var err error
		candidates, err = t.runs.taskCandidates(ctx, tx, t.claim.RunID(), request, rsiplugin.MaxInputBytes)
		return err
	})
	if errors.Is(err, agent.ErrResourceLimit) {
		return tool.Failure("RESOURCE_LIMIT"), true, nil
	}
	if err != nil {
		return tool.Result{}, true, err
	}
	facts := contract.SearchFacts{Query: request.Query, Limit: request.Limit, Candidates: make([]contract.Candidate, len(candidates))}
	for i, c := range candidates {
		facts.Candidates[i] = contract.Candidate{Text: c.Text, Status: c.Summary.Status, UpdatedAt: c.Summary.UpdatedAt}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return tool.Result{}, true, err
	}
	if len(raw) > rsiplugin.MaxInputBytes {
		return tool.Failure("RESOURCE_LIMIT"), true, nil
	}
	response, err := h.ReadService(ctx, a.pluginGraph, contract.SearchContract, json.RawMessage(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: raw}})
	if err != nil {
		result, e := taskPolicyFailure(err)
		return result, true, e
	}
	var plan contract.SearchPlan
	if err = rsiplugin.DecodeResult(response, &plan); err != nil {
		return tool.Result{}, true, err
	}
	result, err := taskSelection(candidates, request.Limit, plan)
	if err != nil {
		return tool.Result{}, true, err
	}
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.runs.evidenceFence(ctx, tx, a) })
	return result, true, err
}

func (t *tools) pureToolPolicyHost(ctx context.Context, inv tool.Invocation) (rsi.Host, evidenceAccess, error) {
	a := evidenceAccess{claim: t.claim}
	a.scope.RunID = t.claim.RunID()
	a.scope.UserID = t.runs.host.OwnerID()
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, inv); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT r.agent_id,coalesce(b.graph_revision,''),coalesce(p.epoch,0) FROM run r JOIN rsi_run b USING(run_id) LEFT JOIN memory_privacy p USING(agent_id) WHERE r.run_id=?`, t.claim.RunID()).Scan(&a.scope.AgentID, &a.pluginGraph, &a.epoch)
	})
	if err != nil {
		return rsi.Host{}, a, err
	}
	if t.runs.plugins == nil || a.pluginGraph == "" {
		return rsi.Host{}, a, rsi.ErrNotFound
	}
	h := *t.runs.plugins
	store := t.runs.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		_, _, _, err := t.acceptedBuiltin(ctx, tx, inv)
		return err
	}
	h.Store = store
	return h, a, nil
}

func (t *tools) prepareTaskLabel(ctx context.Context, inv tool.Invocation) (taskLabel, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.LabelContract)
	label := taskLabel{Managed: managed}
	if err != nil || !managed {
		return label, err
	}
	var request task.ManageRequest
	needed := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if kind != "main" || name != task.ManageName {
			return nil
		}
		request, err = task.ParseManage(args)
		if err != nil || request.Operation != "create" {
			return nil
		}
		canonical, _ := encoded(request)
		label.RequestHash = digest(canonical)
		// Effects already accepted in the Task transaction replay their original
		// receipt even if the old method can no longer be dispatched.
		var accepted bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_invocation WHERE invocation_id=?)`, inv.ID).Scan(&accepted); err != nil || accepted {
			return err
		}
		var source string
		if err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT i.message_id FROM main_input i WHERE i.run_id=r.run_id AND i.sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY i.sequence DESC LIMIT 1),(SELECT request_id FROM main_inbox b WHERE b.run_id=r.run_id ORDER BY b.sequence DESC LIMIT 1),'') FROM run r WHERE r.run_id=?`, t.claim.RunID()).Scan(&source); err != nil {
			return err
		}
		if source != "" {
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_request WHERE source_message_id=? AND request_hash=?)`, source, label.RequestHash).Scan(&accepted); err != nil || accepted {
				return err
			}
		}
		if source == "" {
			return nil
		}
		allowed, err := t.runs.checkGitHubAppend(ctx, tx, t.claim.RunID(), request)
		if err != nil {
			return err
		}
		if !allowed {
			return nil
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		for _, ref := range request.ArtifactRefs {
			if _, err = t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, ref, true); err != nil {
				return nil
			}
		}
		needed = true
		return nil
	})
	if err != nil || !needed {
		return label, err
	}
	h, a, err := t.pureToolPolicyHost(ctx, inv)
	if err != nil {
		return label, err
	}
	binding, err := h.ServiceInstance(ctx, a.pluginGraph, contract.LabelContract)
	if err != nil {
		return label, err
	}
	text := request.Title
	if text == "" {
		text = request.Task
	}
	input, _ := json.Marshal(contract.LabelFacts{Text: text, Explicit: request.Title != ""})
	if err = h.Store.CheckGraph(ctx, a.pluginGraph); err != nil {
		return label, err
	}
	plan, saved, err := t.loadTaskLabelPlan(ctx, inv, a, label.RequestHash, binding.InstanceID, input)
	if err != nil {
		return label, err
	}
	if saved {
		if !validTaskLabel(request, plan.Title) {
			return label, rsi.ErrInvalid
		}
		label.Title = plan.Title
		return label, nil
	}
	// This adapter has no state or effect callbacks. An interrupted read can be
	// computed again; only a complete, validated plan is durably accepted below.
	// The generic opaque Call journal retains its unknown/no-retry semantics.
	var raw json.RawMessage
	if plan.Title == "" {
		raw, err = h.ReadService(ctx, a.pluginGraph, contract.LabelContract, input, nil)
	}
	if err != nil {
		if authority := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.runs.evidenceFence(ctx, tx, a) }); authority != nil {
			return label, authority
		}
		return label, err
	}
	if raw != nil {
		if err = rsiplugin.DecodeResult(raw, &plan); err != nil {
			return label, err
		}
	}
	if !validTaskLabel(request, plan.Title) {
		return label, rsi.ErrInvalid
	}
	t.runs.host.boundary("before_task_label_commit")
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, inv); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		var graph, hash string
		var title sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT graph_revision,request_hash,title FROM task_label_plan WHERE invocation_id=?`, inv.ID).Scan(&graph, &hash, &title)
		if err == nil {
			if graph != a.pluginGraph || hash != label.RequestHash || !title.Valid || !validTaskLabel(request, title.String) {
				return agent.ErrConflict
			}
			label.Title = title.String
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO task_label_plan VALUES(?,?,?,?)`, inv.ID, a.pluginGraph, label.RequestHash, plan.Title)
		label.Title = plan.Title
		return err
	})
	if err == nil {
		t.runs.host.boundary("after_task_label_commit")
	}
	return label, err
}

// Completed pre-schema64 label outputs are historical accepted choices. Reuse
// them without redispatching the opaque call. Incomplete old calls remain audit
// facts; any new computation uses the capability-free pure-read adapter above.
// Remove this compatibility read only when no retained pre-schema64 invocation
// needs its original label output.
func (t *tools) loadTaskLabelPlan(ctx context.Context, inv tool.Invocation, a evidenceAccess, requestHash, instance string, input json.RawMessage) (plan contract.LabelPlan, saved bool, err error) {
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, inv); err != nil {
			return err
		}
		var graph, hash string
		var title sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT graph_revision,request_hash,title FROM task_label_plan WHERE invocation_id=?`, inv.ID).Scan(&graph, &hash, &title)
		if err == nil {
			if graph != a.pluginGraph || hash != requestHash {
				return agent.ErrConflict
			}
			if !title.Valid {
				return rsi.ErrRevoked
			}
			plan.Title, saved = title.String, true
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var value sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT request_hash,result_json FROM rsi_call WHERE run_id=? AND instance_id=? AND operation_id=?`, t.claim.RunID(), instance, "task-label:"+inv.ID).Scan(&hash, &value)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		legacyHash, _ := rsiplugin.Digest("steward.rsi.input/1", struct {
			Contract string
			Input    json.RawMessage
		}{contract.LabelContract, input})
		if hash != legacyHash {
			return rsi.ErrConflict
		}
		if value.Valid {
			if err := rsiplugin.DecodeResult([]byte(value.String), &plan); err != nil {
				return err
			}
			if plan.Title == "" {
				return rsi.ErrInvalid
			}
		}
		return nil
	})
	return
}
func validTaskLabel(q task.ManageRequest, title string) bool {
	return strings.TrimSpace(title) != "" && task.Title(title) == title && (q.Title == "" || title == task.Title(q.Title))
}
func taskSelection(candidates []task.Candidate, limit int, p contract.SearchPlan) (tool.Result, error) {
	if len(p.Indices) > limit {
		return tool.Result{}, rsi.ErrInvalid
	}
	seen := map[int]bool{}
	out := make([]task.Summary, 0, len(p.Indices))
	for _, i := range p.Indices {
		if i < 0 || i >= len(candidates) || seen[i] {
			return tool.Result{}, rsi.ErrInvalid
		}
		seen[i] = true
		out = append(out, candidates[i].Summary)
	}
	raw, err := encoded(out)
	return tool.Result{Text: raw}, err
}
func taskPolicyFailure(err error) (tool.Result, error) {
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	return tool.Result{}, err
}
