package tool

import (
	"context"
	"testing"
)

func TestPromptResourcesAreCopiedAndFrozenOnSelection(t *testing.T) {
	d, _ := FixtureRegistry().Resolve("mcp__fixture__echo")
	d.Spec.PromptContext = []PromptContext{{Kind: "project_instructions", Source: "frozen", Text: "old rules"}}
	r, e := NewRegistry([]Definition{d})
	if e != nil {
		t.Fatal(e)
	}
	d.Spec.PromptContext[0].Text = "caller mutation"
	frozen := r.Describe()
	if frozen[0].PromptContext[0].Text != "old rules" {
		t.Fatal("mutable registry input")
	}
	frozen[0].PromptContext[0].Text = "local mutation"
	if r.Describe()[0].PromptContext[0].Text != "old rules" {
		t.Fatal("mutable registry output")
	}
	frozen = r.Describe()
	live, _ := r.Resolve(d.Spec.Name)
	live.Spec.PromptContext[0].Text = "newly loaded rules"
	refreshed, e := NewRegistry([]Definition{live})
	if e != nil {
		t.Fatal(e)
	}
	selected, e := refreshed.Select(frozen)
	if e != nil {
		t.Fatal(e)
	}
	if selected.Describe()[0].PromptContext[0].Text != "old rules" {
		t.Fatal("frozen Run acquired new instructions")
	}
}

func TestModelDeclarationIsFrozenWithoutChangingExecutionSchema(t *testing.T) {
	d, _ := FixtureRegistry().Resolve("mcp__fixture__echo")
	d.Spec.ModelInputSchema = `{"type":"object","description":"Original compact declaration"}`
	r, err := NewRegistry([]Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	for _, compact := range []string{d.Spec.ModelInputSchema, ""} {
		frozen := r.Describe()
		frozen[0].ModelInputSchema = compact
		d.Spec.ModelInputSchema = `{"type":"object","description":"New live declaration"}`
		live, err := NewRegistry([]Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := live.Select(frozen)
		if err != nil || selected.Describe()[0].ModelInputSchema != compact || selected.Describe()[0].InputSchema != d.Spec.InputSchema {
			t.Fatal("live metadata changed a frozen declaration or its execution schema", err)
		}
	}
	d.Spec.ModelInputSchema = `{"type":"object","unknownKeyword":true}`
	if _, err := NewRegistry([]Definition{d}); err == nil {
		t.Fatal("unsupported model declaration accepted")
	}
}

func TestEchoSchemaAndPolicyDefaults(t *testing.T) {
	registry := FixtureRegistry()
	definition, ok := registry.Resolve("mcp__fixture__echo")
	if !ok {
		t.Fatal("Tool absent")
	}
	for _, input := range []string{`{}`, `null`, `[]`, `{"text":1}`, `{"text":"ok","unfrozen":true}`, `{"text":"a"} {"text":"b"}`} {
		result := definition.Execute(context.Background(), input)
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" || result.Error.Retryable {
			t.Fatal("invalid argument accepted", input, result)
		}
	}
	result := definition.Execute(context.Background(), `{"text":"ok"}`)
	if result.Error != nil || result.Text != "ok" {
		t.Fatal(result)
	}
	for _, policy := range []string{"", "never", "safe"} {
		spec := definition.Spec
		spec.ReplayPolicy = policy
		registry, err := NewRegistry([]Definition{{Spec: spec, Execute: definition.Execute}})
		if err != nil {
			t.Fatal(err)
		}
		d, _ := registry.Resolve(spec.Name)
		want := policy
		if want == "" {
			want = "never"
		}
		if d.Spec.Policy().Replay != want {
			t.Fatal("replay default", d.Spec.Policy())
		}
	}
	specQuery := definition.Spec
	specQuery.ReplayPolicy = "query_before_retry"
	if _, err := NewRegistry([]Definition{{Spec: specQuery, Execute: definition.Execute}}); err == nil {
		t.Fatal("query policy needs authoritative query")
	}

	spec := definition.Spec
	spec.Permission = "external_effect"
	if _, err := NewRegistry([]Definition{{Spec: spec, Execute: definition.Execute}}); err == nil {
		t.Fatal("effectful tool admitted")
	}
}

func TestRegistryClosesMissingExternalMetadataAndUnsupportedSchemas(t *testing.T) {
	d := Definition{Spec: Spec{Name: "mcp__example__send", Version: 1, InputSchema: `{"type":"object"}`, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 1000, OutputBytes: 1024, Provider: "example", ConflictPolicy: "none"}, Invoke: func(context.Context, Request) Outcome { return Outcome{} }}
	if _, err := NewRegistry([]Definition{d}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Definition){func(d *Definition) { d.Spec.Risk = "low" }, func(d *Definition) { d.Spec.TimeoutMS = 0 }, func(d *Definition) { d.Spec.OutputBytes = 0 }, func(d *Definition) { d.Spec.Provider = "" }, func(d *Definition) { d.Spec.ReplayPolicy = "safe" }, func(d *Definition) { d.Spec.ReplayPolicy = "query_before_retry" }, func(d *Definition) { d.Spec.ConflictPolicy = "dynamic" }, func(d *Definition) { d.Spec.InputSchema = `{"type":"object","patternProperties":{}}` }} {
		changed := d
		mutate(&changed)
		if _, err := NewRegistry([]Definition{changed}); err == nil {
			t.Fatal("incomplete policy registered", changed.Spec)
		}
	}
}
func TestSchemaRejectsInvalidBoundsTypesAndDuplicates(t *testing.T) {
	schema := `{"type":"object","properties":{"n":{"type":"integer","minimum":1,"maximum":3},"tags":{"type":"array","maxItems":2,"items":{"type":"string","maxLength":2}}},"required":["n"],"additionalProperties":false}`
	if !Validate(schema, `{"n":1,"tags":["中"]}`) {
		t.Fatal("valid value rejected")
	}
	for _, args := range []string{`{"n":0}`, `{"n":4}`, `{"n":1.1}`, `{"n":"1"}`, `{"n":null}`, `{"n":1,"n":2}`, `{"n":1,"tags":["long"]}`, `{"n":1,"tags":["a","b","c"]}`, `{"n":1,"extra":true}`, `{"n":1} {}`, `null`} {
		if Validate(schema, args) {
			t.Fatal("invalid value", args)
		}
	}
}

func TestHostErrorCodesStripAllProviderDetails(t *testing.T) {
	for _, code := range []string{"INVALID_ARGUMENT", "NOT_FOUND", "PERMISSION_DENIED", "APPROVAL_DENIED", "TIMEOUT", "CANCELLED", "PROVIDER_UNAVAILABLE", "RESOURCE_LIMIT", "UNKNOWN_SIDE_EFFECT", "NON_DETERMINISTIC_REPLAY", "private raw SQL credential"} {
		result := Normalize(Result{Text: "private internal Run ID", Error: &Error{Code: code, Message: "credential internal-id /private/path SQL", Retryable: true}})
		want := code
		if code == "private raw SQL credential" {
			want = "PROVIDER_UNAVAILABLE"
		}
		if result.Error.Code != want || result.Error.Message != want || result.Text != "" || result.Error.Retryable != (want == "PROVIDER_UNAVAILABLE") {
			t.Fatal(result)
		}
	}
}
