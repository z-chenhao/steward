package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

// PendingToolBatch is delivery state, not a second invocation lifecycle. The
// complete model response and every actual outcome remain in their own stores.
type PendingToolBatch struct {
	ExchangeID string         `json:"exchange_id"`
	Step       int64          `json:"step"`
	Calls      []llm.ToolCall `json:"calls"`
}

type MainActivity struct{ Input, Frozen bool }

// MainActivityStore is the Loop's private persistence port for checking ingress
// without changing an already accepted generation or an unfinished tool pair.
type MainActivityStore interface {
	MainActivity(context.Context, RunClaim, int64, int64) (MainActivity, error)
}

type toolJob struct {
	done    chan struct{}
	cancel  context.CancelFunc
	results []tool.Result
	err     error
}

func (j *toolJob) ready() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

func (l *agentLoop) activity(r Run) (MainActivity, error) {
	s, ok := l.options.Store.(MainActivityStore)
	if !ok || r.Kind != MainRole {
		return MainActivity{}, llm.ModelIncompatible
	}
	return s.MainActivity(l.work, l.claim, r.Checkpoint.InputSequence, r.Checkpoint.Step)
}

func (l *agentLoop) startTools(exchange string, calls []llm.ToolCall) *toolJob {
	if j := l.jobs[exchange]; j != nil {
		return j
	}
	ctx, cancel := context.WithCancel(l.execution)
	j := &toolJob{done: make(chan struct{}), cancel: cancel}
	l.jobs[exchange] = j
	go func() {
		defer close(j.done)
		defer cancel()
		j.results, j.err = l.options.Tools.Execute(ctx, exchange, calls)
	}()
	return j
}

func (l *agentLoop) joinTools(cancel bool) {
	if cancel {
		for _, j := range l.jobs {
			j.cancel()
		}
	}
	for _, j := range l.jobs {
		<-j.done
	}
}

func (l *agentLoop) drainTools(r Run) error {
	// A recovered owner may reach monetary consent before another mutable
	// model boundary. Reattach accepted batches even if no local job exists;
	// the Executor reuses results and reconciles uncertain effects as usual.
	for _, batch := range r.Checkpoint.PendingTools {
		l.startTools(batch.ExchangeID, batch.Calls)
	}
	l.joinTools(false)
	for _, j := range l.jobs {
		if j.err != nil && !errors.Is(j.err, tool.ErrSuspended) {
			return j.err
		}
	}
	return nil
}

func operationRef(step int64, index int) string {
	return fmt.Sprintf("%d-%d", step, index)
}

func deferredOperationRef(messages []AgentMessage, step int64, index int, callID string) string {
	ref := operationRef(step, index)
	legacy := fmt.Sprintf("operation:%d:%d", step, index)
	for _, prior := range messages {
		if prior.Type != "tool_result" || prior.ToolCallID != callID {
			continue
		}
		var receipt struct {
			Status string `json:"status"`
			Ref    string `json:"operation_ref"`
		}
		if json.Unmarshal([]byte(messageText(prior)), &receipt) == nil && receipt.Status == "pending" && (receipt.Ref == ref || receipt.Ref == legacy) {
			return receipt.Ref
		}
	}
	return ref
}

// Fast calls retain ordinary native ToolResults. Only a new input (or an
// already deferred batch) makes a slow call's delivery asynchronous.
func (l *agentLoop) responsiveTools(ctx context.Context, r Run) (bool, error) {
	cp := r.Checkpoint
	// Bound the number of in-process batches. Invocation-level quotas still
	// enforce the shared limits across direct calls and Code Mode.
	if len(cp.PendingTools) >= 8 && l.jobs[cp.ExchangeID] == nil {
		invs, err := l.options.Tools.Store.Accept(l.work, cp.ExchangeID, cp.Result.ToolCalls, r.Tools)
		if err != nil {
			return false, err
		}
		unstarted := true
		for _, inv := range invs {
			unstarted = unstarted && (inv.Status == "queued" || inv.Status == "failed" || inv.Status == "cancelled" || inv.Status == "succeeded")
		}
		if unstarted {
			results := make([]tool.Result, len(invs))
			for i, inv := range invs {
				if inv.Status != "queued" {
					results[i] = inv.Result
					continue
				}
				results[i] = tool.Failure("RESOURCE_LIMIT")
				if err = l.options.Tools.Store.Reject(l.work, inv.ID, results[i]); err != nil {
					return false, err
				}
			}
			return l.finishTools(r, results)
		}
	}
	j := l.startTools(cp.ExchangeID, cp.Result.ToolCalls)
	// Give ordinary local calls a small synchronous window. A waiting input
	// must not turn an already fast exchange into an unnecessary later reply.
	interruptAfter := time.Now().Add(200 * time.Millisecond)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		activity, err := l.activity(r)
		if err != nil {
			return false, err
		}
		if j.ready() {
			delete(l.jobs, cp.ExchangeID)
			if !errors.Is(j.err, tool.ErrSuspended) {
				if j.err != nil {
					return false, j.err
				}
				return l.finishTools(r, j.results)
			}
			if !activity.Input && len(cp.PendingTools) == 0 {
				reason, err := l.options.Tools.Store.WaitReason(l.work)
				if err != nil {
					return false, err
				}
				if err = l.options.Store.Wait(l.work, l.claim, r.Version, reason); err != nil {
					return false, err
				}
				l.emit(r, "agent_wait", reason, "")
				return true, nil
			}
			return l.deferTools(r)
		}
		if activity.Input && !time.Now().Before(interruptAfter) {
			return l.deferTools(r)
		}
		select {
		case <-ctx.Done():
			<-j.done // retain the existing graceful-shutdown boundary
			return false, ctx.Err()
		case <-j.done:
		case <-ticker.C:
		}
	}
}

func (l *agentLoop) deferTools(r Run) (bool, error) {
	cp := r.Checkpoint
	// The receipt cannot precede durable acceptance, even if the executor's
	// goroutine has not yet reached admission. Accept is fenced and idempotent.
	if _, err := l.options.Tools.Store.Accept(l.work, cp.ExchangeID, cp.Result.ToolCalls, r.Tools); err != nil {
		return false, err
	}
	cp.PendingTools = append(append([]PendingToolBatch(nil), cp.PendingTools...), PendingToolBatch{ExchangeID: cp.ExchangeID, Step: cp.Step, Calls: cp.Result.ToolCalls})
	results := make([]tool.Result, len(cp.Result.ToolCalls))
	for i := range results {
		raw, _ := json.Marshal(struct {
			Status       string `json:"status"`
			OperationRef string `json:"operation_ref"`
			Message      string `json:"message"`
		}{"pending", operationRef(cp.Step, i), "Final outcome will arrive in a runtime tool_results message. This receipt is not completion or approval; do not repeat the call."})
		results[i] = tool.Result{Text: string(raw)}
	}
	r.Checkpoint = cp
	stopped, err := l.finishTools(r, results)
	if err == nil {
		l.emit(r, "tool_results_deferred", "", "")
	}
	return stopped, err
}

// collectTools runs only at a mutable model boundary. Finished invocation
// results are checkpointed together with removal of their pending delivery,
// making a crash before/after this commit safe to recover without re-sending.
func (l *agentLoop) collectTools(r Run) (bool, error) {
	activity, err := l.activity(r)
	if err != nil || activity.Frozen {
		return false, err
	}
	cp := r.Checkpoint
	remaining := []PendingToolBatch{}
	changed := false
	for _, batch := range cp.PendingTools {
		j := l.startTools(batch.ExchangeID, batch.Calls)
		if !j.ready() || errors.Is(j.err, tool.ErrSuspended) {
			remaining = append(remaining, batch)
			continue
		}
		if j.err != nil {
			return false, j.err
		}
		for i, result := range j.results {
			if err := applyToolLoads(&cp, r.Tools, batch.Calls[i].Name, result); err != nil {
				return false, err
			}
			projected, err := l.projectTool(r, batch.Calls[i].ID, result)
			if err != nil {
				return false, err
			}
			result = projected
			// A pre-upgrade pending receipt keeps its exact correlation identity.
			ref := deferredOperationRef(cp.Messages, batch.Step, i, batch.Calls[i].ID)
			m := RuntimeContext("tool_results", "", result.Error != nil && result.Error.Code == "UNKNOWN_SIDE_EFFECT", struct {
				OperationRef string      `json:"operation_ref"`
				Tool         string      `json:"tool"`
				Result       tool.Result `json:"result"`
			}{ref, batch.Calls[i].Name, result.ForModel()})
			for _, imageRef := range result.ImageRefs {
				m.Content = append(m.Content, conversation.ContentPart{Type: "image", ArtifactRef: imageRef})
			}
			cp.Messages = append(cp.Messages, m)
		}
		delete(l.jobs, batch.ExchangeID)
		changed = true
	}
	if !changed {
		return false, nil
	}
	cp.PendingTools = remaining
	cp.Phase = ModelPhase
	if err = l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
		return false, err
	}
	l.emit(r, "tool_results_delivered", "", "")
	return true, nil
}

func (l *agentLoop) waitTools(ctx context.Context, r Run) (bool, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		changed, err := l.collectTools(r)
		if err != nil || changed {
			return false, err
		}
		activity, err := l.activity(r)
		if err != nil {
			return false, err
		}
		if activity.Input {
			cp := r.Checkpoint
			cp.Phase = ModelPhase
			return false, l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp)
		}
		active := false
		ready := false
		for _, j := range l.jobs {
			active = active || !j.ready()
			ready = ready || j.ready() && !errors.Is(j.err, tool.ErrSuspended)
		}
		if ready {
			// A job can finish between collection and the idle check. Collect
			// it before releasing ownership, even when it was the last job.
			continue
		}
		if !active {
			reason, err := l.options.Tools.Store.WaitReason(l.work)
			if err != nil {
				return false, err
			}
			if err = l.options.Store.Wait(l.work, l.claim, r.Version, reason); err != nil {
				return false, err
			}
			l.emit(r, "agent_wait", reason, "")
			return true, nil
		}
		select {
		case <-ctx.Done():
			l.joinTools(false)
			return false, ctx.Err()
		case <-ticker.C:
			// A suspended sibling can become ready while another job runs.
			for exchange, j := range l.jobs {
				if j.ready() && errors.Is(j.err, tool.ErrSuspended) {
					delete(l.jobs, exchange)
				}
			}
		}
	}
}
