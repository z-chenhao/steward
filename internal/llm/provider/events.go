package provider

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type pendingTool struct {
	index     int
	arguments strings.Builder
	ended     bool
}
type streamState struct {
	message  AssistantMessage
	emit     func(Event)
	tools    map[int]*pendingTool
	blocks   map[int]int
	terminal bool
	onOutput func(string)
}

func newStreamState(m Model, emit func(Event)) *streamState {
	return &streamState{message: AssistantMessage{Role: "assistant", API: m.API, Provider: m.Provider, Model: m.ID, Content: []Content{}, Timestamp: time.Now().UnixMilli()}, emit: emit, tools: map[int]*pendingTool{}, blocks: map[int]int{}}
}
func (s *streamState) send(e Event) {
	if s.emit != nil {
		s.emit(e)
	}
}
func (s *streamState) text(text string, thinking bool) {
	if text == "" {
		return
	}
	kind := "text"
	if thinking {
		kind = "thinking"
	}
	i := len(s.message.Content) - 1
	if i < 0 || s.message.Content[i].Type != kind {
		s.message.Content = append(s.message.Content, Content{Type: kind})
		i++
		s.send(Event{Type: kind + "_start", Index: i})
	}
	if thinking {
		s.message.Content[i].Thinking += text
	} else {
		s.message.Content[i].Text += text
	}
	if s.onOutput != nil {
		s.onOutput("first_text")
	}
	s.send(Event{Type: kind + "_delta", Index: i, Delta: text})
}
func (s *streamState) tool(key int, id, name, namespace string) *pendingTool {
	p := s.tools[key]
	if p == nil {
		i := len(s.message.Content)
		s.message.Content = append(s.message.Content, Content{Type: "toolCall", ID: id, Name: name, Namespace: namespace})
		p = &pendingTool{index: i}
		s.tools[key] = p
		s.send(Event{Type: "toolcall_start", Index: i})
	}
	c := &s.message.Content[p.index]
	if id != "" {
		c.ID = id
	}
	if name != "" {
		c.Name = name
	}
	if namespace != "" {
		c.Namespace = namespace
	}
	if s.onOutput != nil && (id != "" || name != "") {
		s.onOutput("first_tool_call")
	}
	return p
}
func (s *streamState) args(key int, delta string) {
	p := s.tool(key, "", "", "")
	p.arguments.WriteString(delta)
	if delta != "" && s.onOutput != nil {
		s.onOutput("first_tool_call")
	}
	c := s.message.Content[p.index]
	s.send(Event{Type: "toolcall_delta", Index: p.index, Delta: delta, Content: &c})
}
func (s *streamState) endTool(key int) error {
	p := s.tools[key]
	if p == nil || p.ended {
		return nil
	}
	v := &s.message.Content[p.index]
	if v.ID == "" {
		v.ID = fmt.Sprintf("call_%d_%d", s.message.Timestamp, p.index)
	}
	if v.Name == "" {
		return ErrInvalidResponse
	}
	raw := p.arguments.String()
	if raw == "" && v.Arguments != nil {
		raw = "{}"
	}
	var args map[string]any
	if json.Unmarshal([]byte(raw), &args) != nil || args == nil {
		return ErrInvalidResponse
	}
	v.Arguments = args
	p.ended = true
	copy := *v
	s.send(Event{Type: "toolcall_end", Index: p.index, Content: &copy})
	return nil
}
func (s *streamState) finish() error {
	keys := make([]int, 0, len(s.tools))
	for key := range s.tools {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return s.tools[keys[i]].index < s.tools[keys[j]].index })
	for _, key := range keys {
		if err := s.endTool(key); err != nil {
			return err
		}
	}
	if s.message.StopReason == "" {
		s.message.StopReason = "stop"
	}
	if s.message.StopReason == "error" {
		return ErrInvalidResponse
	}
	for _, v := range s.message.Content {
		if v.Type == "toolCall" && s.message.StopReason == "stop" {
			s.message.StopReason = "toolUse"
		}
	}
	s.message.Usage.TotalTokens = s.message.Usage.Input + s.message.Usage.Output + s.message.Usage.CacheRead + s.message.Usage.CacheWrite
	s.send(Event{Type: "done", Message: &s.message, Reason: s.message.StopReason})
	return nil
}
func record(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func list(v any) []any { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func integer(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}
func (s *streamState) usage(v map[string]any, api API) error {
	if len(v) == 0 {
		return nil
	}
	if err := validateTokenCounts(v); err != nil {
		return err
	}
	u := &s.message.Usage
	u.Known = true
	switch api {
	case OpenAICompletions, MistralConversations:
		u.CacheRead = integer(record(v["prompt_tokens_details"])["cached_tokens"])
		u.Input = integer(v["prompt_tokens"]) - u.CacheRead
		u.Output = integer(v["completion_tokens"])
	case GoogleGenerative, GoogleVertex:
		u.CacheRead = integer(v["cachedContentTokenCount"])
		u.Input = integer(v["promptTokenCount"]) - u.CacheRead
		u.Output = integer(v["candidatesTokenCount"]) + integer(v["thoughtsTokenCount"])
	case BedrockConverse:
		u.CacheRead = integer(v["cacheReadInputTokens"])
		u.CacheWrite = integer(v["cacheWriteInputTokens"])
		u.Input = integer(v["inputTokens"])
		u.Output = integer(v["outputTokens"])
	default:
		if n, ok := v["input_tokens"]; ok {
			u.Input = integer(n)
		}
		if n, ok := v["output_tokens"]; ok {
			u.Output = integer(n)
		}
		if n, ok := v["cache_read_input_tokens"]; ok {
			u.CacheRead = integer(n)
		}
		if n, ok := v["cache_creation_input_tokens"]; ok {
			u.CacheWrite = integer(n)
		}
		if n, ok := record(v["cache_creation"])["ephemeral_1h_input_tokens"]; ok {
			u.CacheWrite1h = integer(n)
		}
		if api != AnthropicMessages {
			u.CacheRead = integer(record(v["input_tokens_details"])["cached_tokens"])
			u.Input -= u.CacheRead
		}
	}
	if n, ok := record(v["output_tokens_details"])["reasoning_tokens"]; ok {
		value := integer(n)
		u.Reasoning = &value
	}
	if n, ok := record(v["completion_tokens_details"])["reasoning_tokens"]; ok {
		value := integer(n)
		u.Reasoning = &value
	}
	if api == GoogleGenerative || api == GoogleVertex {
		if n, ok := v["thoughtsTokenCount"]; ok {
			value := integer(n)
			u.Reasoning = &value
		}
	}
	if u.Input < 0 || u.Output < 0 {
		return ErrInvalidResponse
	}
	return nil
}

func validateTokenCounts(v map[string]any) error {
	for key, value := range v {
		if child, ok := value.(map[string]any); ok {
			if err := validateTokenCounts(child); err != nil {
				return err
			}
			continue
		}
		if strings.Contains(strings.ToLower(key), "token") || key == "cached_tokens" {
			n, err := number(value)
			if err != nil || n < 0 || n != math.Trunc(n) || n > float64(1<<53-1) {
				return ErrInvalidResponse
			}
		}
	}
	return nil
}

func (s *streamState) consume(api API, v map[string]any) error {
	switch api {
	case OpenAICompletions, MistralConversations:
		return s.consumeChat(api, v)
	case OpenAIResponses, AzureResponses, CodexResponses:
		return s.consumeResponses(api, v)
	case AnthropicMessages:
		return s.consumeAnthropic(api, v)
	case GoogleGenerative, GoogleVertex:
		return s.consumeGoogle(api, v)
	case BedrockConverse:
		return s.consumeBedrock(api, v)
	case GatewayMessages:
		return s.consumeGateway(api, v)
	default:
		return ErrIncompatible
	}
}
func stopReason(reason string) string {
	switch reason {
	case "length", "max_tokens", "MAX_TOKENS":
		return "length"
	case "tool_calls", "tool_use", "function_call":
		return "toolUse"
	case "content_filter", "SAFETY", "RECITATION":
		return "error"
	default:
		return "stop"
	}
}
