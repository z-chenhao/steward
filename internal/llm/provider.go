package llm

import (
	"context"
	"errors"
)

// These internal contracts are owned by the model control plane and future
// Agent Loop. Provider wire messages and credentials do not enter the loop.
type ProviderError string

func (e ProviderError) Error() string { return string(e) }

const (
	AuthRequired            ProviderError = "AUTH_REQUIRED"
	ModelNotFound           ProviderError = "MODEL_NOT_FOUND"
	ModelIncompatible       ProviderError = "MODEL_INCOMPATIBLE"
	RateLimited             ProviderError = "RATE_LIMITED"
	ContextLengthExceeded   ProviderError = "CONTEXT_LENGTH_EXCEEDED"
	ProviderUnavailable     ProviderError = "PROVIDER_UNAVAILABLE"
	GenerationCancelled     ProviderError = "GENERATION_CANCELLED"
	InvalidProviderResponse ProviderError = "INVALID_PROVIDER_RESPONSE"
)

func NormalizeError(code string) error {
	switch ProviderError(code) {
	case AuthRequired, ModelNotFound, ModelIncompatible, RateLimited, ContextLengthExceeded, ProviderUnavailable, GenerationCancelled, InvalidProviderResponse:
		return ProviderError(code)
	default:
		return InvalidProviderResponse
	}
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Memory     *MemoryText    `json:"memory_text,omitempty"`
	Source     *ContextSource `json:"source,omitempty"`
	ImageRefs  []string       `json:"image_refs,omitempty"`
	Role       string         `json:"role"`
	Origin     string         `json:"origin,omitempty"`
	Text       string         `json:"text"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
}

// ContextSource is Host provenance retained through context selection and replay.
// Providers receive the rendered text; this metadata never grants authority.
type ContextSource struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema string `json:"input_schema"`
}

type GenerationRequest struct {
	ResolveMemory  func(context.Context, MemoryText) (string, error)     `json:"-"`
	ToolGeneration *ToolGeneration                                       `json:"tool_generation,omitempty"`
	ResolveImage   func(context.Context, string) (string, []byte, error) `json:"-"`
	// DispatchGuard is a trusted Host lease held only while writing the request
	// frame. Revocation and dispatch serialize; it never crosses the wire/hash.
	DispatchGuard    func(context.Context) (func(), error) `json:"-"`
	GenerationID     string                                `json:"generation_id"`
	OutputLimitBytes int                                   `json:"output_limit_bytes,omitempty"`
	RunID            string                                `json:"run_id"`
	SessionID        string                                `json:"session_id"`
	Snapshot         Snapshot                              `json:"model_snapshot"`
	Messages         []Message                             `json:"messages"`
	Tools            []ToolSpec                            `json:"tools,omitempty"`
	WebSearch        *WebSearchRequest                     `json:"web_search,omitempty"`

	// AuthEpoch is captured by the Host resolver and passed only to final
	// persistence. It is not part of the frozen snapshot or Bridge protocol.
	AuthEpoch int64 `json:"-"`
	// OwnerToken is installed by the Host RunStore for fenced acceptance. It
	// never crosses the Provider wire or appears in a frozen request hash.
	OwnerToken string `json:"-"`
	// Finalize serializes the usage commit with acknowledged cancellation.
	// The Host installs it per registration; it never crosses the Bridge wire.
	Finalize func(commit func(cancelled bool) error) error `json:"-"`
}

type TokenUsage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// WebSearch is a private hosted-tool request, never a replacement model profile.
type WebSearchRequest struct {
	InvocationID  string   `json:"invocation_id"`
	Query         string   `json:"query"`
	DomainFilter  []string `json:"domain_filter,omitempty"`
	RecencyFilter string   `json:"recency_filter,omitempty"`
	NumResults    int      `json:"num_results,omitempty"`
}

type WebSource struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

type WebSearchResult struct {
	Sources []WebSource `json:"sources"`
}

type GenerationEvent struct {
	WebSearch    *WebSearchResult `json:"web_search,omitempty"`
	Usage        *TokenUsage      `json:"usage,omitempty"`
	GenerationID string           `json:"generation_id"`
	Sequence     int64            `json:"sequence"`
	Kind         string           `json:"kind"`
	Text         string           `json:"text,omitempty"`
	ToolCall     *ToolCall        `json:"tool_call,omitempty"`
	ErrorCode    string           `json:"error_code,omitempty"`
}

type GenerationResult struct {
	WebSearch *WebSearchResult `json:"web_search,omitempty"`
	Usage     *TokenUsage      `json:"usage,omitempty"`
	Text      string           `json:"text"`
	ToolCalls []ToolCall       `json:"tool_calls"`
	Clipped   bool             `json:"clipped"`
}

type LLMProvider interface {
	Generate(context.Context, GenerationRequest, func(GenerationEvent)) (GenerationResult, error)
	Continue(context.Context, GenerationRequest, func(GenerationEvent)) (GenerationResult, error)
	Cancel(context.Context, string) error
	Dispose(context.Context, string) error
}

type AuthEvent struct {
	Kind       string   `json:"kind"`
	URL        string   `json:"url,omitempty"`
	DeviceCode string   `json:"device_code,omitempty"`
	Choices    []string `json:"choices,omitempty"`
}

type AuthState struct {
	Status string      `json:"status"`
	Events []AuthEvent `json:"events"`
}

type ProviderAuth interface {
	Start(context.Context) (AuthState, error)
	ContinueAuth(context.Context, string) (AuthState, error)
	Refresh(context.Context) (AuthState, error)
	Logout(context.Context) error
	Status(context.Context) (AuthState, error)
}

type CatalogModel struct {
	ModelID         string `json:"model_id"`
	ContextWindow   int64  `json:"context_window"`
	MaxOutputTokens int64  `json:"max_output_tokens"`
	ImageInput      bool   `json:"image_input,omitempty"`
}

type ModelCatalog interface {
	List(context.Context) ([]CatalogModel, error)
	RefreshCatalog(context.Context) ([]CatalogModel, error)
}

// A Probe records observed mandatory behaviors, not catalog claims.
type ProbeResult struct {
	Streaming    bool `json:"streaming"`
	ToolCalls    bool `json:"tool_calls"`
	Continuation bool `json:"continuation"`
	Cancellation bool `json:"cancellation"`
	Clipping     bool `json:"clipping"`
	ImageInput   bool `json:"image_input,omitempty"`
}

func (p ProbeResult) Validate() error {
	if !p.Streaming || !p.ToolCalls || !p.Continuation || !p.Cancellation || !p.Clipping {
		return ModelIncompatible
	}
	return nil
}

// Optional modalities require observed proof as well as a profile declaration.
// Older text-only records remain valid; an old record cannot admit image input.
func (p ProbeResult) ValidateCapabilities(c Capabilities) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if c.ImageInput && !p.ImageInput {
		return ModelIncompatible
	}
	return nil
}

type ModelProbe interface {
	Run(context.Context, Profile) (ProbeResult, error)
}

//lint:ignore ST1005 Preserve the exported sentinel diagnostic text.
var ErrNoBridge = errors.New("Provider Bridge is not configured; use explicit test injection for M0 fixtures")

// ToolGeneration identifies a fenced auxiliary model exchange.
type ToolGeneration struct {
	InvocationID string `json:"invocation_id"`
	CallKey      string `json:"call_key"`
	Kind         string `json:"kind"`
}
