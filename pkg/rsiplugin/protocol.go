// Package rsiplugin is the experimental, standard-library-only Go contract for
// Steward-owned feature plugins. The SDK never grants Host or OS authority.
package rsiplugin

import (
	"context"
	"encoding/json"
	"errors"
)

const Protocol = "steward-rsi-plugins/0.1"
const MaxFrameBytes = 1 << 20

type Stage string

const (
	PrepareRun          Stage = "prepare_run"
	InputAccepted       Stage = "input_accepted"
	BuildContext        Stage = "build_context"
	Recall              Stage = "recall"
	CompactContext      Stage = "compact_context"
	SelectCapabilities  Stage = "select_capabilities"
	BeforeModelRequest  Stage = "before_model_request"
	ModelStream         Stage = "model_stream"
	ModelResult         Stage = "model_result"
	BeforeToolCall      Stage = "before_tool_call"
	ToolResult          Stage = "tool_result"
	ProjectToolResult   Stage = "project_tool_result"
	PrepareDelegation   Stage = "prepare_delegation"
	PrepareReply        Stage = "prepare_reply"
	SuggestContinuation Stage = "suggest_continuation"
	SuggestRecovery     Stage = "suggest_recovery"
	RunWaiting          Stage = "run_waiting"
	RunResumed          Stage = "run_resumed"
	RunSettled          Stage = "run_settled"
)

func (s Stage) Valid() bool {
	switch s {
	case PrepareRun, InputAccepted, BuildContext, Recall, CompactContext, SelectCapabilities,
		BeforeModelRequest, ModelStream, ModelResult, BeforeToolCall, ToolResult,
		ProjectToolResult, PrepareDelegation, PrepareReply, SuggestContinuation,
		SuggestRecovery, RunWaiting, RunResumed, RunSettled:
		return true
	}
	return false
}

type Runtime struct {
	Entry     string `json:"entry"`
	TimeoutMS int    `json:"timeout_ms"`
}
type File struct {
	Path       string `json:"path"`
	Digest     string `json:"digest"`
	Executable bool   `json:"executable,omitempty"`
}
type Service struct {
	Contract     string          `json:"contract"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}
type Requirement struct {
	Binding  string `json:"binding"`
	Contract string `json:"contract"`
}
type Hook struct {
	Stage Stage `json:"stage"`
}
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Contract    string `json:"contract"`
}
type Resource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
}
type StateSpec struct {
	Version int             `json:"version"`
	Schema  json.RawMessage `json:"schema"`
	Initial json.RawMessage `json:"initial"`
}
type Manifest struct {
	Protocol     string          `json:"protocol"`
	PluginID     string          `json:"plugin_id"`
	Version      string          `json:"version"`
	FeatureIDs   []string        `json:"feature_ids,omitempty"`
	Runtime      Runtime         `json:"runtime"`
	Files        []File          `json:"files"`
	Provides     []Service       `json:"provides,omitempty"`
	Requires     []Requirement   `json:"requires,omitempty"`
	Hooks        []Hook          `json:"hooks,omitempty"`
	Tools        []Tool          `json:"tools,omitempty"`
	Resources    []Resource      `json:"resources,omitempty"`
	ConfigSchema json.RawMessage `json:"config_schema"`
	State        StateSpec       `json:"state"`
	Requests     []string        `json:"requests,omitempty"`
}
type Dependency struct {
	Binding    string `json:"binding"`
	InstanceID string `json:"instance_id"`
	Contract   string `json:"contract"`
}
type Binding struct {
	InstanceID     string          `json:"instance_id"`
	PackageDigest  string          `json:"package_digest"`
	Config         json.RawMessage `json:"config"`
	Dependencies   []Dependency    `json:"dependencies,omitempty"`
	StateNamespace string          `json:"state_namespace"`
	StateVersion   int             `json:"state_version"`
}
type Graph struct {
	Protocol string    `json:"protocol"`
	Bindings []Binding `json:"bindings"`
}
type Execution struct {
	RunID         string `json:"run_id"`
	GraphRevision string `json:"graph_revision"`
	Role          string `json:"role,omitempty"`
	Step          int64  `json:"step"`
}
type Message struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	SourceRef string `json:"source_ref,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type HookInput struct {
	Stage     Stage     `json:"stage"`
	Execution Execution `json:"execution"`
	Messages  []Message `json:"messages,omitempty"`
	ToolNames []string  `json:"tool_names,omitempty"`
	ToolCall  *ToolCall `json:"tool_call,omitempty"`
	Text      string    `json:"text,omitempty"`
	Reference string    `json:"reference,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Status    string    `json:"status,omitempty"`
}
type HookOutput struct {
	Reject            bool            `json:"reject,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	AdditionalContext []Message       `json:"additional_context,omitempty"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	Text              *string         `json:"text,omitempty"`
	ToolNames         *[]string       `json:"tool_names,omitempty"`
}

// Validate constrains suggestions to their declared seam. Observers cannot
// veto committed facts; changes are still subject to the Host's domain checks.
func (o HookOutput) Validate(stage Stage) error {
	if !stage.Valid() || len(o.Reason) > 2048 || len(o.AdditionalContext) > 16 || len(o.Arguments) > 64<<10 || o.Text != nil && len(*o.Text) > 64<<10 {
		return errors.New("invalid hook output")
	}
	if o.Reject && stage != BeforeModelRequest && stage != BeforeToolCall && stage != PrepareReply {
		return errors.New("observer cannot reject")
	}
	if len(o.AdditionalContext) > 0 && stage != BuildContext && stage != Recall && stage != BeforeModelRequest {
		return errors.New("context outside preparation")
	}
	for _, m := range o.AdditionalContext {
		if m.Kind != "plugin_context" || m.Protected || len(m.Text) > 8192 || len(m.SourceRef) > 256 {
			return errors.New("invalid additional context")
		}
	}
	if len(o.Arguments) > 0 && (stage != BeforeToolCall || !json.Valid(o.Arguments)) {
		return errors.New("arguments outside tool preparation")
	}
	if o.Text != nil && stage != ProjectToolResult && stage != PrepareReply {
		return errors.New("text outside projection")
	}
	if o.ToolNames != nil && (stage != SelectCapabilities || len(*o.ToolNames) > 1024) {
		return errors.New("tool selection outside preparation")
	}
	return nil
}

type State struct {
	Version int64           `json:"version"`
	Value   json.RawMessage `json:"value"`
}
type PrepareInput struct {
	Protocol   string          `json:"protocol"`
	InstanceID string          `json:"instance_id"`
	Config     json.RawMessage `json:"config"`
}
type Call struct {
	OperationID string          `json:"operation_id"`
	Execution   Execution       `json:"execution"`
	Contract    string          `json:"contract"`
	Input       json.RawMessage `json:"input"`
	State       State           `json:"state"`
	Inputs      []Input         `json:"inputs,omitempty"`
}
type HookCall struct {
	OperationID string    `json:"operation_id"`
	Input       HookInput `json:"input"`
	State       State     `json:"state"`
}
type Result struct {
	Output     json.RawMessage  `json:"output"`
	OutputData *Input           `json:"output_data,omitempty"`
	State      *json.RawMessage `json:"state,omitempty"`
}
type HookResult struct {
	Output HookOutput       `json:"output"`
	State  *json.RawMessage `json:"state,omitempty"`
}
type Description struct {
	Protocol  string   `json:"protocol"`
	PluginID  string   `json:"plugin_id"`
	Version   string   `json:"version"`
	Contracts []string `json:"contracts"`
	Hooks     []Stage  `json:"hooks"`
}

// Plugin is implemented outside Host. Persistent state is supplied and accepted
// by Host, not recovered from a plugin process's private memory or files.
type Plugin interface {
	Describe() Description
	Prepare(context.Context, PrepareInput) error
	Call(context.Context, Call) (Result, error)
	Hook(context.Context, HookCall) (HookResult, error)
	Drain(context.Context) error
	Dispose(context.Context) error
}
