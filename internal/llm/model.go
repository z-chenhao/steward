// Package llm owns model metadata and the private provider control contracts.
// Storing metadata alone never enables a provider.
package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
)

var (
	ErrConflict   = errors.New("metadata version conflict")
	ErrNotFound   = errors.New("metadata not found")
	ErrStorage    = errors.New("model metadata persistence failed")
	ErrUnverified = errors.New("model profile requires a successful Probe")
)

type GenerationDefaults struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
}

type Compatibility struct {
	DeveloperRole     bool   `json:"developer_role"`
	ReasoningEffort   bool   `json:"reasoning_effort"`
	ParallelToolCalls bool   `json:"parallel_tool_calls"`
	StreamUsage       bool   `json:"stream_usage"`
	ToolResultFormat  string `json:"tool_result_format"`
}

type Capabilities struct {
	TextInput        bool     `json:"text_input"`
	ImageInput       bool     `json:"image_input"`
	AudioInput       bool     `json:"audio_input"`
	ToolCalls        bool     `json:"tool_calls"`
	Streaming        bool     `json:"streaming"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
}

// ProfileInput is the entire metadata-only CLI input. Status, verification,
// credentials, IDs, versions, and timestamps are always host owned.
type ProfileInput struct {
	Name               string             `json:"name"`
	ProviderKind       string             `json:"provider_kind"`
	Endpoint           string             `json:"endpoint,omitempty"`
	WireAPI            string             `json:"wire_api"`
	ModelID            string             `json:"model_id"`
	AuthConnectionID   string             `json:"auth_connection_id,omitempty"`
	ReasoningEffort    string             `json:"reasoning_effort,omitempty"`
	ContextWindow      int64              `json:"context_window"`
	MaxOutputTokens    int64              `json:"max_output_tokens"`
	GenerationDefaults GenerationDefaults `json:"generation_defaults"`
	Compatibility      Compatibility      `json:"compatibility"`
	Capabilities       Capabilities       `json:"capabilities"`
}

type Profile struct {
	ProfileInput
	ProfileID  string     `json:"profile_id"`
	Status     string     `json:"status"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Version    int64      `json:"version"`
}

// Snapshot intentionally has neither endpoint nor CredentialRef. It is never
// updated when a profile or its referenced authentication connection changes.
type Snapshot struct {
	SnapshotID         string             `json:"snapshot_id"`
	ProfileID          string             `json:"profile_id"`
	ProfileVersion     int64              `json:"profile_version"`
	ProviderKind       string             `json:"provider_kind"`
	EndpointHash       string             `json:"endpoint_hash,omitempty"`
	WireAPI            string             `json:"wire_api"`
	ModelID            string             `json:"model_id"`
	AuthConnectionID   string             `json:"auth_connection_id,omitempty"`
	ReasoningEffort    string             `json:"reasoning_effort,omitempty"`
	ContextWindow      int64              `json:"context_window"`
	MaxOutputTokens    int64              `json:"max_output_tokens"`
	GenerationDefaults GenerationDefaults `json:"generation_defaults"`
	Compatibility      Compatibility      `json:"compatibility"`
	Capabilities       Capabilities       `json:"capabilities"`
	CreatedAt          time.Time          `json:"created_at"`
}

func (p ProfileInput) Validate() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.ModelID) == "" {
		return errors.New("profile name and model_id are required")
	}
	if p.WireAPI != "openai_completions" && p.WireAPI != "openai_responses" {
		return errors.New("invalid wire_api")
	}
	if err := p.validateProviderIdentity(); err != nil {
		return err
	}
	if p.ContextWindow <= 0 || p.MaxOutputTokens <= 0 || p.MaxOutputTokens > p.ContextWindow {
		return errors.New("token limits must be positive and max_output_tokens must not exceed context_window")
	}
	if !p.Capabilities.TextInput || (p.Compatibility.ToolResultFormat != "openai" && p.Compatibility.ToolResultFormat != "provider_specific") {
		return errors.New("invalid capabilities or tool_result_format")
	}
	for _, v := range p.Capabilities.ReasoningEfforts {
		if strings.TrimSpace(v) == "" {
			return errors.New("empty reasoning effort")
		}
	}
	if p.ReasoningEffort != "" {
		found := false
		for _, v := range p.Capabilities.ReasoningEfforts {
			found = found || v == p.ReasoningEffort
		}
		if !p.Compatibility.ReasoningEffort || !found {
			return errors.New("unsupported reasoning_effort")
		}
	}
	for _, v := range []struct {
		value *float64
		max   float64
	}{{p.GenerationDefaults.Temperature, 2}, {p.GenerationDefaults.TopP, 1}} {
		if v.value != nil && (math.IsNaN(*v.value) || math.IsInf(*v.value, 0) || *v.value < 0 || *v.value > v.max) {
			return errors.New("invalid generation defaults")
		}
	}
	return nil
}

// ValidateActivation applies current execution policy without changing the
// persisted metadata format. Older candidates remain readable for diagnostics
// and can be edited before testing/selecting them under today's Host limits.
func (p ProfileInput) ValidateActivation() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ContextWindow > 1048576 {
		return errors.New("context window exceeds Host safety bound (1048576)")
	}
	return nil
}

func validEndpoint(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(value, "#\\\r\n\t ")
}

// CanonicalJSON is the repository's restricted JSON encoding for typed model
// metadata: sorted object keys, compact UTF-8, normalized finite numbers. Inputs
// have no arbitrary JSON, large integers, duplicate keys, or untyped extensions.
func CanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("invalid model JSON")
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, errors.New("invalid model JSON")
	}
	return json.Marshal(normalizeZeros(normalized))
}

func normalizeZeros(value any) any {
	switch v := value.(type) {
	case float64:
		if v == 0 {
			return float64(0)
		}
	case map[string]any:
		for key, child := range v {
			v[key] = normalizeZeros(child)
		}
	case []any:
		for i, child := range v {
			v[i] = normalizeZeros(child)
		}
	}
	return value
}

func EndpointHash(endpoint string) (string, error) {
	if endpoint == "" {
		return "", nil
	}
	if !validEndpoint(endpoint) {
		return "", errors.New("invalid endpoint")
	}
	data, err := CanonicalJSON(map[string]string{"endpoint": endpoint})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateIdentity checks supplied identity/transport authority before defaults
// are resolved. It is not sufficient to persist or activate a profile.
func (p ProfileInput) ValidateIdentity() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.ModelID) == "" {
		return errors.New("profile name and model_id are required")
	}
	return p.validateProviderIdentity()
}
func (p ProfileInput) validateProviderIdentity() error {
	switch p.ProviderKind {
	case "openai_compatible":
		if !validEndpoint(p.Endpoint) {
			return errors.New("endpoint must be an HTTP(S) URL without credentials, query, or fragment")
		}
	case "openai_account":
		if p.Endpoint != "" || p.AuthConnectionID == "" || p.WireAPI != "openai_responses" {
			return errors.New("openai_account requires an auth connection, responses API, and no custom endpoint")
		}
	default:
		return errors.New("invalid provider_kind")
	}
	return nil
}
