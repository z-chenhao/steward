package context

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
)

func TestSummaryPromptBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	anchors := []llm.Message{{Role: "user", Text: "Keep constraints", Source: &llm.ContextSource{Kind: "task", Protected: true}}}
	sources := []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: `{}`}}}, {Role: "tool", ToolCallID: "call-1", Text: "confirmed result"}}
	for _, tc := range []struct {
		target int
		hash   string
	}{{256, "f3e5e2cec04001cbcf4f91a22efe540e5b3273264729a5474b42e475f534f70d"}, {2048, "820c398c68a941c734a2f4c3d2aa746a81c200c58e8103cb47e3eb5cbe168a76"}, {16384, "b81e53b94d14c7babbe07a6d07fe629628c333aabe26e4c9e83e2dbe25974011"}} {
		t.Run(fmt.Sprint(tc.target), func(t *testing.T) {
			messages, n, err := SummaryRequest(sources, anchors, 10000, tc.target)
			if err != nil || n != len(sources) {
				t.Fatal(n, err)
			}
			raw, _ := json.Marshal(messages)
			got := fmt.Sprintf("%x", sha256.Sum256(raw))
			if got != tc.hash {
				t.Fatalf("summary request bytes changed: %s", got)
			}
		})
	}
}

func TestWorkingPreparationPreservesTaskAndUnknownFacts(t *testing.T) {
	anchor := llm.Message{Role: "user", Text: strings.Repeat("task background ", 150) + "DO_NOT_PUBLISH\nartifact:input", Source: &llm.ContextSource{Kind: "task", Protected: true}}
	in := []llm.Message{{Role: "system", Text: "rules"}, anchor, {Role: "user", Origin: "runtime", Text: "unknown external outcome; do not repeat", Source: &llm.ContextSource{Kind: "unknown_effect", Protected: true}}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprint(i)
		in = append(in, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: "echo", Arguments: `{}`}}}, llm.Message{Role: "tool", ToolCallID: id, Text: strings.Repeat("result ", 200)})
	}
	before := append([]llm.Message(nil), in...)
	r, e := Prepare(in, Budget{ModelBytes: 7000, FrameBytes: 7000}, DefaultPolicy(), false)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Sources) == 0 || !reflect.DeepEqual(before, in) {
		t.Fatal("source mutation or no semantic compaction")
	}
	for _, m := range r.Sources {
		if m.Source != nil && m.Source.Protected {
			t.Fatal("required fact selected for removal")
		}
	}
	request, n, e := SummaryRequest(r.Sources, []llm.Message{anchor}, 6500, 2048)
	if e != nil {
		t.Fatal(e)
	}
	if n == 0 || n%2 != 0 || Size(request) > 6500 || !strings.Contains(request[1].Text, "DO_NOT_PUBLISH") || len(request[0].ToolCalls) > 0 {
		t.Fatal("summary input lost scope or split exchange", n, e)
	}
}

func TestWorkingPreparationBudgetsPixelsAndTransport(t *testing.T) {
	in := []llm.Message{{Role: "system", Text: "rules"}}
	for i := 0; i < 9; i++ {
		in = append(in, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: fmt.Sprint(i), Name: "observe", Arguments: `{}`}}}, llm.Message{Role: "tool", ToolCallID: fmt.Sprint(i), ImageRefs: []string{fmt.Sprintf("artifact:%d", i)}, Text: "screenshot reference"})
	}
	r, e := Prepare(in, Budget{ModelBytes: 10000, FrameBytes: 10000}, DefaultPolicy(), false)
	if e != nil {
		t.Fatal(e)
	}
	images := []string{}
	for _, m := range r.Messages {
		images = append(images, m.ImageRefs...)
	}
	if len(images) != 8 || images[0] != "artifact:1" || images[7] != "artifact:8" || len(in[2].ImageRefs) != 1 {
		t.Fatal("incorrect image retention", images)
	}
	repeated := append(append([]llm.Message(nil), in...), llm.Message{Role: "user", Origin: "runtime", ImageRefs: []string{"artifact:8"}, Text: "latest selection"})
	dedup, e := Prepare(repeated, Budget{ModelBytes: 10000, FrameBytes: 10000}, DefaultPolicy(), false)
	if e != nil {
		t.Fatal(e)
	}
	occurrences := 0
	for _, m := range dedup.Messages {
		for _, ref := range m.ImageRefs {
			if ref == "artifact:8" {
				occurrences++
			}
		}
	}
	if occurrences != 1 {
		t.Fatal("repeated image pixels kept in historical messages", occurrences)
	}
	retry, e := Prepare(in, Budget{ModelBytes: 10000, FrameBytes: 10000}, DefaultPolicy(), true)
	if e != nil {
		t.Fatal(e)
	}
	remaining := 0
	for _, m := range retry.Messages {
		remaining += len(m.ImageRefs)
	}
	if remaining != 4 {
		t.Fatal("context retry did not reduce image load", remaining)
	}
	budget := BudgetFor(1048576, 65536, 1024, DefaultPolicy(), false)
	if budget.FrameBytes != 384<<10-1024 || BudgetFor(8192, 8192, 0, DefaultPolicy(), false).ModelBytes > 0 {
		t.Fatal("transport/output bounds ignored", budget)
	}
	large := []llm.Message{{Role: "system", Text: "rules"}}
	for i := 0; i < 24; i++ {
		large = append(large, llm.Message{Role: "user", Text: strings.Repeat("x", 48<<10)})
	}
	got, e := Prepare(large, budget, DefaultPolicy(), false)
	if e != nil || len(got.Sources) == 0 {
		t.Fatal("oversize request passed admission", e)
	}
}

func TestLargeRuntimeEvidenceCanBeSummarizedWithoutDroppingTask(t *testing.T) {
	in := []llm.Message{{Role: "system", Text: "rules"}, {Role: "user", Text: "deliver the final report", Source: &llm.ContextSource{Kind: "task", Protected: true}}, {Role: "user", Origin: "runtime", Source: &llm.ContextSource{Kind: "task_result", Ref: "trace:result"}, Text: strings.Repeat("business evidence ", 4000)}}
	r, e := Prepare(in, Budget{ModelBytes: 10000, FrameBytes: 10000}, DefaultPolicy(), false)
	if e != nil || len(r.Sources) != 1 {
		t.Fatal("large final evidence prevented context recovery", e)
	}
	m, n, e := SummaryRequest(r.Sources, in[1:2], 9000, 2048)
	if e != nil || n != 1 || Size(m) > 9000 || !strings.Contains(m[1].Text, "excerpt truncated") {
		t.Fatal("runtime payload was not safely bounded", n, e)
	}
}

func TestWorkingBudgetSeparatesHostProvenanceFromModelInput(t *testing.T) {
	plain := []llm.Message{{Role: "user", Text: "current request"}}
	withSource := []llm.Message{{Role: "user", Text: "current request", Origin: "user", Source: &llm.ContextSource{Kind: "conversation", Ref: strings.Repeat("provenance", 1000), Protected: true}}}
	if ModelSize(plain) != ModelSize(withSource) {
		t.Fatal("Host metadata consumed model tokens")
	}
	budget := Budget{ModelBytes: ModelSize(plain), FrameBytes: Size(withSource)}
	if !budget.Fits(withSource) {
		t.Fatal("model and frame budgets were conflated")
	}
	budget.FrameBytes--
	if budget.Fits(withSource) {
		t.Fatal("Host metadata escaped transport admission")
	}
}

func TestLayeredMemoryBudgetAccountsForExpandedEscapedText(t *testing.T) {
	plain := strings.Repeat("\\\"<", 4000)
	envelope := &llm.MemoryText{Sealed: strings.Repeat("x", len(plain)*4/3), Binding: "test", EncodedBytes: llm.MemoryEncodedBytes(plain)}
	messages := []llm.Message{{Role: "user", Text: "current", Memory: envelope}}
	wire := []llm.Message{{Role: "user", Text: "current" + plain}}
	if ModelSize(messages) < ModelSize(wire) {
		t.Fatal("expanded JSON escaped bytes escaped admission")
	}
	budget := Budget{ModelBytes: ModelSize(messages) - 1, FrameBytes: Size(messages)}
	if budget.Fits(messages) {
		t.Fatal("expanded memory escaped model budget")
	}
	envelope.EncodedBytes = -1
	if Size(messages) < 1<<20 {
		t.Fatal("invalid hint reduced memory admission size")
	}
}

func TestContextFullWindowFallbackDistinguishesTargetFromHardCapacity(t *testing.T) {
	p := DefaultPolicy()
	p.FullWindowFallback = true
	input := []llm.Message{{Role: "system", Text: strings.Repeat("x", 19000)}, {Role: "user", Text: "current"}}
	budget := BudgetFor(8192, 1024, 0, p, false)
	out, err := Prepare(input, budget, p, false)
	if err != nil || len(out.Sources) != 0 || out.Messages[0].Text != input[0].Text {
		t.Fatal("soft threshold rejected required input within the real window", err)
	}
	input[0].Text = strings.Repeat("x", 22000)
	if _, err = Prepare(input, budget, p, false); err != llm.ContextLengthExceeded {
		t.Fatal("hard model capacity bypassed", err)
	}
	input[0].Text = strings.Repeat("x", 19000)
	if _, err = Prepare(input, BudgetFor(8192, 1024, 0, p, true), p, true); err != llm.ContextLengthExceeded {
		t.Fatal("provider context retry bypassed its smaller budget", err)
	}
	p.FullWindowFallback = false
	if _, err = Prepare(input, BudgetFor(8192, 1024, 0, p, false), p, false); err != llm.ContextLengthExceeded {
		t.Fatal("historical policy changed", err)
	}
}

func TestContextModelBudgetCountsExpandedMemoryWithoutCiphertext(t *testing.T) {
	plain := "precise \"quote\" <markup>\\n" + strings.Repeat("body ", 900)
	expanded := []llm.Message{{Role: "tool", Text: "public prefix: " + plain}}
	sealed := []llm.Message{{Role: "tool", Text: "public prefix: ", Memory: &llm.MemoryText{Sealed: strings.Repeat("ciphertext", 900), Binding: "memory-read:run:call", EncodedBytes: llm.MemoryEncodedBytes(plain)}}}
	if ModelSize(sealed) != ModelSize(expanded) {
		t.Fatal("model capacity counted Host ciphertext", ModelSize(sealed), ModelSize(expanded))
	}
	if Size(sealed) <= ModelSize(sealed) {
		t.Fatal("RPC capacity lost sealed envelope headroom")
	}
	budget := Budget{ModelBytes: ModelSize(expanded), FrameBytes: Size(sealed)}
	if _, err := Prepare(sealed, budget, DefaultPolicy(), false); err != nil {
		t.Fatal("unnecessary compaction of a fitting exact read", err)
	}
	sealed[0].Memory.EncodedBytes = 1
	if ModelSize(sealed) < 1<<20 {
		t.Fatal("invalid expanded-size claim reduced the budget")
	}
}
