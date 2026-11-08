package context

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/prompt"
)

type Budget struct {
	HardModelBytes int
	ModelBytes     int
	FrameBytes     int
}

// BudgetFor separates provider-visible input from Host provenance on the RPC
// frame. Both retain output and media headroom; metadata is not model tokens.
func BudgetFor(window, output int64, tools int, p Policy, aggressive bool) Budget {
	tokens := min(int64(float64(window)*p.Threshold), window-output)
	model := int(tokens*3) - tools
	if aggressive {
		model /= 2
	}
	hard := 0
	if p.FullWindowFallback && !aggressive {
		hard = int((window-output)*3) - tools
	}
	return Budget{ModelBytes: model, HardModelBytes: hard, FrameBytes: (384 << 10) - tools}
}

func ModelSize(in []llm.Message) int {
	projected := make([]llm.Message, len(in))
	expanded := 0
	for i, m := range in {
		m.Source = nil
		m.Origin = ""
		m.ImageRefs = nil
		if m.Memory != nil {
			if m.Memory.EncodedBytes < 2 || m.Memory.EncodedBytes > 1<<20 {
				return 1 << 20
			}
			// JSON string concatenation is additive after removing one pair of quotes.
			// The ciphertext/resolver envelope belongs to the Host frame, not tokens.
			expanded += m.Memory.EncodedBytes - 2
			m.Memory = nil
		}
		projected[i] = m
	}
	raw, _ := json.Marshal(projected)
	return len(raw) + expanded
}

func (b Budget) Fits(in []llm.Message) bool {
	return ModelSize(in) <= b.ModelBytes && Size(in) <= b.FrameBytes
}

type Preparation struct {
	Messages      []llm.Message
	Sources       []llm.Message
	OmittedImages []string
}

// Prepare selects a working set without rewriting the source transcript.
// Over-budget history is handed to the same Agent Loop for a tool-free model
// summary; required task inputs and unknown-effect facts remain exact.
func Prepare(in []llm.Message, budget Budget, p Policy, aggressive bool) (Preparation, error) {
	out, err := prepare(in, budget, p, aggressive)
	// The compaction target is not the model's hard capacity. Required input may
	// exceed that target, while still fitting after reserving output and tools.
	// A provider-context retry stays aggressive; never resend its rejected frame.
	if err == llm.ContextLengthExceeded && p.FullWindowFallback && !aggressive && budget.HardModelBytes > budget.ModelBytes {
		budget.ModelBytes = budget.HardModelBytes
		return prepare(in, budget, p, aggressive)
	}
	return out, err
}
func prepare(in []llm.Message, budget Budget, p Policy, aggressive bool) (Preparation, error) {
	if !p.Valid() || budget.ModelBytes <= 0 || budget.FrameBytes <= 0 {
		return Preparation{}, llm.ContextLengthExceeded
	}
	out := Preparation{Messages: append([]llm.Message(nil), in...)}
	// Pixels have an independent budget. Keep the newest explicitly selected
	// images; old Artifact references remain in each message's text.
	seen := map[string]bool{}
	imageLimit := llm.MaxInputImages
	if aggressive {
		imageLimit = 4
	}
	for i := len(out.Messages) - 1; i >= 0; i-- {
		m := out.Messages[i]
		images := []string{}
		for _, ref := range m.ImageRefs {
			if !seen[ref] && len(seen) < imageLimit {
				seen[ref] = true
				images = append(images, ref)
			} else {
				out.OmittedImages = append(out.OmittedImages, ref)
			}
		}
		if len(images) != len(m.ImageRefs) {
			m.Text += "\nHistorical image pixels omitted; reopen the scoped Artifact if needed."
			m.ImageRefs = images
		}
		out.Messages[i] = m
	}
	if budget.Fits(out.Messages) {
		return out, nil
	}
	latest := -1
	for i := len(in) - 1; i >= 0; i-- {
		if IsInput(in[i]) {
			latest = i
			break
		}
	}
	cut := max(0, len(in)-p.Recent)
	for cut > 0 && cut < len(in) && in[cut].Role == "tool" {
		cut--
	}
	// Keep at least the current exchange and current explicit input. Larger
	// recent tails can be summarized when they prevent forward progress.
	for {
		out.Sources = nil
		kept := []llm.Message{}
		for i, m := range out.Messages {
			if i < cut && m.Role != "system" && !protected(m) && i != latest && (m.Source == nil || m.Source.Kind != "recall") {
				out.Sources = append(out.Sources, m)
			} else {
				kept = append(kept, m)
			}
		}
		reserve := min(p.SummaryBytes+512, max(1024, budget.ModelBytes/4))
		if len(out.Sources) > 0 && ModelSize(kept)+reserve <= budget.ModelBytes && Size(kept)+reserve+200 <= budget.FrameBytes {
			return out, nil
		}
		if cut >= len(in) {
			return Preparation{}, llm.ContextLengthExceeded
		}
		cut++
		for cut < len(in) && in[cut].Role == "tool" {
			cut++
		}
	}
}

func protected(m llm.Message) bool { return m.Source != nil && m.Source.Protected }

// IsInput excludes runtime evidence and metadata, rather than guessing from a
// textual prefix. Task text and references form one explicit logical input.
func IsInput(m llm.Message) bool {
	return m.Role == "user" && m.Origin != "runtime"
}

const SummaryEvidenceMarker = "Evidence to summarize:\n"

// SummaryRequest serializes evidence into one user payload, so historical tool
// calls cannot execute and their original roles and sources remain explicit.
func SummaryRequest(sources, anchors []llm.Message, budget, target int) ([]llm.Message, int, error) {
	prefix := prompt.SummaryInstructions(target)
	if len(anchors) > 0 {
		raw, _ := json.Marshal(anchors)
		prefix += "Protected task inputs and Host facts (also retained verbatim in the live context):\n" + string(raw) + "\n"
	}
	prefix += SummaryEvidenceMarker
	base := []llm.Message{{Role: "system", Text: prompt.SummarySystem()}, {Role: "user", Text: prefix}}
	selected := []llm.Message{}
	for i := 0; i < len(sources); {
		end := i + 1
		if len(sources[i].ToolCalls) > 0 {
			for end < len(sources) && sources[end].Role == "tool" {
				end++
			}
		}
		group := []llm.Message{}
		for _, original := range sources[i:end] {
			m := original
			// Bound individual tool payloads while retaining the originals in the
			// authorized archive. Summary text and user instructions stay intact.
			evidence := m.Role == "tool" || m.Origin == "runtime" && m.Source != nil && m.Source.Kind != "working_state" && m.Source.Kind != "previous_main_state" && m.Source.Kind != "task_instruction"
			if evidence && len(m.Text) > 2400 {
				m.Text = clipText(m.Text, 2400) + "\n[excerpt truncated; original retained]"
			}
			m.ImageRefs = nil
			if len(m.ToolCalls) > 0 {
				m.ToolCalls = append([]llm.ToolCall(nil), m.ToolCalls...)
				for j := range m.ToolCalls {
					if len(m.ToolCalls[j].Arguments) > 2400 {
						m.ToolCalls[j].Arguments = clipText(m.ToolCalls[j].Arguments, 2400) + " [excerpt truncated]"
					}
				}
			}
			group = append(group, m)
		}
		candidate := append(append([]llm.Message(nil), selected...), group...)
		raw, _ := json.Marshal(candidate)
		base[1].Text = prefix + string(raw)
		if Size(base) > budget {
			if len(selected) == 0 {
				return nil, 0, llm.ContextLengthExceeded
			}
			break
		}
		selected = candidate
		i = end
	}
	if len(selected) == 0 {
		return nil, 0, llm.ContextLengthExceeded
	}
	raw, _ := json.Marshal(selected)
	base[1].Text = prefix + string(raw)
	return base, len(selected), nil
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
