package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

// RunFailure proves that the Run's terminal failure was committed. Interactive
// surfaces may keep serving; callers can still inspect the provider cause.
type RunFailure struct{ cause error }

func (e *RunFailure) Error() string { return e.cause.Error() }
func (e *RunFailure) Unwrap() error { return e.cause }

// CommittedRunFailure is used by initialization only after the same RunStore
// terminal failure has committed. It preserves normal Worker failure isolation.
func CommittedRunFailure(cause error) error { return &RunFailure{cause: cause} }

type LoopOptions struct {
	Store    RunStore
	Provider llm.LLMProvider
	Tools    tool.Executor
	Sink     EventSink
	// Execution is cancelled for business stop or lost ownership. Shutdown
	// still waits for the current step to persist at its stable boundary.
	Execution context.Context
}

// RunAgentLoop and ContinueAgentLoop share one stateless kernel. Roles supply
// context, tools and a result destination outside this algorithm.
func RunAgentLoop(ctx context.Context, claim RunClaim, options LoopOptions) error {
	return ContinueAgentLoop(ctx, claim, options)
}

func ContinueAgentLoop(ctx context.Context, claim RunClaim, options LoopOptions) error {
	work := context.WithoutCancel(ctx)
	execution := options.Execution
	if execution == nil {
		execution = work
	}
	l := agentLoop{claim: claim, options: options, work: work, execution: execution, jobs: map[string]*toolJob{}}
	defer l.joinTools(true)
	defer l.discardPreview()
	return l.run(ctx)
}

// The runner owns no durable state. Every phase reloads the authoritative Run;
// each phase handler advances exactly one checkpoint or stops at a stable wait.
// Shutdown stops between phases. Business stop/claim loss cancels execution
// while persistence continues through the independent work context.
type agentLoop struct {
	claim     RunClaim
	options   LoopOptions
	work      context.Context
	execution context.Context
	jobs      map[string]*toolJob
	uiSession string
	uiBase    context.Context
	uiStop    func() bool
	preview   Event // Main generation whose temporary text may still become a reply.
}

func (l *agentLoop) run(ctx context.Context) error {
	r, err := l.options.Store.Load(l.work, l.claim)
	if err != nil {
		return err
	}
	l.emit(r, "agent_start", "", "")
	l.beginUIPreview(r)
	defer func() { l.endUIPreview(r, "ui_preview_discarded", "") }()
	for {
		if ctx.Err() != nil {
			// Shutdown joins accepted work; it is not a business cancellation.
			l.joinTools(false)
			return ctx.Err()
		}
		r, err = l.options.Store.Load(l.work, l.claim)
		if err != nil {
			return err
		}
		// Settlement can request consent after a final model result was saved.
		// Hold that result without regenerating it.
		if r.SpendStatus == "pending" {
			return l.wait(r, "resource")
		}
		// Accepted Tool calls must drain and adopt terminal Child results even
		// when monetary authorization is denied at the next model boundary.
		if (r.SpendStatus == "denied" || r.SpendStatus == "invalidated") && r.Checkpoint.Phase != ToolsPhase {
			return l.fail(r, "SPEND_AUTHORIZATION_DENIED")
		}
		var stopped bool
		switch r.Checkpoint.Phase {
		case ModelPhase:
			if r.ResponsiveTools && len(r.Checkpoint.PendingTools) > 0 {
				changed, collectErr := l.collectTools(r)
				if collectErr != nil {
					return collectErr
				}
				if changed {
					continue
				}
			}
			stopped, err = l.modelTurn(r)
		case ToolsPhase:
			if r.ResponsiveTools {
				stopped, err = l.responsiveTools(ctx, r)
			} else {
				stopped, err = l.toolsTurn(r)
			}
		case ReplyPhase:
			stopped, err = l.replyTurn(r)
		case SettlePhase:
			if r.ResponsiveTools && len(r.Checkpoint.PendingTools) > 0 {
				stopped, err = l.waitTools(ctx, r)
			} else {
				stopped, err = l.settleTurn(r)
			}
		default:
			return ErrStorage
		}
		if stopped || err != nil {
			return err
		}
	}
}

func (l *agentLoop) modelTurn(r Run) (bool, error) {
	cp := r.Checkpoint
	inputs, err := l.options.Store.Steering(l.work, l.claim, cp.InputSequence)
	if errors.Is(err, ErrConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(inputs) > 0 {
		for _, input := range inputs {
			cp.Messages = append(cp.Messages, input.Context...)
			cp.Messages = append(cp.Messages, AgentMessage{MessageID: input.Message.MessageID, Type: "user", Content: input.Message.Content})
			cp.InputSequence = input.Sequence
		}
		if err = l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
			return false, err
		}
		return false, nil
	}
	// Context preparation may retrieve durable evidence. Business stop or
	// lost ownership must cancel those provider reads before model admission.
	generation, err := l.options.Store.Generation(l.execution, l.claim, r)
	if err != nil {
		if errors.Is(err, llm.ContextLengthExceeded) {
			if retry, e := l.retryContext(r); retry {
				if e != nil {
					return false, e
				}
				return false, nil
			}
		}
		if errors.Is(err, ErrResourceLimit) {
			return false, l.fail(r, "RESOURCE_LIMIT")
		}
		var providerError llm.ProviderError
		if errors.As(err, &providerError) {
			return false, l.fail(r, providerError.Error())
		}
		return false, err
	}
	if generation.Waiting {
		if err := l.wait(r, "resource"); err != nil {
			return false, err
		}
		l.emit(r, "agent_wait", "resource", "")
		return true, nil
	}
	if generation.Denied {
		return false, l.fail(r, "SPEND_AUTHORIZATION_DENIED")
	}
	if generation.ErrorCode != "" {
		if generation.ErrorCode == llm.ContextLengthExceeded.Error() {
			if retry, e := l.retryContext(r); retry {
				if e != nil {
					return false, e
				}
				return false, nil
			}
		}
		return false, l.fail(r, generation.ErrorCode)
	}
	result := generation.Result
	keepPreview := false
	defer func() {
		if !keepPreview {
			l.discardPreview()
		}
	}()
	if result != nil {
		l.emit(r, "generation_reused", "", "")
	}
	keepUIPrefixes := false
	if result == nil {
		visible := generation.Compaction == nil
		if visible {
			l.emit(r, "turn_start", "", "")
			start := l.event(r, "message_start", "", "")
			start.GenerationID = generation.Request.GenerationID
			if r.Kind == MainRole {
				l.preview = start
			}
			l.publish(start)
		}
		// Give the Bridge's 180-second generation budget time to report its outcome.
		callCtx, cancel := context.WithTimeout(l.execution, 195*time.Second)
		stopDiscard := l.discardOnCancel(callCtx)
		generate := l.options.Provider.Generate
		if cp.Step > 0 {
			generate = l.options.Provider.Continue
		}
		prefixes := map[string]string{}
		lastPreview := map[string]time.Time{}
		defer func() {
			for key := range prefixes {
				if keepUIPrefixes {
					continue
				}
				l.publishUIPreview(r, "ui_preview_remove", key, nil)
			}
		}()
		value, callErr := generate(callCtx, generation.Request, func(e llm.GenerationEvent) {
			if visible && r.Kind == MainRole && callCtx.Err() == nil && e.Kind == "ui_delta" && e.ToolCall != nil && e.ToolCall.Name == ui.ShowName {
				key := generation.Request.GenerationID + ":" + e.ToolCall.ID
				if len(prefixes) < 16 || prefixes[key] != "" {
					if len(prefixes[key])+len(e.ToolCall.Arguments) <= 128<<10 {
						prefixes[key] += e.ToolCall.Arguments
						if time.Since(lastPreview[key]) >= 25*time.Millisecond {
							lastPreview[key] = time.Now()
							if preview, ok := ui.ParsePreview(prefixes[key]); ok {
								l.publishUIPreview(r, "ui_preview_update", key, &preview)
							}
						}
					}
				}
			}
			if visible && e.Kind == "text_delta" && e.Text != "" && callCtx.Err() == nil {
				update := l.event(r, "message_update", e.Text, "")
				update.GenerationID = generation.Request.GenerationID
				l.publish(update)
			}
		})
		stopDiscard()
		cancel()
		if l.execution.Err() != nil {
			return false, l.execution.Err()
		}
		if callErr != nil {
			l.discardPreview()
			// Storage/claim rejection is infrastructure, not a failed model.
			if errors.Is(callErr, ErrClaimLost) || errors.Is(callErr, ErrStorage) || errors.Is(callErr, llm.ErrStorage) {
				return false, callErr
			}
			// A failed provider response may still report billable usage.
			// Settlement can have requested Root consent atomically with
			// that failure; preserve it before finalizing the Run.
			latest, loadErr := l.options.Store.Load(l.work, l.claim)
			if loadErr != nil {
				return false, loadErr
			}
			if latest.SpendStatus == "pending" {
				if err := l.wait(latest, "resource"); err != nil {
					return false, err
				}
				l.emit(r, "agent_wait", "resource", "")
				return true, nil
			}
			if latest.SpendStatus == "denied" || latest.SpendStatus == "invalidated" {
				return false, l.fail(latest, "SPEND_AUTHORIZATION_DENIED")
			}
			if errors.Is(callErr, llm.ContextLengthExceeded) {
				if retry, e := l.retryContext(latest); retry {
					if e != nil {
						return false, e
					}
					return false, nil
				}
			}
			return false, l.fail(latest, llm.NormalizeError(callErr.Error()).Error())
		}
		result = &value
	}
	if plan := generation.Compaction; plan != nil {
		if result.Clipped || len(result.ToolCalls) > 0 || strings.TrimSpace(result.Text) == "" || len(result.Text) > plan.MaxBytes {
			return false, l.fail(r, llm.InvalidProviderResponse.Error())
		}
		cp.WorkingState = &WorkingState{Through: plan.Through, Text: result.Text, SourceRefs: plan.SourceRefs, ArchiveRef: plan.ArchiveRef}
		cp.Step++
		cp.Result = nil
		cp.ExchangeID = ""
		cp.Phase = ModelPhase
		return false, l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp)
	}
	cp.ContextRetries = 0
	// Bridge acceptance has durably saved the complete result already.
	parts := conversation.Text(result.Text)
	for _, call := range result.ToolCalls {
		c := conversation.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
		parts = append(parts, conversation.ContentPart{Type: "tool_call", ToolCall: &c})
	}
	cp.Messages = append(cp.Messages, AgentMessage{Type: "assistant", Content: parts})
	cp.Result = result
	cp.ExchangeID = generation.Request.GenerationID
	cp.Phase = ReplyPhase
	if len(result.ToolCalls) > 0 {
		cp.Phase = ToolsPhase
	}
	if err = l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
		return false, err
	}
	end := l.event(r, "message_end", "", "")
	end.GenerationID = cp.ExchangeID
	l.publish(end)
	keepUIPrefixes = cp.Phase == ToolsPhase
	keepPreview = cp.Phase == ReplyPhase
	return false, nil
}

func (l *agentLoop) toolsTurn(r Run) (bool, error) {
	cp := r.Checkpoint
	results, err := l.options.Tools.Execute(l.execution, cp.ExchangeID, cp.Result.ToolCalls)
	if errors.Is(err, tool.ErrSuspended) {
		reason, waitErr := l.options.Tools.Store.WaitReason(l.work)
		if waitErr != nil {
			return false, waitErr
		}
		if err = l.options.Store.Wait(l.work, l.claim, r.Version, reason); err != nil {
			return false, err
		}
		l.emit(r, "agent_wait", reason, "")
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return l.finishTools(r, results)
}

func (l *agentLoop) finishTools(r Run, results []tool.Result) (bool, error) {
	cp := r.Checkpoint
	for i, result := range results {
		if result.Error != nil && i < len(cp.Result.ToolCalls) && cp.Result.ToolCalls[i].Name == ui.ShowName {
			l.publishUIPreview(r, "ui_preview_remove", cp.ExchangeID+":"+cp.Result.ToolCalls[i].ID, nil)
		}
	}
	unknownExchange := false
	for _, result := range results {
		if result.Error != nil && result.Error.Code == "UNKNOWN_SIDE_EFFECT" {
			unknownExchange = true
		}
	}
	if unknownExchange {
		// Preserve the complete native exchange, including all sibling results,
		// rather than inserting a user message between Tool calls and replies.
		for i := len(cp.Messages) - 1; i >= 0; i-- {
			if cp.Messages[i].Type == "assistant" {
				cp.Messages[i].Source = &llm.ContextSource{Kind: "unknown_exchange", Protected: true}
				break
			}
		}
	}
	for i, result := range results {
		if err := applyToolLoads(&cp, r.Tools, cp.Result.ToolCalls[i].Name, result); err != nil {
			return false, err
		}
		parts := conversation.Text(result.ModelText())
		for _, ref := range result.ImageRefs {
			parts = append(parts, conversation.ContentPart{Type: "image", ArtifactRef: ref})
		}
		message := AgentMessage{Type: "tool_result", ToolCallID: cp.Result.ToolCalls[i].ID, Content: parts}
		if unknownExchange {
			message.Source = &llm.ContextSource{Kind: "unknown_exchange", Protected: true}
		}
		cp.Messages = append(cp.Messages, message)
	}
	cp.Step++
	cp.Phase = ModelPhase
	cp.ExchangeID = ""
	cp.Result = nil
	if err := l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
		return false, err
	}
	l.emit(r, "turn_end", "", "")
	return false, nil
}

func (l *agentLoop) replyTurn(r Run) (bool, error) {
	cp := r.Checkpoint
	ready, err := l.options.Store.PrepareReply(l.work, l.claim, r)
	if err != nil {
		return false, err
	}
	if !ready {
		l.discardPreview()
		// A completed generation may have been overtaken by user steering.
		// Keep it as model context, then inject separate user messages.
		cp.Step++
		cp.Phase = ModelPhase
		cp.Result = nil
		cp.ExchangeID = ""
		if err = l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
			return false, err
		}
		return false, nil
	}
	if err = l.options.Store.DeliverReply(l.work, l.claim, r); err != nil {
		return false, err
	}
	l.finishPreview("reply_committed")
	l.endUIPreview(r, "ui_preview_committed", cp.ExchangeID)
	l.emit(r, "turn_end", "", "")
	return false, nil
}

func (l *agentLoop) settleTurn(r Run) (bool, error) {
	cp := r.Checkpoint
	done, err := l.options.Store.Complete(l.work, l.claim, r.Version)
	if err != nil {
		return false, err
	}
	if done {
		l.emit(r, "agent_end", "", "")
		return true, nil
	}
	cp.Phase = ModelPhase
	l.beginUIPreview(r)
	if err = l.options.Store.Checkpoint(l.work, l.claim, r.Version, cp); err != nil {
		return false, err
	}
	return false, nil
}

func (l *agentLoop) emit(r Run, kind, text, call string) {
	l.publish(l.event(r, kind, text, call))
}

func (l *agentLoop) event(r Run, kind, text, call string) Event {
	return Event{Kind: kind, RunID: r.ID, RunKind: string(r.Kind), AgentID: r.AgentID, Step: r.Checkpoint.Step, OccurredAt: time.Now().UTC(), Text: text, ToolCallID: call}
}

func (l *agentLoop) publish(e Event) {
	if l.options.Sink != nil {
		l.options.Sink(e)
	}
}

// Withdraw the draft at cancellation even if the provider is still cleaning up.
func (l *agentLoop) discardOnCancel(ctx context.Context) func() {
	e := l.preview
	if e.GenerationID == "" || l.options.Sink == nil {
		return func() {}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		e.Kind, e.OccurredAt = "message_discarded", time.Now().UTC()
		l.publish(e)
		close(done)
	})
	return func() {
		if !stop() {
			<-done
			l.preview = Event{}
		}
	}
}

func (l *agentLoop) discardPreview() { l.finishPreview("message_discarded") }

func (l *agentLoop) finishPreview(kind string) {
	if l.preview.GenerationID == "" {
		return
	}
	e := l.preview
	l.preview = Event{}
	e.Kind, e.OccurredAt = kind, time.Now().UTC()
	l.publish(e)
}

func (l *agentLoop) wait(r Run, reason string) error {
	// Waiting revokes the running-state fence. Drain accepted batches before
	// that transition so model consent cannot cancel or orphan their outcomes.
	if err := l.drainTools(r); err != nil {
		return err
	}
	return l.options.Store.Wait(l.work, l.claim, r.Version, reason)
}

func (l *agentLoop) fail(r Run, code string) error {
	if code == "SPEND_AUTHORIZATION_DENIED" {
		if err := l.drainTools(r); err != nil {
			return err
		}
	} else {
		l.joinTools(true)
	}
	if err := l.options.Store.Fail(l.work, l.claim, r.Version, code); err != nil {
		return err
	}
	if code == "SPEND_AUTHORIZATION_DENIED" {
		return &RunFailure{cause: ErrSpendDenied}
	}
	if code == "RESOURCE_LIMIT" {
		return &RunFailure{cause: ErrResourceLimit}
	}
	return &RunFailure{cause: llm.NormalizeError(code)}
}

func (l agentLoop) retryContext(run Run) (bool, error) {
	if run.Checkpoint.ContextRetries >= 1 {
		return false, nil
	}
	cp := run.Checkpoint
	cp.ContextRetries++
	cp.Step++
	cp.Result = nil
	cp.ExchangeID = ""
	cp.Phase = ModelPhase
	return true, l.options.Store.Checkpoint(l.work, l.claim, run.Version, cp)
}
