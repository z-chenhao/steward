package feishu

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/interaction"

	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	feishuv1 "github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
	"github.com/z-chenhao/assistant/internal/task"
	"net/http/httptest"
	"strings"
	"testing"
)

type feedbackCore struct {
	Core
	err    error
	calls  int
	before func()
}

func (c *feedbackCore) RespondTaskFeedbackInbound(_ context.Context, e interaction.InboundEvent, ref, answer string) (agent.Receipt, error) {
	c.calls++
	if c.before != nil {
		c.before()
	}
	if ref != "feedback:one" || answer != "Accepted answer" || e.SourceID != "remote-card" || e.Destination != "chat" {
		panic("feedback scope changed")
	}
	return agent.Receipt{}, c.err
}

const feedbackEvent = `{"event":{"host":"im_message","operator":{"tenant_key":"tenant","open_id":"owner"},"context":{"open_message_id":"remote-card","open_chat_id":"chat"},"action":{"tag":"button","name":"steward_task_question_submit","value":{"feedback_ref":"feedback:one"},"form_value":{"answer":"Accepted answer"}}}}`

func TestFeedbackProjectionIsPreparedBeforeEffectAndOwnsAllResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
		wake int
	}{
		{"saved", nil, "v2 saved", 1}, {"unauthorized", interaction.ErrUnauthorized, "v2 invalid", 0}, {"missing", task.ErrNotFound, "v2 invalid", 0}, {"stale", task.ErrFeedbackStale, "v2 stale", 0}, {"unknown", errors.New("private diagnostic"), "v2 unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projected, wakes := 0, 0
			core := &feedbackCore{err: tc.err, before: func() {
				if projected != 1 {
					t.Fatal("effect preceded projection", projected)
				}
			}}
			client := fixtureClient()
			client.Config.TenantKey = "tenant"
			handler := Handler{Client: client, Core: core, Wake: func() { wakes++ }, ProjectCard: func(_ context.Context, f contract.CardFacts) (contract.CardProjection, error) {
				projected++
				out := feishuv1.ProjectCard(f)
				out.FeedbackCopy = &contract.FeedbackCopy{Invalid: "v2 invalid", Empty: "v2 empty", Saved: "v2 saved", Stale: "v2 stale", Unknown: "v2 unknown"}
				return out, nil
			}}
			response := httptest.NewRecorder()
			handler.taskQuestionAction(response, httptest.NewRequest("POST", "/feishu/callback", nil), "callback", []byte(feedbackEvent))
			if core.calls != 1 || projected != 1 || wakes != tc.wake || !strings.Contains(response.Body.String(), tc.want) || strings.Contains(response.Body.String(), "private diagnostic") {
				t.Fatal(response.Body.String(), core.calls, projected, wakes)
			}
		})
	}
}
func TestFeedbackUnavailableProjectionCannotCommitAnswer(t *testing.T) {
	for _, broken := range []string{"stopped", "missing-copy", "nul"} {
		t.Run(broken, func(t *testing.T) {
			core := &feedbackCore{}
			client := fixtureClient()
			client.Config.TenantKey = "tenant"
			handler := Handler{Client: client, Core: core, ProjectCard: func(_ context.Context, f contract.CardFacts) (contract.CardProjection, error) {
				out := feishuv1.ProjectCard(f)
				switch broken {
				case "stopped":
					return contract.CardProjection{}, errors.New("private plugin failure")
				case "missing-copy":
					out.FeedbackCopy = nil
				case "nul":
					out.FeedbackCopy.Saved = "bad\x00message"
				}
				return out, nil
			}}
			response := httptest.NewRecorder()
			handler.taskQuestionAction(response, httptest.NewRequest("POST", "/feishu/callback", nil), "callback", []byte(feedbackEvent))
			if core.calls != 0 || !strings.Contains(response.Body.String(), "temporarily unavailable") || strings.Contains(response.Body.String(), "private") {
				t.Fatal(response.Body.String(), core.calls)
			}
		})
	}
}
