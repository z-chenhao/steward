package rsiplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictFramesAndCanonicalIdentity(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{} {}`, `{"unknown":1}`} {
		var req Request
		if Decode([]byte(raw), &req) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	a, err := Digest("domain", json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Digest("domain", json.RawMessage(`{"a":1,"b":2}`))
	if err != nil || a != b {
		t.Fatal("unstable identity", a, b, err)
	}
	c, _ := Digest("other", json.RawMessage(`{"a":1,"b":2}`))
	if c == a {
		t.Fatal("domain separation lost")
	}
	for _, raw := range []string{`{"a":1e999}`, `{"a":NaN}`} {
		if _, err := Canonical(json.RawMessage(raw)); err == nil {
			t.Fatal("unbounded number accepted")
		}
	}
	large, err := Canonical(json.RawMessage(`{"a":9007199254740993,"b":1.25}`))
	if err != nil || string(large) != `{"a":9007199254740993,"b":1.25}` {
		t.Fatal("numeric identity rounded", string(large), err)
	}
	if Decode([]byte(strings.Repeat(" ", MaxFrameBytes+1)), new(Request)) == nil {
		t.Fatal("frame limit lost")
	}
}
func TestHookAuthorityAndProjection(t *testing.T) {
	text := "projection"
	for _, test := range []struct {
		stage  Stage
		output HookOutput
		valid  bool
	}{
		{BeforeToolCall, HookOutput{Reject: true}, true}, {RunSettled, HookOutput{Reject: true}, false},
		{BeforeToolCall, HookOutput{Arguments: json.RawMessage(`{"text":"changed"}`)}, true},
		{ModelResult, HookOutput{Arguments: json.RawMessage(`{}`)}, false},
		{BeforeModelRequest, HookOutput{AdditionalContext: []Message{{Kind: "plugin_context", Text: "note"}}}, true},
		{BeforeModelRequest, HookOutput{AdditionalContext: []Message{{Kind: "system", Text: "replace safety"}}}, false},
		{PrepareReply, HookOutput{Text: &text}, true}, {ModelStream, HookOutput{Text: &text}, false},
	} {
		if valid := test.output.Validate(test.stage) == nil; valid != test.valid {
			t.Fatalf("%s: %v", test.stage, valid)
		}
	}
}
func TestServerLifecycleAndUnknownFields(t *testing.T) {
	var lifecycle []string
	h := Handlers{PluginID: "steward.test", Version: "1.0.0", OnPrepare: func(context.Context, PrepareInput) error { lifecycle = append(lifecycle, "prepare"); return nil }, Services: map[string]func(context.Context, Call) (Result, error){"steward.echo/1": func(_ context.Context, c Call) (Result, error) {
		lifecycle = append(lifecycle, "call")
		return Result{Output: c.Input}, nil
	}}, OnDrain: func(context.Context) error { lifecycle = append(lifecycle, "drain"); return nil }, OnDispose: func(context.Context) error { lifecycle = append(lifecycle, "dispose"); return nil }}
	var input, output bytes.Buffer
	requests := []struct {
		method string
		value  any
	}{{"call", Call{}}, {"prepare", PrepareInput{Protocol: Protocol, Config: json.RawMessage(`{}`)}}, {"call", Call{Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"ok"}`), State: State{Value: json.RawMessage(`{}`)}}}, {"drain", nil}, {"call", Call{}}, {"dispose", nil}}
	for _, r := range requests {
		raw, _ := json.Marshal(r.value)
		if err := json.NewEncoder(&input).Encode(Request{Protocol: Protocol, ID: r.method, Method: r.method, Params: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Serve(context.Background(), &input, &output, h); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lifecycle, ",") != "prepare,call,drain,dispose" {
		t.Fatal(lifecycle)
	}
	d := json.NewDecoder(&output)
	for i := range requests {
		var r Response
		if err := d.Decode(&r); err != nil {
			t.Fatal(err)
		}
		if (r.Error != "") != (i == 0 || i == 4) {
			t.Fatalf("response %d: %+v", i, r)
		}
	}
}

func TestControlFixturesAndResultsDoNotShareWireFrameLimit(t *testing.T) {
	raw := json.RawMessage(`{"text":"` + strings.Repeat("<", 200000) + `"}`)
	canonical, err := Canonical(raw)
	if err != nil || len(canonical) <= MaxFrameBytes {
		t.Fatal("legal result cannot be independently compared", len(canonical), err)
	}
	var result struct {
		Text string `json:"text"`
	}
	if Decode(canonical, &result) == nil || DecodeControl(canonical, &result) != nil || len(result.Text) != 200000 {
		t.Fatal("control document widened RPC or lost text")
	}
	if DecodeControl([]byte(strings.Repeat(" ", MaxControlBytes+1)), new(any)) == nil {
		t.Fatal("unbounded owner document")
	}
}
