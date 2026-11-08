// Package fake implements deterministic stdio fixtures. It is reachable only
// through explicit test injection; it never performs network or secret I/O.
package fake

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/bridge"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/prompt"
)

func ValidFixture(f string) bool {
	if strings.HasPrefix(f, "error:") {
		code := strings.TrimPrefix(f, "error:")
		return llm.NormalizeError(code).Error() == code
	}
	switch f {
	case "local", "agenda", "research", "monitor", "wedding", "meeting", "automation", "computer", "recall", "core", "codemode", "children", "capabilities", "artifacts", "approval", "approval-never", "tasks", "terminal-input", "golden", "main", "success", "incompatible", "crash", "partial-crash", "completed-crash", "no-tools", "bad-sequence", "drift", "malformed", "auth-required", "raw-error", "event-error":
		return true
	}
	return false
}

func ServeRecorded(in io.Reader, out io.Writer, fixture, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return serve(in, out, fixture, f)
}
func Serve(in io.Reader, out io.Writer, fixture string) error { return serve(in, out, fixture, nil) }
func serve(in io.Reader, out io.Writer, fixture string, audit io.Writer) error {
	if !ValidFixture(fixture) {
		return llm.InvalidProviderResponse
	}
	encoder := json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), bridge.MaxFrameBytes)
	status := "logged_out"
	waiting := map[string]int64{}
	for scanner.Scan() {
		var r struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      uint64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil || r.JSONRPC != "2.0" || r.ID == 0 {
			return llm.InvalidProviderResponse
		}
		reply := func(value any) error {
			return encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": value})
		}
		failure := func(code string) error {
			return encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32000, "message": code}})
		}
		event := func(e llm.GenerationEvent) error {
			return encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "generation.event", "params": e})
		}
		var err error
		switch r.Method {
		case "bridge.shutdown":
			return reply(struct{}{})
		case "bridge.initialize":
			version := bridge.ProtocolVersion
			if fixture == "incompatible" {
				version++
			}
			err = reply(map[string]int{"protocol_version": version})
		case "auth.start":
			err = reply(llm.AuthState{Status: "reauth_required", Events: []llm.AuthEvent{{Kind: "open_url", URL: "https://example.invalid/authorize"}, {Kind: "device_code", DeviceCode: "TEST-CODE"}, {Kind: "progress"}, {Kind: "choose_workspace", Choices: []string{"test-workspace"}}, {Kind: "input_required"}}})
		case "auth.continue", "auth.refresh":
			status = "connected"
			err = reply(llm.AuthState{Status: status, Events: []llm.AuthEvent{}})
		case "auth.logout":
			status = "logged_out"
			err = reply(struct{}{})
		case "auth.status":
			err = reply(llm.AuthState{Status: status, Events: []llm.AuthEvent{}})
		case "models.list", "models.refresh":
			err = reply([]llm.CatalogModel{{ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 1024}})
		case "model.probe":
			err = reply(map[string]string{"status": "use_host_behavioral_probe"})
		case "generation.start", "generation.continue":
			if strings.HasPrefix(fixture, "error:") {
				err = failure(strings.TrimPrefix(fixture, "error:"))
				break
			}
			if fixture == "crash" {
				return nil
			}
			if fixture == "auth-required" {
				err = failure(string(llm.AuthRequired))
				break
			}
			if fixture == "raw-error" {
				err = failure("SYNTHETIC_PROVIDER_SECRET response body")
				break
			}
			if fixture == "malformed" {
				_, err = io.WriteString(out, "invalid JSON\n")
				break
			}
			var request llm.GenerationRequest
			if json.Unmarshal(r.Params, &request) != nil {
				return llm.InvalidProviderResponse
			}
			color := ""
			if request.Snapshot.Capabilities.ImageInput {
				var e error
				color, e = imageInput(r.Params, request)
				if e != nil {
					return e
				}
			}
			if audit != nil {
				if err = json.NewEncoder(audit).Encode(map[string]string{"generation_id": request.GenerationID, "snapshot_id": request.Snapshot.SnapshotID}); err != nil {
					return err
				}
			}
			if err = reply(struct{}{}); err != nil {
				return err
			}
			if len(request.Messages) > 0 && (request.Messages[0].Text == memory.CurationInstruction || strings.HasPrefix(request.Messages[0].Text, memory.DreamInstruction)) {
				value := memoryReply(request)
				if strings.HasPrefix(request.Messages[0].Text, memory.DreamInstruction) {
					value = llm.GenerationResult{Text: `{"facts":[],"documents":[],"done":true}`}
				}
				if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 1, Kind: "text_delta", Text: value.Text}); err != nil {
					return err
				}
				err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 2, Kind: "completed"})
				break
			}
			e := llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 1, Kind: "text_delta", Text: "OK"}
			last := request.Messages[len(request.Messages)-1]
			summarizing := len(request.Messages) > 0 && request.Messages[0].Text == prompt.SummarySystem()
			if summarizing || (fixture == "local" || fixture == "agenda" || fixture == "research" || fixture == "monitor" || fixture == "wedding" || fixture == "meeting" || fixture == "automation" || fixture == "computer" || fixture == "recall" || fixture == "core" || fixture == "codemode" || fixture == "children" || fixture == "capabilities" || fixture == "artifacts" || fixture == "tasks" || fixture == "approval" || fixture == "approval-never") && (hasTool(request.Tools, "mcp__fixture__echo") || hasTool(request.Tools, "builtin__artifact_search")) {
				// Hold an actual accepted Worker generation while keeping the RPC
				// peer responsive to cancellation. A new Root gets the appended
				// instruction as its last message and can finish normally.
				if !summarizing && fixture == "tasks" && strings.Contains(last.Text, "Task anchor:") && strings.Contains(last.Text, "Hold worker") {
					err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 1, Kind: "text_delta", Text: "Worker waiting for cancellation"})
					waiting[request.GenerationID] = 2
					break
				}
				var value llm.GenerationResult
				var cause error
				if fixture == "children" && last.Text == "Hold child" {
					err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 1, Kind: "text_delta", Text: "Child waiting for cancellation"})
					waiting[request.GenerationID] = 2
					break
				}
				if summarizing {
					value = contextReply(request)
				} else if fixture == "automation" {
					value = automationReply(request)
				} else if fixture == "recall" {
					value = recallReply(request)
				} else if fixture == "children" {
					value, cause = childrenReply(request)
				} else if fixture == "computer" || fixture == "core" || fixture == "capabilities" || fixture == "codemode" {
					value = capabilitiesReply(request)
				} else if fixture == "artifacts" {
					value = artifactsReply(request)
				} else if fixture == "tasks" {
					value, cause = tasksReply(request)
				} else {
					value = approvalReply(request)
				}
				sequence := int64(1)
				if cause != nil {
					err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "error", ErrorCode: cause.Error()})
					break
				}
				for _, call := range value.ToolCalls {
					c := call
					if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "tool_call", ToolCall: &c}); err != nil {
						return err
					}
					sequence++
				}
				if value.Text != "" {
					if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "text_delta", Text: value.Text}); err != nil {
						return err
					}
					sequence++
				}
				var usage *llm.TokenUsage
				if fixture == "children" && (request.Snapshot.ModelID == "priced-fixture-model" || request.Snapshot.ModelID == "overrun-fixture-model") {
					// Synthetic, explicit monetary fixture; ordinary children keep
					// their missing-usage contract. No real market price is implied.
					usage = &llm.TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20, CacheWriteTokens: 10}
					if request.Snapshot.ModelID == "overrun-fixture-model" {
						usage.InputTokens = 200000
					}
				}
				err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "completed", Usage: usage})
				break
			}
			if fixture == "terminal-input" && (hasTool(request.Tools, "mcp__fixture__echo") || hasTool(request.Tools, "builtin__artifact_search")) {
				// A short model reply isolates the terminal byte boundary from
				// the separate Tool argument/output limits. Probe still runs below.
				e.Text = "Input received."
				if err = event(e); err != nil {
					return err
				}
				err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 2, Kind: "completed"})
				break
			}
			if (fixture == "main" || fixture == "golden") && (hasTool(request.Tools, "mcp__fixture__echo") || hasTool(request.Tools, "builtin__artifact_search")) {
				last = mainFixtureMessage(request.Messages)
				// Same call IDs on distinct exchanges deliberately exercise the
				// (run, exchange, call) association, never global call-ID dedup.
				sequence := int64(1)
				if last.Origin == "runtime" && strings.HasPrefix(last.Text, "Runtime tool_results") || last.Role == "user" && last.Text == "F003:ping" {
					text := "F003:pong"
					if last.Origin == "runtime" {
						text = "F003:finished " + last.Text
					}
					if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "text_delta", Text: text}); err != nil {
						return err
					}
					sequence++
				} else if last.Role == "user" {
					for _, id := range []string{"echo-a", "echo-b"} {
						args := []byte(`{}`)
						call := llm.ToolCall{ID: id, Name: "builtin__artifact_search", Arguments: string(args)}
						if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "tool_call", ToolCall: &call}); err != nil {
							return err
						}
						sequence++
					}
				} else if last.Role == "tool" {
					user := ""
					for i := len(request.Messages) - 1; i >= 0; i-- {
						m := request.Messages[i]
						if m.Role == "user" && m.Origin == "" {
							user = m.Text
							break
						}
					}
					text := "Processed: " + user
					if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "text_delta", Text: text}); err != nil {
						return err
					}
					sequence++
				} else {
					return llm.InvalidProviderResponse
				}
				if err = event(llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: sequence, Kind: "completed"}); err != nil {
					return err
				}
				break
			}
			if r.Method == "generation.continue" {
				if last.Role != "tool" || last.ToolCallID != "probe-call" {
					err = failure(string(llm.InvalidProviderResponse))
					break
				}
				e.Text = last.Text
			} else if len(request.Tools) > 0 && fixture != "no-tools" {
				e.Kind = "tool_call"
				e.Text = ""
				e.ToolCall = &llm.ToolCall{ID: "probe-call", Name: "probe_echo", Arguments: `{"value":"OK"}`}
			} else if strings.Contains(last.Text, "long output") {
				e.Text = strings.Repeat("x", 128<<10)
			} else if color != "" && strings.Contains(last.Text, "dominant color") {
				e.Text = color
			}
			if fixture == "bad-sequence" {
				e.Sequence = 2
			}
			if fixture == "event-error" {
				e.Kind = "error"
				e.Text = ""
				e.ErrorCode = string(llm.RateLimited)
			}
			if err = event(e); err != nil {
				return err
			}
			if fixture == "partial-crash" {
				return nil
			}
			if err = event(e); err != nil {
				return err
			} // exact redelivery fixture
			if fixture == "drift" {
				e.Text = "changed"
				err = event(e)
				if err != nil {
					return err
				}
			}
			if strings.Contains(last.Text, "until cancelled") {
				waiting[request.GenerationID] = 2
				break
			}
			e = llm.GenerationEvent{GenerationID: request.GenerationID, Sequence: 2, Kind: "completed"}
			if err = event(e); err == nil {
				err = event(e)
			} // completion commits only once
			if fixture == "completed-crash" {
				return err
			}
		case "generation.cancel":
			var params struct {
				GenerationID string `json:"generation_id"`
			}
			if json.Unmarshal(r.Params, &params) != nil {
				return llm.InvalidProviderResponse
			}
			err = reply(struct{}{})
			if next, ok := waiting[params.GenerationID]; ok && err == nil {
				delete(waiting, params.GenerationID)
				err = event(llm.GenerationEvent{GenerationID: params.GenerationID, Sequence: next, Kind: "error", ErrorCode: string(llm.GenerationCancelled)})
			}
		case "session.dispose":
			err = reply(struct{}{})
		default:
			err = failure(string(llm.InvalidProviderResponse))
		}
		if err != nil {
			return err
		}
	}
	return scanner.Err()
}

func hasTool(specs []llm.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}
