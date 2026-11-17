package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	contextbuilder "github.com/z-chenhao/assistant/internal/context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact"
	"github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
)

func TestContextPluginSelectionPreservesNativeWorkingSetAndPrivateBytes(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 43))
	for trial := 0; trial < 300; trial++ {
		in := []llm.Message{{Role: "system", Text: "private system"}, {Role: "user", Text: "private goal", Source: &llm.ContextSource{Kind: "task", Protected: true}}}
		for i := 0; i < rng.IntN(30)+1; i++ {
			id := fmt.Sprint(i)
			in = append(in, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: "echo", Arguments: `{"private":"arguments"}`}}})
			m := llm.Message{Role: "tool", ToolCallID: id, Text: strings.Repeat("private evidence<&>\"", rng.IntN(120)), Source: &llm.ContextSource{Kind: "tool", Ref: "private-ref:" + id}}
			if rng.IntN(3) == 0 {
				m.ImageRefs = []string{"artifact:private-image-" + fmt.Sprint(rng.IntN(12)), "artifact:private-image-other"}
			}
			if rng.IntN(5) == 0 {
				m.Memory = &llm.MemoryText{Sealed: "private-ciphertext", Binding: "private-binding", EncodedBytes: 4000}
			}
			in = append(in, m)
		}
		in = append(in, llm.Message{Role: "user", Text: "private latest input", Source: &llm.ContextSource{Kind: "user", Protected: true}})
		p := contextbuilder.DefaultPolicy()
		p.FullWindowFallback = true
		p.Recent = 2 + rng.IntN(12)
		aggressive := trial%2 == 0
		b := contextbuilder.Budget{ModelBytes: 2000 + rng.IntN(40000), HardModelBytes: 65000, FrameBytes: 5000 + rng.IntN(120000)}
		before, _ := json.Marshal(in)
		expected, err1 := contextbuilder.Prepare(in, b, p, aggressive)
		f := contextFacts(in, b, p, aggressive)
		raw, _ := json.Marshal(f)
		if strings.Contains(string(raw), "private") {
			t.Fatal("policy received prompt, references or sealed memory", string(raw))
		}
		actual, err2 := applyContextPlan(in, b, p, aggressive, contextcompact.Select(f))
		if !errors.Is(err1, err2) || !errors.Is(err2, err1) {
			t.Fatalf("trial %d errors: %v / %v", trial, err1, err2)
		}
		if err1 == nil {
			// The omission receipt has no source-order semantic; compare its multiset.
			eOmit, aOmit := map[string]int{}, map[string]int{}
			for _, x := range expected.OmittedImages {
				eOmit[x]++
			}
			for _, x := range actual.OmittedImages {
				aOmit[x]++
			}
			if !reflect.DeepEqual(expected.Messages, actual.Messages) || !reflect.DeepEqual(expected.Sources, actual.Sources) || !reflect.DeepEqual(eOmit, aOmit) {
				t.Fatalf("trial %d default selection changed", trial)
			}
		}
		after, _ := json.Marshal(in)
		if string(before) != string(after) {
			t.Fatal("source mutation")
		}
	}
}

func TestContextPluginRejectsForgedImagesSplitGroupsAndOverBudgetPlans(t *testing.T) {
	in := []llm.Message{{Role: "system", Text: "Host rules"}, {Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "x", Name: "echo", Arguments: `{}`}}}, {Role: "tool", ToolCallID: "x", Text: strings.Repeat("large ", 2000), ImageRefs: []string{"artifact:a"}}, {Role: "user", Text: "latest", Source: &llm.ContextSource{Protected: true}}}
	p := contextbuilder.DefaultPolicy()
	b := contextbuilder.Budget{ModelBytes: 5000, FrameBytes: 20000}
	for _, plan := range []contract.Plan{
		{Images: [][]int{{}, {}, {1}, {}}, Through: 3},
		{Images: [][]int{{}, {}, {0, 0}, {}}, Through: 3},
		{Images: [][]int{{}, {}, {0}, {}}, Through: 2},
		{Images: [][]int{{}, {}, {0}, {}}, Through: 0},
		{Images: [][]int{{}, {}, {0}, {}}, Through: 5},
	} {
		if _, err := applyContextPlan(in, b, p, false, plan); err == nil {
			t.Fatal("unsafe plan accepted", plan)
		}
	}
	good := contract.Plan{Images: [][]int{{}, {}, {0}, {}}, Through: 4, Instructions: "summarize"}
	out, err := applyContextPlan(in, b, p, false, good)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sources) != 2 || out.Sources[0].Role != "assistant" || out.Messages[0].Text != "Host rules" || out.Messages[3].Text != "latest" {
		t.Fatal("protected input changed", out)
	}
}
