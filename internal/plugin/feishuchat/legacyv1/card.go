// Historical card policy for graphs that never activated the card service.
package legacyv1

import (
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"strings"
)

func ProjectCard(f contract.CardFacts) contract.CardProjection {
	out := contract.CardProjection{Mode: "plain", Text: f.Fallback}
	if f.Feedback {
		out.FeedbackCopy = &contract.FeedbackCopy{
			Invalid: "This feedback request could not be opened.",
			Empty:   "Enter valid feedback before submitting.",
			Saved:   "Feedback saved. Ghost will review it.",
			Stale:   "This request changed or already has a different response. Check Tasks.",
			Unknown: "The response could not be confirmed. Check Tasks before trying again.",
		}
		out.Mode = "feedback"
		out.Placeholder = "Your answer"
		out.SubmitLabel = "Send answer"
		if f.Review {
			out.LinkLabel = "Open result in Steward"
			out.Placeholder = "Your review feedback"
			out.SubmitLabel = "Send review feedback"
		}
		return out
	}
	if f.Linked {
		out.Mode = "linked"
		text := []rune(f.Fallback)
		if len(text) > 1200 {
			text = append(text[:1200], '…')
		}
		out.Text = string(text)
		out.LinkLabel = "Open full response and files"
		return out
	}
	parts := []string{}
	for _, c := range f.Components {
		if !c.Valid {
			parts = append(parts, f.Fallback)
			continue
		}
		switch c.Kind {
		case "markdown", "progress", "openintelligentui":
			parts = append(parts, c.Text)
		case "table":
			parts = append(parts, strings.Join(c.Columns, " | "))
			for _, row := range c.Rows {
				parts = append(parts, strings.Join(row, " | "))
			}
		case "confirmation":
			parts = append(parts, c.Text+"\nApproval: "+c.ApprovalID+"\nHash: "+c.Hash+"\nReply /approve ID HASH or /deny ID HASH")
		case "form":
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
			for _, field := range c.Fields {
				parts = append(parts, field.Label+": "+field.Value)
			}
		}
	}
	if len(parts) > 0 {
		out.Text = strings.Join(parts, "\n")
	}
	return out
}
