package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/task"
)

type FeedbackSettings interface {
	TaskFeedbacks(context.Context, string, string) (task.FeedbackPage, error)
	RespondTaskFeedbackInbound(context.Context, interaction.InboundEvent, string, string) (agent.Receipt, error)
}

type feedbackView struct {
	task.Feedback
	Artifacts []shownArtifact
	Draft     string
}
type attentionIssue struct{ Key, Title, Description, URL, Action string }
type attentionView struct {
	Feedbacks               []feedbackView
	Approvals               []approvalView
	Issues                  []attentionIssue
	Count                   int
	More, Unavailable       bool
	Next                    string
	ApprovalNext, TasksNext string
}

func (s *Server) loadAttention(ctx context.Context, p *page, ref, before string, approvals []interaction.Approval) {
	if s.Feedbacks == nil {
		return // Optional on older embedding/test hosts; production always wires it.
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v := &attentionView{}
	p.Attention = v
	feedbacks, err := s.Feedbacks.TaskFeedbacks(ctx, ref, before)
	if err != nil {
		v.Unavailable = true
	} else {
		v.More = feedbacks.Next != ""
		if feedbacks.Next != "" {
			q := url.Values{"feedback_before": {feedbacks.Next}}
			v.Next = "/tasks/attention?" + q.Encode()
		}
		for _, f := range feedbacks.Items {
			entry := feedbackView{Feedback: f}
			for _, artifactRef := range f.ArtifactRefs {
				d, err := s.Core.DescribeArtifact(ctx, artifactRef)
				if err == nil {
					entry.Artifacts = append(entry.Artifacts, shownArtifact{Ref: artifactRef, Name: d.Name, Previewable: artifactPreviewType(d) != ""})
				}
			}
			v.Feedbacks = append(v.Feedbacks, entry)
		}
	}
	if ref != "" && p.Tasks != nil {
		v.Approvals = p.Tasks.Approvals
		if p.Tasks.Detail != nil {
			addAttentionIssue(v, p.Tasks.Detail.Entry)
		}
	} else {
		if reader, ok := s.Feedbacks.(interface {
			PendingApprovals(context.Context) ([]interaction.Approval, error)
		}); ok {
			approvals, err = reader.PendingApprovals(ctx)
			if err != nil {
				v.Unavailable = true
			}
		} else if approvals == nil {
			approvals, err = s.Core.WebApprovals(ctx)
			if err != nil {
				v.Unavailable = true
			}
		}
		for _, a := range approvals {
			if a.Status != "pending" {
				continue
			}
			if len(v.Approvals) == 50 {
				v.More = true
				// Chat renders current approvals even when they have no Task or
				// their notification has left the latest conversation page.
				v.ApprovalNext = (&url.URL{Path: "/", Fragment: "approval-" + a.ID}).String()
				break
			}
			card := approvalFor(a)
			card.ReturnTo = "/tasks?view=attention"
			v.Approvals = append(v.Approvals, card)
		}
		if s.Tasks != nil {
			list, err := s.Tasks.List(ctx, task.ListQuery{View: "attention", Limit: 50})
			if err != nil {
				v.Unavailable = true
			} else {
				for _, entry := range list.Items {
					addAttentionIssue(v, entry)
				}
				if list.Next != "" {
					v.More = true
					v.TasksNext = "/tasks?view=attention#ongoing-work"
				}
			}
		}
	}
	v.Count = len(v.Feedbacks) + len(v.Approvals) + len(v.Issues)
}

func addAttentionIssue(v *attentionView, entry task.Entry) {
	if entry.Status != "active" && !(entry.Status == "paused" && entry.Progress.Unknown) {
		return
	}
	description := ""
	if entry.Progress.Unknown {
		description = "An external action has an unconfirmed outcome. Ask Ghost to check it before continuing."
	} else if entry.Progress.Root == "failed" {
		description = "This attempt failed. Review the task and discuss the next step with Ghost."
	} else if entry.Progress.Input {
		for _, f := range v.Feedbacks {
			if f.TaskRef == entry.TaskRef && f.Kind == "input" {
				return
			}
		}
		description = "Ghost needs more information. Open the task to see the latest update."
	}
	if description != "" {
		v.Issues = append(v.Issues, attentionIssue{Key: entry.TaskRef, Title: entry.Title, Description: description, URL: taskURL(entry.TaskRef), Action: "Open task"})
	}
}

func (s *Server) respondFeedback(w http.ResponseWriter, r *http.Request) error {
	if s.Feedbacks == nil {
		return task.ErrNotFound
	}
	ref, answer := r.FormValue("feedback_ref"), r.FormValue("answer")
	if !strings.HasPrefix(ref, "feedback:") || len(ref) > 256 || !task.ValidFeedbackAnswer(answer) {
		return task.ErrInvalid
	}
	source := r.FormValue("source_id")
	e := interaction.InboundEvent{Channel: "web", Subject: Subject, Destination: Destination, EventID: source, SourceID: source, Content: conversation.Text(answer)}
	receipt, err := s.Feedbacks.RespondTaskFeedbackInbound(r.Context(), e, ref, answer)
	if err != nil {
		return err
	}
	if s.Wake != nil {
		s.Wake()
	}
	target := safePageReturn(r.FormValue("return_to"))
	if r.Header.Get("Accept") == "application/json" {
		jsonResult(w, struct {
			Accepted  bool   `json:"accepted"`
			MessageID string `json:"message_id"`
		}{true, receipt.MessageID})
	} else {
		u, _ := url.Parse(target)
		q := u.Query()
		q.Set("notice", "feedback-saved")
		u.RawQuery = q.Encode()
		u.Fragment = "task-feedback-status"
		http.Redirect(w, r, u.String(), http.StatusSeeOther)
	}
	return nil
}
