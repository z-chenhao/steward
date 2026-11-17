package connectedapps

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialCaptionsProjectPrivateOriginalSourcesAcrossBatches(t *testing.T) {
	calls := []int{}
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{Captions: func(_ context.Context, facts []contract.CaptionFacts) ([]contract.CaptionPlan, error) {
		calls = append(calls, len(facts))
		raw, _ := json.Marshal(facts)
		if strings.Contains(string(raw), "private") {
			t.Fatal("source text entered candidate input")
		}
		plans := make([]contract.CaptionPlan, len(facts))
		for i, f := range facts {
			if f.EditorialRunes != 3 || f.ProviderRunes != 29 {
				t.Fatal("original facts changed", f)
			}
			plans[i] = contract.CaptionPlan{Source: "editorial", End: 2, Text: "!"}
		}
		return plans, nil
	}}}
	sources := make([]CaptionSources, 129)
	for i := range sources {
		sources[i] = CaptionSources{Editorial: "汉🙂字", Provider: "private original source facts"}
	}
	result, err := selection.ProjectCaptions(context.Background(), sources)
	if err != nil || len(result) != 129 || !reflect.DeepEqual(calls, []int{64, 64, 1}) {
		t.Fatal(result, calls, err)
	}
	for _, text := range result {
		if text != "汉🙂!" {
			t.Fatal("wrong original Unicode prefix", text)
		}
	}
}
func TestEditorialCaptionsRejectInvalidReferencesWithoutPartialOrFallback(t *testing.T) {
	if output, err := (DirectorySelection{}).ProjectCaptions(context.Background(), []CaptionSources{{Provider: "original"}}); !errors.Is(err, ErrUnavailable) || output != nil {
		t.Fatal("empty selection invented native compatibility", output, err)
	}
	emptyContext := WithEditorial(context.Background(), contract.Query{}, DirectorySelection{}, nil)
	if _, err := (&Service{}).Directory(emptyContext, contract.Query{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("empty frozen selection invented native compatibility", err)
	}

	for name, plan := range map[string]contract.CaptionPlan{
		"unknown-source": {Source: "account-name", End: 1}, "out-of-range": {Source: "provider", End: 4}, "negative": {Source: "editorial", End: -1}, "literal-range": {Source: "literal", End: 1}, "oversized-literal": {Source: "literal", Text: strings.Repeat("x", 4097)}, "invalid-text": {Source: "literal", Text: string([]byte{255})},
	} {
		t.Run(name, func(t *testing.T) {
			selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{Captions: func(context.Context, []contract.CaptionFacts) ([]contract.CaptionPlan, error) {
				return []contract.CaptionPlan{plan}, nil
			}}}
			result, err := selection.ProjectCaptions(context.Background(), []CaptionSources{{Editorial: "汉", Provider: "abc"}})
			if !errors.Is(err, ErrUnavailable) || result != nil {
				t.Fatal("bad plan exposed original/native text", result, err)
			}
		})
	}
	for _, method := range []CaptionProjector{nil, func(context.Context, []contract.CaptionFacts) ([]contract.CaptionPlan, error) { return nil, nil }, func(context.Context, []contract.CaptionFacts) ([]contract.CaptionPlan, error) {
		return nil, errors.New("private failure")
	}} {
		selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{Captions: method}}
		if result, err := selection.ProjectCaptions(context.Background(), []CaptionSources{{Provider: "original"}}); !errors.Is(err, ErrUnavailable) || result != nil {
			t.Fatal("missing/error/short result restored native", result, err)
		}
	}
	calls := 0
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{Captions: func(_ context.Context, facts []contract.CaptionFacts) ([]contract.CaptionPlan, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("late failure")
		}
		plans := make([]contract.CaptionPlan, len(facts))
		for i := range plans {
			plans[i] = contract.CaptionPlan{Source: "literal", Text: "first batch"}
		}
		return plans, nil
	}}}
	if result, err := selection.ProjectCaptions(context.Background(), make([]CaptionSources, 65)); !errors.Is(err, ErrUnavailable) || result != nil || calls != 2 {
		t.Fatal("partial batch escaped", result, err, calls)
	}
}
