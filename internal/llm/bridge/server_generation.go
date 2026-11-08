package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
)

type generationRequest struct {
	llm.GenerationRequest
	Connection llm.Connection `json:"connection"`
	Observe    bool           `json:"observe"`
	UIPreview  bool           `json:"ui_preview"` // Optional v1 event opt-in; absent for older clients.
	Images     map[string]struct {
		MediaType      string `json:"media_type"`
		Data           string `json:"data"`
		OriginalWidth  int    `json:"original_width"`
		OriginalHeight int    `json:"original_height"`
		PreviewWidth   int    `json:"preview_width"`
		PreviewHeight  int    `json:"preview_height"`
	} `json:"images"`
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return provider.ErrIncompatible
	}
	return nil
}
func endpointHash(endpoint string) string {
	raw, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}

func (s *Server) generate(ctx context.Context, r generationRequest, j *job) {
	started := time.Now()
	seq := int64(0)
	event := func(kind string, data map[string]any) {
		if j.suppressed.Load() {
			return
		}
		seq++
		data["generation_id"] = r.GenerationID
		data["sequence"] = seq
		data["kind"] = kind
		s.send(map[string]any{"jsonrpc": "2.0", "method": "generation.event", "params": data})
	}
	fail := func(err error) { event("error", map[string]any{"error_code": provider.ErrorCode(err)}) }
	snapshot := r.Snapshot
	account := snapshot.ProviderKind == "openai_account"
	if r.SessionID != r.RunID || r.RunID == "" || snapshot.SnapshotID == "" || snapshot.MaxOutputTokens < 1 || snapshot.ContextWindow < 1 || snapshot.ContextWindow > 1048576 || snapshot.MaxOutputTokens > snapshot.ContextWindow || snapshot.ReasoningEffort != "" || snapshot.GenerationDefaults.Temperature != nil || snapshot.GenerationDefaults.TopP != nil || snapshot.Compatibility.ToolResultFormat != "openai" || snapshot.Compatibility.DeveloperRole || snapshot.Compatibility.ReasoningEffort || snapshot.Compatibility.ParallelToolCalls || snapshot.Compatibility.StreamUsage {
		fail(provider.ErrIncompatible)
		return
	}
	if err := validateEndpoint(r.Connection.Endpoint); err != nil {
		fail(err)
		return
	}
	if account {
		if snapshot.WireAPI != "openai_responses" || snapshot.EndpointHash != "" || r.Connection.Endpoint != apiURL || r.Connection.AccessToken == "" || r.Connection.APIKey != "" {
			fail(provider.ErrIncompatible)
			return
		}
		models, err := s.catalog(ctx, r.Connection)
		if err != nil {
			fail(err)
			return
		}
		found := false
		for _, m := range models {
			found = found || m.ModelID == snapshot.ModelID
		}
		if !found {
			fail(&provider.Error{Code: "MODEL_NOT_FOUND"})
			return
		}
	} else if snapshot.ProviderKind != "openai_compatible" || snapshot.WireAPI != "openai_completions" || snapshot.EndpointHash != endpointHash(r.Connection.Endpoint) {
		fail(provider.ErrIncompatible)
		return
	}
	m := provider.Model{ID: snapshot.ModelID, API: provider.OpenAICompletions, Provider: "steward", BaseURL: r.Connection.Endpoint, MaxTokens: snapshot.MaxOutputTokens, ContextWindow: snapshot.ContextWindow, Compat: map[string]any{"supportsStore": false, "supportsDeveloperRole": false, "supportsUsageInStreaming": false, "supportsStrictMode": false, "maxTokensField": "max_tokens", "sendSessionAffinityHeaders": true}}
	if account {
		m.API = provider.OpenAIResponses
		m.Compat["supportsMaxOutputTokens"] = false
	}
	in := provider.Context{}
	if r.WebSearch != nil {
		if !account || r.WebSearch.InvocationID == "" || strings.TrimSpace(r.WebSearch.Query) == "" || len(r.WebSearch.Query) > 4096 || len(r.Tools) != 0 || len(r.Messages) != 2 || r.Messages[1].Text != r.WebSearch.Query {
			fail(provider.ErrIncompatible)
			return
		}
		in.WebSearch = true
		if len(r.WebSearch.DomainFilter) > 20 {
			fail(provider.ErrIncompatible)
			return
		}
		for _, domain := range r.WebSearch.DomainFilter {
			if strings.HasPrefix(domain, "-") {
				continue
			}
			if len(domain) > 253 || strings.ContainsAny(domain, "/\\?#@% :") {
				fail(provider.ErrIncompatible)
				return
			}
			in.WebSearchDomains = append(in.WebSearchDomains, domain)
		}
	}
	pending := map[string]string{}
	systemSeen := false
	for _, message := range r.Messages {
		if message.Role == "system" {
			if len(in.Messages) > 0 || systemSeen {
				fail(provider.ErrInvalidResponse)
				return
			}
			systemSeen = true
			in.SystemPrompt = message.Text
			continue
		}
		msg := provider.Message{Role: message.Role, Content: []provider.Content{provider.Text(message.Text)}}
		if message.Role == "assistant" {
			pending = map[string]string{}
			for _, call := range message.ToolCalls {
				var args map[string]any
				if json.Unmarshal([]byte(call.Arguments), &args) != nil || args == nil || pending[call.ID] != "" {
					fail(provider.ErrInvalidResponse)
					return
				}
				namespace := ""
				if account {
					namespace = "steward"
				}
				msg.Content = append(msg.Content, provider.Content{Type: "toolCall", ID: call.ID, Name: call.Name, Namespace: namespace, Arguments: args})
				pending[call.ID] = call.Name
			}
		}
		if message.Role == "tool" {
			name := pending[message.ToolCallID]
			if name == "" {
				fail(provider.ErrInvalidResponse)
				return
			}
			msg.Role = "toolResult"
			msg.ToolCallID = message.ToolCallID
			msg.ToolName = name
			delete(pending, message.ToolCallID)
		}
		for _, ref := range message.ImageRefs {
			image, ok := r.Images[ref]
			if !ok {
				continue
			}
			if image.MediaType != "image/jpeg" || len(image.Data) > 65536 {
				fail(provider.ErrInvalidResponse)
				return
			}
			if _, err := base64.StdEncoding.DecodeString(image.Data); err != nil {
				fail(provider.ErrInvalidResponse)
				return
			}
			msg.Content = append(msg.Content, provider.Text(imageNotice(image.OriginalWidth, image.OriginalHeight, image.PreviewWidth, image.PreviewHeight)), provider.Content{Type: "image", Data: image.Data, MIMEType: "image/jpeg"})
		}
		in.Messages = append(in.Messages, msg)
	}
	for _, t := range r.Tools {
		if !json.Valid([]byte(t.InputSchema)) {
			fail(provider.ErrInvalidResponse)
			return
		}
		namespace := ""
		if account {
			namespace = "steward"
		}
		in.Tools = append(in.Tools, provider.Tool{Name: t.Name, Description: t.Description, Parameters: json.RawMessage(t.InputSchema), Namespace: namespace})
	}
	var expired atomic.Bool
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline := time.AfterFunc(180*time.Second, func() {
		expired.Store(true)
		s.observe(r, provider.Observation{Phase: "generation_deadline", Attempt: "generation", At: time.Now()})
		cancel()
	})
	defer deadline.Stop()
	o := provider.Options{HTTPClient: s.client(), APIKey: r.Connection.APIKey, AccessToken: r.Connection.AccessToken, MaxTokens: snapshot.MaxOutputTokens, SessionID: r.SessionID, HeaderTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxRetries: 2, MaxResponseBytes: 4 << 20, MaxEvents: 64 << 10, Observe: func(obs provider.Observation) {
		if obs.Phase == "header_timeout" || obs.Phase == "stream_idle_timeout" {
			expired.Store(true)
		}
		s.observe(r, obs)
	}}
	if r.WebSearch != nil {
		o.MaxRetries = 0
	}
	toolCount, outputBytes := 0, 0
	type pendingPreview struct {
		text strings.Builder
		sent time.Time
	}
	previews := map[string]*pendingPreview{}
	flushPreview := func(id string) {
		if p := previews[id]; p != nil && p.text.Len() > 0 {
			event("ui_delta", map[string]any{"tool_call": llm.ToolCall{ID: id, Name: "builtin__intelligent_ui_show", Arguments: p.text.String()}})
			p.text.Reset()
			p.sent = time.Now()
		}
	}
	// Provider chunk boundaries do not change the accepted result. Coalesce
	// small text fragments so a bounded long report cannot exhaust the Host's
	// event-count safety limit before its byte budget. Keep first text prompt.
	var text strings.Builder
	var textSent time.Time
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		event("text_delta", map[string]any{"text": text.String()})
		text.Reset()
		textSent = time.Now()
	}
	invalid := false
	result, err := (&provider.Client{}).Stream(work, m, in, o, func(e provider.Event) {
		switch e.Type {
		case "text_delta":
			outputBytes += len(e.Delta)
			encoded, _ := json.Marshal(e.Delta)
			if outputBytes > 1<<20 || len(encoded) > (1<<20)-2048 {
				invalid = true
				cancel()
				return
			}
			if text.Len()+len(e.Delta) > 16<<10 {
				flushText()
			}
			text.WriteString(e.Delta)
			if textSent.IsZero() || text.Len() >= 512 || time.Since(textSent) >= 80*time.Millisecond {
				flushText()
			}
		case "toolcall_delta":
			flushText()
			// Only presentation arguments have a browser-preview consumer. Other
			// tool parameters, source code and reasoning stay on the execution path.
			if r.UIPreview && e.Content != nil && e.Content.Name == "builtin__intelligent_ui_show" && e.Content.ID != "" {
				p := previews[e.Content.ID]
				if p == nil && len(previews) < 32 {
					p = &pendingPreview{}
					previews[e.Content.ID] = p
				}
				if p != nil {
					if p.text.Len()+len(e.Delta) > 128<<10 {
						invalid = true
						cancel()
						return
					}
					p.text.WriteString(e.Delta)
					if p.sent.IsZero() || p.text.Len() >= 512 || time.Since(p.sent) >= 40*time.Millisecond {
						flushPreview(e.Content.ID)
					}
				}
			}
		case "toolcall_end":
			flushText()
			if e.Content == nil {
				invalid = true
				cancel()
				return
			}
			flushPreview(e.Content.ID)
			toolCount++
			raw, _ := json.Marshal(e.Content.Arguments)
			if toolCount > 32 || len(raw) > 65536 {
				invalid = true
				cancel()
				return
			}
			event("tool_call", map[string]any{"tool_call": llm.ToolCall{ID: e.Content.ID, Name: e.Content.Name, Arguments: string(raw)}})
		}
	})
	flushText()
	if invalid {
		fail(provider.ErrInvalidResponse)
		return
	}
	if expired.Load() {
		fail(&provider.Error{Code: "PROVIDER_UNAVAILABLE"})
		return
	}
	if err != nil {
		fail(err)
		return
	}
	// The Host result contract cannot represent truncation. Preserve the SDK's
	// partial-output support while refusing to accept it as completed work here.
	if result.StopReason == "length" {
		fail(provider.ErrInvalidResponse)
		return
	}
	data := map[string]any{"latency_ms": time.Since(started).Milliseconds()}
	if r.WebSearch != nil {
		if !result.WebSearchPerformed {
			fail(provider.ErrInvalidResponse)
			return
		}
		search := llm.WebSearchResult{Sources: []llm.WebSource{}}
		for _, source := range result.WebSources {
			search.Sources = append(search.Sources, llm.WebSource{URL: source.URL, Title: source.Title})
		}
		data["web_search"] = search
	}
	if result.Usage.Known {
		data["usage"] = llm.TokenUsage{InputTokens: result.Usage.Input, OutputTokens: result.Usage.Output, CacheReadTokens: result.Usage.CacheRead, CacheWriteTokens: result.Usage.CacheWrite}
	}
	event("completed", data)
}

func (s *Server) catalog(ctx context.Context, c llm.Connection) ([]llm.CatalogModel, error) {
	if validateEndpoint(c.Endpoint) != nil {
		return nil, provider.ErrIncompatible
	}
	if c.AccessToken != "" && (c.Endpoint != apiURL || c.APIKey != "") {
		return nil, provider.ErrIncompatible
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Endpoint, "/")+"/models", nil)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	client := *s.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, &provider.Error{Code: "PROVIDER_UNAVAILABLE"}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, &provider.Error{Code: mapStatus(response.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, provider.ErrInvalidResponse
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Slug            string   `json:"slug"`
			Visibility      string   `json:"visibility"`
			ContextWindow   int64    `json:"context_window"`
			MaxOutputTokens int64    `json:"max_output_tokens"`
			InputModalities []string `json:"input_modalities"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &body) != nil {
		return nil, provider.ErrInvalidResponse
	}
	out := []llm.CatalogModel{}
	if c.AccessToken != "" {
		if body.Models == nil || len(body.Models) > 4096 {
			return nil, provider.ErrInvalidResponse
		}
		seen := map[string]bool{}
		knownModels := make(map[string]provider.Model)
		for _, model := range provider.Models("openai") {
			if model.API == provider.OpenAIResponses {
				knownModels[model.ID] = model
			}
		}
		for _, m := range body.Models {
			if m.Visibility != "list" {
				continue
			}
			if m.Slug == "" || len(m.Slug) > 512 || seen[m.Slug] {
				return nil, provider.ErrInvalidResponse
			}
			seen[m.Slug] = true
			window, max := m.ContextWindow, m.MaxOutputTokens
			image := m.InputModalities == nil || slices.Contains(m.InputModalities, "image")
			// Account catalogs can omit limits and modalities. Resolve those
			// fields from the bundled Pi chat catalog, never the classifier entry.
			if known, ok := knownModels[m.Slug]; ok {
				if window < 1 {
					window = known.ContextWindow
				}
				if max < 1 {
					max = known.MaxTokens
				}
				if m.InputModalities == nil {
					image = slices.Contains(known.Input, "image")
				}
			}
			if window < 1 {
				window = 128000
			}
			if max < 1 {
				max = 16384
			}
			window = min(window, 1048576)
			out = append(out, llm.CatalogModel{ModelID: m.Slug, ContextWindow: window, MaxOutputTokens: min(max, window), ImageInput: image})
		}
	} else {
		if body.Data == nil || len(body.Data) > 4096 {
			return nil, provider.ErrInvalidResponse
		}
		for _, m := range body.Data {
			if m.ID == "" || len(m.ID) > 512 {
				return nil, provider.ErrInvalidResponse
			}
			out = append(out, llm.CatalogModel{ModelID: m.ID})
		}
	}
	return out, nil
}
func mapStatus(status int) string {
	if status == 401 || status == 403 {
		return "AUTH_REQUIRED"
	}
	if status == 404 {
		return "MODEL_NOT_FOUND"
	}
	if status == 429 {
		return "RATE_LIMITED"
	}
	if status >= 500 {
		return "PROVIDER_UNAVAILABLE"
	}
	return "MODEL_INCOMPATIBLE"
}
