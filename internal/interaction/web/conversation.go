package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
)

func (s *Server) showMessage(ctx context.Context, m conversation.Message, approvals map[string]interaction.Approval) (shownMessage, error) {
	out := shownMessage{Role: m.Role, Sequence: m.Sequence}
	if !m.CreatedAt.IsZero() {
		out.CreatedAt = m.CreatedAt.Format(time.RFC3339Nano)
	}
	if m.Source == "main" && m.Role == "assistant" && m.SourceEventID != "" && m.MessageID == "reply:"+m.SourceEventID {
		out.ReplyKey = replyKey(m.SourceEventID)
	}
	if m.Source == "web" && m.Role == "user" && len(m.SourceEventID) == 32 {
		if _, err := hex.DecodeString(m.SourceEventID); err == nil {
			out.ClientSource = m.SourceEventID
		}
	}
	// Match Host-authored notification identity, never strip arbitrary user text
	// by regex or rewrite the durable Conversation/frozen delivery projection.
	if m.Source == "security" {
		id, ok := strings.CutPrefix(m.MessageID, "approval:")
		if !ok {
			id, ok = strings.CutPrefix(m.MessageID, "spend:")
		}
		if a, found := approvals[id]; ok && found {
			v := approvalFor(a)
			out.Approval = &v
			return out, nil
		}
	}
	fallback := ""
	for _, c := range m.Content {
		if c.Type == "text" {
			fallback += c.Text + "\n"
		} else if c.ArtifactRef != "" {
			fallback += c.Type + ": " + c.ArtifactRef + "\n"
		}
	}
	raw, err := s.Core.WebProjection(ctx, m)
	if err != nil {
		return out, err
	}
	var grouped struct {
		Bubbles      []ui.Spec              `json:"bubbles"`
		FormDisplays map[string]ui.FormView `json:"form_displays"`
	}
	if json.Unmarshal(raw, &grouped) == nil && len(grouped.Bubbles) > 0 {
		for _, bubble := range grouped.Bubbles {
			data, err := json.Marshal(bubble)
			if err != nil {
				return out, err
			}
			out.Bubbles = append(out.Bubbles, shownProjection{HTML: ui.HTMLWithFormDisplays(data, bubble.Fallback, grouped.FormDisplays), Spec: string(data), Fallback: bubble.Fallback})
		}
	}
	// Strip the private display envelope before exposing the client spec.
	var spec ui.Spec
	clientRaw := raw
	if json.Unmarshal(raw, &spec) == nil && spec.SchemaVersion == ui.Version {
		clientRaw, err = json.Marshal(spec)
		if err != nil {
			return out, err
		}
	}
	out.Projection = shownProjection{HTML: ui.HTMLWithFormDisplays(clientRaw, fallback, grouped.FormDisplays), Spec: string(clientRaw), Fallback: fallback}
	return out, nil
}
func (s *Server) loadConversation(ctx context.Context, p *page) error {
	approvals, err := s.Core.WebApprovals(ctx)
	if err != nil {
		return err
	}
	indexed := map[string]interaction.Approval{}
	for _, a := range approvals {
		indexed[a.ID] = a
		if a.Status == "pending" {
			p.PendingCount++
			if p.FirstPending == "" {
				p.FirstPending = a.ID
			}
		}
	}
	messages, err := s.Core.Conversation(ctx, p.Cursor)
	if err != nil {
		return err
	}
	p.Before = messages.Before
	shown := map[string]bool{}
	for _, m := range messages.Messages {
		item, err := s.showMessage(ctx, m, indexed)
		if err != nil {
			return err
		}
		if item.Approval != nil {
			shown[item.Approval.ID] = true
		}
		p.Messages = append(p.Messages, item)
		p.Latest = max(p.Latest, m.Sequence)
	}
	for _, a := range approvals {
		if a.Status == "pending" && !shown[a.ID] {
			p.Approvals = append(p.Approvals, approvalFor(a))
		}
	}
	if p.Cursor != 0 {
		latest, err := s.Core.Conversation(ctx, 0)
		if err != nil {
			return err
		}
		for _, m := range latest.Messages {
			p.Latest = max(p.Latest, m.Sequence)
		}
	}
	s.loadAttention(ctx, p, "", "", approvals)
	return nil
}
