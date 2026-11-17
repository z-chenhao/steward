package feishu

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"strconv"
	"strings"
	"unicode/utf8"
)

type CardProjector func(context.Context, contract.CardFacts) (contract.CardProjection, error)

func cardFacts(spec ui.Spec, linked, feedback, review bool) contract.CardFacts {
	f := contract.CardFacts{Fallback: spec.Fallback, Components: []contract.CardComponent{}, Linked: linked, Feedback: feedback, Review: review}
	if spec.SchemaVersion != ui.Version {
		return f
	}
	for _, c := range spec.Components {
		item := contract.CardComponent{Valid: ui.ValidComponent(c), Kind: c.Type, Text: c.Text, Columns: append([]string{}, c.Columns...), Rows: append([][]string{}, c.Rows...), ApprovalID: c.ApprovalID, Hash: c.Hash, Fields: []contract.CardField{}}
		for _, field := range c.Fields {
			item.Fields = append(item.Fields, contract.CardField{Label: field.Label, Value: field.Value})
		}
		f.Components = append(f.Components, item)
	}
	return f
}
func (a Adapter) prepareCard(ctx context.Context, e delivery.Envelope, source, feedback, reviewURL string) (projection, error) {
	spec := ui.Compose(e)
	linked := a.Client.Config.WorkspaceURL != "" && e.Message.Sequence > 0 && e.Request.Intent != "approval"
	facts := cardFacts(spec, linked, feedback != "", reviewURL != "")
	if feedback != "" {
		facts.Fallback = e.Message.Content[0].Text
		facts.Components = []contract.CardComponent{}
	}
	out, err := a.ProjectCard(ctx, facts)
	if err != nil {
		return projection{}, err
	}
	return bindCard(spec, out, facts, a.Client.Config.WorkspaceURL, e.Message.Sequence, source, feedback, reviewURL)
}
func bindCard(spec ui.Spec, out contract.CardProjection, facts contract.CardFacts, workspace string, sequence int64, source, feedback, reviewURL string) (projection, error) {
	for _, s := range []string{out.Text, out.LinkLabel, out.Placeholder, out.SubmitLabel} {
		if !utf8.ValidString(s) || len(s) > 1<<20 || strings.ContainsRune(s, 0) {
			return projection{}, errors.New("invalid Feishu card presentation")
		}
	}
	if (facts.Feedback && out.Mode != "feedback") || (!facts.Feedback && out.Mode != "plain" && !(facts.Linked && out.Mode == "linked")) {
		return projection{}, errors.New("invalid Feishu card mode")
	}
	plain := func(s string) map[string]string { return map[string]string{"tag": "plain_text", "content": s} }
	card := map[string]any{"config": map[string]any{"wide_screen_mode": true}, "elements": []any{map[string]any{"tag": "div", "text": plain(out.Text)}}}
	include := true
	if out.Mode == "linked" {
		link := strings.TrimRight(workspace, "/") + "/?before=" + strconv.FormatInt(sequence+1, 10) + "#message-" + strconv.FormatInt(sequence, 10)
		card["elements"] = append(card["elements"].([]any), map[string]any{"tag": "action", "actions": []any{map[string]any{"tag": "button", "text": plain(out.LinkLabel), "type": "default", "url": link}}})
		include = false
	}
	if out.Mode == "feedback" {
		if !validFeedbackCopy(out.FeedbackCopy) {
			return projection{}, errors.New("invalid Feishu feedback presentation")
		}
		card = feedbackCard(feedback, out.Text, reviewURL, out.Placeholder, out.SubmitLabel, out.LinkLabel)
		include = false
	}
	return projection{Spec: spec, Transport: &transportProjection{Version: 1, ReplySource: source, Card: card, IncludeArtifacts: include}}, nil
}

func validFeedbackCopy(c *contract.FeedbackCopy) bool {
	if c == nil {
		return false
	}
	for _, s := range []string{c.Invalid, c.Empty, c.Saved, c.Stale, c.Unknown} {
		if s == "" || len(s) > 4096 || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return false
		}
	}
	return true
}
