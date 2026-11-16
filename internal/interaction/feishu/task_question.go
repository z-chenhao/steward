package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
	"github.com/z-chenhao/assistant/internal/task"
)

const taskQuestionSubmit = "steward_task_question_submit"

func taskFeedbackCard(ref, text, reviewURL string) map[string]any {
	out := legacyv1.ProjectCard(contract.CardFacts{Fallback: text, Feedback: true, Review: reviewURL != ""})
	return feedbackCard(ref, out.Text, reviewURL, out.Placeholder, out.SubmitLabel, out.LinkLabel)
}

// feedbackCard binds only Host-authorized URLs and callback references to the
// fixed external wire grammar. The plugin chooses presentation strings only.
func feedbackCard(ref, text, reviewURL, placeholder, submit, reviewLabel string) map[string]any {
	plain := func(text string) map[string]string { return map[string]string{"tag": "plain_text", "content": text} }
	elements := []any{map[string]any{"tag": "div", "text": plain(text)}}
	if reviewURL != "" {
		elements = append(elements, map[string]any{"tag": "button", "type": "default", "text": plain(reviewLabel), "behaviors": []any{map[string]string{"type": "open_url", "default_url": reviewURL}}})
	}
	// Both forms retain the published submit name. The receipt identifies the
	// persisted feedback kind, so callback data never selects the review role.
	elements = append(elements, map[string]any{"tag": "form", "name": "steward_task_question", "elements": []any{
		map[string]any{"tag": "input", "name": "answer", "required": true, "placeholder": plain(placeholder)},
		map[string]any{"tag": "button", "name": taskQuestionSubmit, "type": "primary", "text": plain(submit), "form_action_type": "submit",
			"behaviors": []any{map[string]any{"type": "callback", "value": map[string]string{"feedback_ref": ref}}}},
	}})
	return map[string]any{"schema": "2.0", "body": map[string]any{"elements": elements}}
}

func (h Handler) taskQuestionAction(w http.ResponseWriter, r *http.Request, eventID string, raw []byte) {
	toast := func(kind, text string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"toast": map[string]string{"type": kind, "content": text}})
	}
	// Freeze the complete response vocabulary before attempting the accepted
	// feedback effect. A failed presentation read cannot leave a newly committed
	// answer followed by a second plugin lookup that changes its reported outcome.
	ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer cancel()
	facts := contract.CardFacts{Components: []contract.CardComponent{}, Feedback: true}
	var out contract.CardProjection
	if h.ProjectCard == nil {
		out = legacyv1.ProjectCard(facts)
	} else {
		var err error
		out, err = h.ProjectCard(ctx, facts)
		if err != nil {
			toast("error", "Feedback is temporarily unavailable. Check Tasks.")
			return
		}
	}
	copy := out.FeedbackCopy
	if out.Mode != "feedback" || !validFeedbackCopy(copy) {
		toast("error", "Feedback is temporarily unavailable. Check Tasks.")
		return
	}
	var event struct {
		Event struct {
			Host     string `json:"host"`
			Operator struct {
				Tenant string `json:"tenant_key"`
				OpenID string `json:"open_id"`
			} `json:"operator"`
			Context struct {
				Message string `json:"open_message_id"`
				Chat    string `json:"open_chat_id"`
			} `json:"context"`
			Action struct {
				Tag   string `json:"tag"`
				Name  string `json:"name"`
				Value struct {
					Ref string `json:"feedback_ref"`
				} `json:"value"`
				Form struct {
					Answer string `json:"answer"`
				} `json:"form_value"`
			} `json:"action"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &event) != nil {
		toast("error", copy.Invalid)
		return
	}
	e := event.Event
	ref, answer := e.Action.Value.Ref, e.Action.Form.Answer
	if e.Host != "im_message" || e.Operator.Tenant != h.Client.Config.TenantKey || e.Operator.OpenID == "" ||
		e.Action.Tag != "button" || e.Action.Name != taskQuestionSubmit || !strings.HasPrefix(ref, "feedback:") || len(ref) > 256 {
		toast("error", copy.Invalid)
		return
	}
	if !task.ValidFeedbackAnswer(answer) {
		toast("error", copy.Empty)
		return
	}
	inbound := interaction.InboundEvent{Channel: "feishu", Subject: h.Client.Subject(e.Operator.OpenID), Destination: e.Context.Chat,
		EventID: eventID, SourceID: e.Context.Message, Content: conversation.Text(answer)}
	_, err := h.Core.RespondTaskFeedbackInbound(ctx, inbound, ref, answer)
	switch {
	case err == nil:
		if h.Wake != nil {
			h.Wake()
		}
		toast("success", copy.Saved)
	case errors.Is(err, interaction.ErrUnauthorized), errors.Is(err, task.ErrNotFound):
		toast("error", copy.Invalid)
	case errors.Is(err, task.ErrFeedbackStale), errors.Is(err, conversation.ErrConflict), errors.Is(err, agent.ErrConflict):
		toast("error", copy.Stale)
	default:
		toast("error", copy.Unknown)
	}
}
