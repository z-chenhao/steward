package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"os"
	"strings"
	"testing"
)

func TestModelInputShorthandPreservesNativeIdentityAndFailures(t *testing.T) {
	raw, e := os.ReadFile("../../tests/fixtures/plugins/model-connections-original-v1/cli-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Args  []string         `json:"args"`
		Input llm.ProfileInput `json:"input"`
		Error string           `json:"error"`
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	if len(cases) != 10 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		flags := flag.NewFlagSet("model", flag.ContinueOnError)
		if e = flags.Parse(c.Args); e != nil {
			t.Fatal(e)
		}
		got, e := readModelInput(command{kind: commandModel}, flags, strings.NewReader(""))
		if c.Error != "" {
			if e == nil || e.Error() != c.Error {
				t.Errorf("case %d: err=%v want=%s", i, e, c.Error)
			}
			continue
		}
		if e != nil || got.Name != c.Input.Name || got.ModelID != c.Input.ModelID || got.ProviderKind != c.Input.ProviderKind || got.Endpoint != c.Input.Endpoint || got.AuthConnectionID != c.Input.AuthConnectionID || got.ContextWindow != 0 || got.MaxOutputTokens != 0 || got.WireAPI != "" {
			t.Errorf("case %d identity/defaults mismatch %#v err=%v", i, got, e)
		}
	}
}
func TestModelInputExplicitMetadataKeepsUserValues(t *testing.T) {
	flags := flag.NewFlagSet("model", flag.ContinueOnError)
	_ = flags.Parse([]string{"model", "add"})
	text := `{"name":"Explicit","model_id":"explicit-model","provider_kind":"openai_compatible","endpoint":"http://localhost:8080/v1","wire_api":"openai_responses","context_window":32768,"max_output_tokens":2048,"compatibility":{"tool_result_format":"openai"},"capabilities":{"text_input":true,"image_input":true}}`
	input, e := readModelInput(command{kind: commandModel}, flags, strings.NewReader(text))
	if e != nil || input.ContextWindow != 32768 || input.MaxOutputTokens != 2048 || input.WireAPI != "openai_responses" || !input.Capabilities.ImageInput {
		t.Fatal(input, e)
	}
}

func TestModelInputShorthandDispatchPersistsValidatedDefaults(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, e := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if e != nil {
		t.Fatal(e)
	}
	application, e := app.OpenRuntimeWithSecrets(ctx, cfg, memory.New())
	if e != nil {
		t.Fatal(e)
	}
	defer application.Close()
	flags := flag.NewFlagSet("model", flag.ContinueOnError)
	_ = flags.Parse([]string{"model", "add", "http://localhost:8080/v1", "CLI model"})
	input, e := readModelInput(command{kind: commandModel}, flags, strings.NewReader(""))
	if e != nil {
		t.Fatal(e)
	}
	var out, diagnostics bytes.Buffer
	if e = runModel(ctx, application, flags, strings.NewReader(""), &out, input, true, &diagnostics); e != nil {
		t.Fatal("actual CLI dispatch skipped defaults", e)
	}
	var profile llm.Profile
	if e = json.Unmarshal(out.Bytes(), &profile); e != nil {
		t.Fatal(e)
	}
	if profile.ContextWindow != 8192 || profile.MaxOutputTokens != 1024 || profile.WireAPI != "openai_completions" || profile.Capabilities.ImageInput || !profile.Capabilities.TextInput || profile.ModelID != "CLI model" || profile.Endpoint != "http://localhost:8080/v1" {
		t.Fatal(profile)
	}
}
