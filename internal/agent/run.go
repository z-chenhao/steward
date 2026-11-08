package agent

import (
	"context"
	"errors"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
)

var ErrClaimLost = errors.New("CLAIM_LOST")
var ErrConflict = errors.New("run version conflict")
var ErrStorage = errors.New("run storage unavailable")
var ErrSpendDenied = errors.New("SPEND_AUTHORIZATION_DENIED")
var ErrResourceLimit = errors.New("RESOURCE_LIMIT")
var ErrBusy = errors.New("run is owned by another executor")
var ErrNotFound = errors.New("run not found")
var ErrSetup = errors.New("setup_required: select an available model before chat")

// RunClaim deliberately exposes no lease, owner, epoch or database internals.
type RunClaim interface{ RunID() string }

// SystemSection is Host-owned frozen provenance, not another model message.
type SystemSection struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	// ChildText excludes Root-only instructions from a Child's frozen rules.
	ChildText string `json:"child_text,omitempty"`
	// Progress opts the Root's execution section into Host stage projection.
	Progress bool `json:"progress,omitempty"`
}

type AgentMessage struct {
	SystemSections []SystemSection            `json:"system_sections,omitempty"`
	Source         *llm.ContextSource         `json:"source,omitempty"`
	MessageID      string                     `json:"message_id,omitempty"`
	CustomType     string                     `json:"custom_type,omitempty"`
	Origin         string                     `json:"origin,omitempty"`
	Details        *task.UpdateDetails        `json:"details,omitempty"`
	CreatedAt      *time.Time                 `json:"created_at,omitempty"`
	Type           string                     `json:"type"`
	Content        []conversation.ContentPart `json:"content"`
	ToolCallID     string                     `json:"tool_call_id,omitempty"`
}

func TextMessage(kind, text string) AgentMessage {
	return AgentMessage{Type: kind, Content: conversation.Text(text)}
}

// ProviderTools is the one model-visible projection. Context budgeting must
// count this wire shape, not Host policy/hash/permission metadata.
func ProviderTools(specs []tool.Spec, loaded ...[]string) []llm.ToolSpec {
	out := []llm.ToolSpec{}
	appendSpec := func(spec tool.Spec) {
		description := spec.Description
		schema := spec.InputSchema
		if spec.ModelInputSchema != "" {
			schema = spec.ModelInputSchema
		}
		if spec.Name == "builtin__skill_list" && len(spec.PromptContext) > 0 {
			description = "List available Skills and their frozen versions. Read a matching Skill with skill view before applying its scoped guidance."
		}
		out = append(out, llm.ToolSpec{Name: spec.Name, Description: description, InputSchema: schema})
	}
	indirect := map[string]tool.Spec{}
	for _, spec := range specs {
		if spec.CodeModeOnly {
			continue
		}
		if spec.Searchable() {
			indirect[spec.Name] = spec
			continue
		}
		if spec.ModelVisible(nil) {
			appendSpec(spec)
		}
	}
	// Keep the original direct declarations and loaded prefix stable as search
	// appends capabilities, matching Pi's active-tool ordering.
	seen := map[string]bool{}
	for _, names := range loaded {
		for _, name := range names {
			if spec, ok := indirect[name]; ok && !seen[name] {
				appendSpec(spec)
				seen[name] = true
			}
		}
	}
	return out
}

type Checkpoint struct {
	SerialInputs         bool                  `json:"serial_inputs,omitempty"`
	RepliedInputSequence int64                 `json:"replied_input_sequence,omitempty"`
	LoadedTools          []string              `json:"loaded_tools,omitempty"`
	PendingTools         []PendingToolBatch    `json:"pending_tools,omitempty"`
	ContextVersion       int                   `json:"context_version,omitempty"`
	WorkingState         *WorkingState         `json:"working_state,omitempty"`
	ContextRetries       int                   `json:"context_retries,omitempty"`
	ContextReady         bool                  `json:"context_ready"`
	Messages             []AgentMessage        `json:"messages"`
	InputSequence        int64                 `json:"input_sequence"`
	Step                 int64                 `json:"step"`
	Phase                Phase                 `json:"phase"`
	ExchangeID           string                `json:"exchange_id,omitempty"`
	Result               *llm.GenerationResult `json:"result,omitempty"`
}
type Run struct {
	ResponsiveTools bool         `json:"-"`
	SpendStatus     string       `json:"-"`
	ID              string       `json:"run_id"`
	AgentID         string       `json:"agent_id"`
	ConversationID  string       `json:"conversation_id"`
	Kind            Role         `json:"kind"`
	Status          RunStatus    `json:"status"`
	WaitReason      string       `json:"wait_reason,omitempty"`
	Version         int64        `json:"version"`
	Snapshot        llm.Snapshot `json:"model_snapshot"`
	Tools           []tool.Spec  `json:"tools"`
	Checkpoint      Checkpoint   `json:"checkpoint"`
}
type Input struct {
	Context  []AgentMessage // Frozen input-scoped UI evidence, absent on other channels.
	Sequence int64
	Message  conversation.Message
}
type Generation struct {
	Compaction *Compaction `json:"-"`
	Waiting    bool
	Denied     bool
	Request    llm.GenerationRequest
	Result     *llm.GenerationResult
	ErrorCode  string
}
type Event struct {
	UIPreview    *ui.Preview `json:"-"`
	PreviewID    string      `json:"-"`
	PreviewKey   string      `json:"-"`
	GenerationID string      `json:"-"` // Correlates live message events; not part of the persisted trace.
	InvocationID string      `json:"invocation_id,omitempty"`
	RunKind      string      `json:"run_kind"`
	TaskID       string      `json:"task_id,omitempty"`
	Kind         string      `json:"kind"`
	RunID        string      `json:"run_id"`
	AgentID      string      `json:"agent_id"`
	Step         int64       `json:"step_index"`
	Sequence     int64       `json:"sequence"`
	OccurredAt   time.Time   `json:"occurred_at"`
	Text         string      `json:"text,omitempty"`
	ToolCallID   string      `json:"tool_call_id,omitempty"`
}
type EventSink func(Event)

// This is the consumer-owned persistence boundary of the reusable loop.
type RunStore interface {
	Load(context.Context, RunClaim) (Run, error)
	Steering(context.Context, RunClaim, int64) ([]Input, error)
	Checkpoint(context.Context, RunClaim, int64, Checkpoint) error
	Generation(context.Context, RunClaim, Run) (Generation, error)
	PrepareReply(context.Context, RunClaim, Run) (bool, error)
	DeliverReply(context.Context, RunClaim, Run) error
	Complete(context.Context, RunClaim, int64) (bool, error)
	Fail(context.Context, RunClaim, int64, string) error
	Wait(context.Context, RunClaim, int64, string) error
}

// These types model persisted runtime states. Their wire strings are frozen.
type Phase string

const (
	ModelPhase  Phase = "model"
	ToolsPhase  Phase = "tools"
	ReplyPhase  Phase = "reply"
	SettlePhase Phase = "settle"
)

type Role string

const (
	MainRole   Role = "main"
	WorkerRole Role = "worker"
)

type RunStatus string

const (
	QueuedRun    RunStatus = "queued"
	RunningRun   RunStatus = "running"
	WaitingRun   RunStatus = "waiting"
	CompletedRun RunStatus = "completed"
	FailedRun    RunStatus = "failed"
	CancelledRun RunStatus = "cancelled"
)
