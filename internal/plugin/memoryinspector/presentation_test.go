package memoryinspector

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"strings"
	"testing"
)

func TestPresentationMatchesCapturedOriginalNavigationAndLabels(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		URLs []struct {
			Query    contract.Query `json:"query"`
			Key      string         `json:"key"`
			Value    string         `json:"value"`
			Expected string         `json:"expected"`
		} `json:"urls"`
		Labels []struct {
			Kind     string `json:"kind"`
			Input    string `json:"input"`
			Expected string `json:"expected"`
		} `json:"labels"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	req := contract.Request{Navigation: make([]contract.Navigation, len(original.URLs))}
	for i, c := range original.URLs {
		req.Navigation[i] = contract.Navigation{Query: c.Query, Key: c.Key, Value: c.Value}
	}
	result := project(req)
	for i, c := range original.URLs {
		if result.URLs[i] != c.Expected {
			t.Errorf("original route %d: got %q want %q", i, result.URLs[i], c.Expected)
		}
	}
	labels := result.Labels
	sources := map[string]string{"conversation": labels.Source.Conversation, "task": labels.Source.Task, "tool": labels.Source.Tool, "control": labels.Source.Control}
	origins := map[string]string{"written": labels.Origin.Written, "dream": labels.Origin.Dream, "original": labels.Origin.Original, "automatic": labels.Origin.Automatic, "explicit": labels.Origin.Explicit}
	layers := map[string]string{"transcripts": labels.Layer.Transcripts, "representations": labels.Layer.Representations, "injected": labels.Layer.Injected}
	for _, c := range original.Labels {
		var got string
		switch c.Kind {
		case "source":
			got = sources[c.Input]
			if got == "" {
				got = labels.Source.Other
			}
		case "origin":
			got = origins[c.Input]
			if got == "" {
				got = labels.Origin.Other
			}
		case "layer":
			got = layers[c.Input]
			if got == "" {
				got = labels.Layer.Notes
			}
		case "status":
			got = c.Input
			if got == "" {
				got = labels.DefaultStatus
			}
		default:
			t.Fatalf("unknown captured case %q", c.Kind)
		}
		if got != c.Expected {
			t.Errorf("original %s %q: got %q want %q", c.Kind, c.Input, got, c.Expected)
		}
	}
}

func TestPresentationOwnsCapturedOriginalViewBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/managed-memory-v1.html.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := project(contract.Request{Navigation: []contract.Navigation{}}).View
	start := strings.Index(got.Source, "{{if eq .Operation \"dream\"}}<section")
	end := strings.Index(got.Source, "<section class=\"memory-generation-history\"><h3>{{tr \"Model attempts\"}}")
	if start < 0 || end <= start || got.Resource != contract.ViewResource || got.Source[:start]+got.Source[end:] != string(raw) || got.Digest != rsiplugin.FileDigest([]byte(got.Source)) {
		t.Fatal("owning resource changed independently captured original view")
	}
}

func TestPresentationOwnsCapturedOriginalStylesheetBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/memory.css.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := project(contract.Request{Navigation: []contract.Navigation{}}).Stylesheet
	if got.Resource != contract.StylesheetResource || got.Source != string(raw) || got.Digest != rsiplugin.FileDigest(raw) {
		t.Fatal("owning stylesheet changed independently captured original")
	}
}

func TestPresentationOwnsCapturedOriginalLocalizationBytes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/memory-inspector-original-v1/localization-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	got := project(contract.Request{Navigation: []contract.Navigation{}}).Localization
	var original, current []contract.Translation
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got.Source), &current); err != nil {
		t.Fatal(err)
	}
	translations := map[string]string{}
	for _, item := range current {
		translations[item.English] = item.Chinese
	}
	for _, item := range original {
		if translations[item.English] != item.Chinese {
			t.Fatalf("historical translation changed: %s", item.English)
		}
	}
	if got.Resource != contract.LocalizationResource || len(current) != len(original)+5 || got.Digest != rsiplugin.FileDigest([]byte(got.Source)) {
		t.Fatal("owning localization changed independent original capture")
	}
}
