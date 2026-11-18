package bridge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
)

func TestServerTimingObservationsReachHost(t *testing.T) {
	for _, phase := range []string{"headers", "first_body", "first_text", "first_tool_call", "stream_terminal", "stream_idle_timeout"} {
		t.Run(phase, func(t *testing.T) {
			var pipe bytes.Buffer
			server := Server{out: &pipe}
			server.observe(generationRequest{GenerationRequest: llm.GenerationRequest{GenerationID: "generation"}, Observe: true}, provider.Observation{Phase: phase, Attempt: "attempt", Status: 200, At: time.Now()})
			var f frame
			if err := json.Unmarshal(pipe.Bytes(), &f); err != nil {
				t.Fatal(err)
			}
			stream := newEventStream("generation", 1024)
			accepted := false
			stream.capture = func(d llm.Dispatch) {
				accepted = d.Phase == phase && (d.HTTPStatus == 0 || phase == "headers" && d.HTTPStatus == 200)
			}
			stream.observe(f.Params)
			if !accepted {
				t.Fatalf("Host discarded %s: %s", phase, f.Params)
			}
		})
	}
}

func TestDispatchEvidenceIsOptionalBoundedAndCannotRejectGeneration(t *testing.T) {
	s := newEventStream("generation", 1024)
	count := 0
	s.wireSecrets = []string{"PRIVATE_KEY"}
	s.capture = func(d llm.Dispatch) {
		count++
		if bytes.Contains(d.Body, []byte("PRIVATE_KEY")) {
			t.Fatal("Host stored peer credential")
		}
	}
	raw := func(id, body string) json.RawMessage {
		b, _ := json.Marshal(llm.Dispatch{GenerationID: id, Attempt: "attempt", Phase: "started", At: 1, Body: json.RawMessage(body)})
		return b
	}
	s.observe(raw("other", `{}`))
	s.observe([]byte(`"malformed"`))
	if count != 0 {
		t.Fatal("cross-registration evidence accepted")
	}
	s.observe(raw("generation", `{"messages":[{"text":"PRIVATE_KEY"}],"authorization":"PRIVATE_KEY"}`))
	if count != 1 {
		t.Fatal("valid capture lost")
	}
	for range 30 {
		s.observe(raw("generation", `{}`))
	}
	if count != 16 {
		t.Fatal("unbounded diagnostics", count)
	}
	s.capture = func(llm.Dispatch) { panic("diagnostic sink failure") }
	s.observed = 0
	s.observe(raw("generation", `{}`))
	s.accept(llm.GenerationEvent{GenerationID: s.id, Sequence: 1, Kind: "text_delta", Text: "model still works"})
	if s.terminal || (<-s.events).event.Text != "model still works" {
		t.Fatal("diagnostics changed generation event sequence")
	}
}

func TestServerObservationBoundsSanitizedRequest(t *testing.T) {
	var pipe bytes.Buffer
	server := Server{out: &pipe}
	req := generationRequest{
		GenerationRequest: llm.GenerationRequest{GenerationID: "generation"},
		Connection:        llm.Connection{APIKey: "PRIVATE_KEY", AccessToken: "PRIVATE_ACCESS"},
	}
	observation := provider.Observation{
		Phase: "started", Attempt: "attempt", At: time.Now(),
		Body: []byte(`{"authorization":"PRIVATE_KEY","messages":[{"text":"PRIVATE_ACCESS"}]}`),
	}
	server.observe(req, observation)
	if pipe.Len() != 0 {
		t.Fatal("unrequested wire observation")
	}
	req.Observe = true
	server.observe(req, observation)
	decode := func() llm.Dispatch {
		t.Helper()
		var f frame
		var dispatch llm.Dispatch
		if err := json.Unmarshal(pipe.Bytes(), &f); err != nil || f.Method != "generation.observation" {
			t.Fatalf("wire observation frame: %v %s", err, pipe.Bytes())
		}
		if err := json.Unmarshal(f.Params, &dispatch); err != nil {
			t.Fatal(err)
		}
		pipe.Reset()
		return dispatch
	}
	captured := decode()
	if captured.Format != "steward-wire-v1/go-ai-v1" ||
		len(captured.BodyHash) != 64 || bytes.Contains(captured.Body, []byte("PRIVATE_")) ||
		bytes.Count(captured.Body, []byte("[credential redacted]")) != 2 {
		t.Fatal("incorrect sanitized request observation", captured)
	}
	observation.Body = []byte(`{"text":"` + strings.Repeat("x", 300000) + `"}`)
	server.observe(req, observation)
	captured = decode()
	if len(captured.Body) != 0 || len(captured.BodyHash) != 64 || captured.Omitted != "sanitized body exceeds 256 KiB" {
		t.Fatal("oversized observation missing bounded identity", captured)
	}
}
