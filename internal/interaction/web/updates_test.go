package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/task"
)

type updatesCore struct {
	Core
	status    string
	allowed   bool
	failure   bool
	messages  []conversation.Message
	approvals []interaction.Approval
	cursors   []int64
}

func (c *updatesCore) CheckChannel(context.Context, string, string, string) error {
	if !c.allowed {
		return errors.New("revoked")
	}
	return nil
}
func (c *updatesCore) WebStatus(context.Context) (Status, error) {
	return Status{Readiness: "ready", Status: c.status}, nil
}
func (c *updatesCore) Conversation(_ context.Context, before int64) (conversation.Page, error) {
	c.cursors = append(c.cursors, before)
	if c.failure {
		return conversation.Page{}, errors.New("private storage diagnostic")
	}
	page := conversation.Page{}
	for _, m := range c.messages {
		if before == 0 || m.Sequence < before {
			page.Messages = append(page.Messages, m)
		}
	}
	return page, nil
}
func (c *updatesCore) WebApprovals(context.Context) ([]interaction.Approval, error) {
	return c.approvals, nil
}
func (c *updatesCore) WebProjection(_ context.Context, m conversation.Message) (json.RawMessage, error) {
	return json.Marshal(ui.Compose(delivery.Envelope{Message: m}))
}
func (c *updatesCore) Activity(context.Context) ([]task.Summary, error) {
	return []task.Summary{{TaskRef: "task:visible", Title: "Current work", Status: c.status}}, nil
}

func TestLiveProjectionUsesCommittedPagesStableETagsAndSafeHTML(t *testing.T) {
	c := &updatesCore{status: "busy", allowed: true, messages: []conversation.Message{{MessageID: "private-message-id", Sequence: 1, Role: "assistant", Content: conversation.Text(`<img src=x onerror="unsafe()">`)}}}
	c.approvals = []interaction.Approval{{ID: "approval:one", Status: "pending", Hash: strings.Repeat("a", 64), Tool: "builtin__write", Arguments: `{"path":"result.txt","content":"confirmed"}`}}
	s := &Server{Config: Config{TokenRef: "credential:test"}, Core: c, Secrets: fakeSecrets{}}
	s.WebWorkbench = workbenchTestReader(t)
	request := func(path, etag, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("If-None-Match", etag)
		r.Header.Set("X-Steward-Workbench-Reader", "sha256:"+strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	first := request("/api/updates?view=/", "", "private-token-at-least-24-bytes")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var value struct {
		Regions        map[string]string
		Latest, Before int64
	}
	if err := json.Unmarshal(first.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Latest != 1 || value.Before != 0 || len(value.Regions) != 4 {
		t.Fatal(value)
	}
	if strings.Contains(value.Regions["conversation-messages"], `<img src=x`) || !strings.Contains(value.Regions["conversation-messages"], "&lt;img") {
		t.Fatal("semantic content became HTML")
	}
	if !strings.Contains(value.Regions["conversation-approvals"], `name="hash"`) || !strings.Contains(value.Regions["conversation-approvals"], strings.Repeat("a", 64)) {
		t.Fatal("frozen approval missing")
	}
	if !strings.Contains(value.Regions["conversation-attention"], `href="/#approval-approval%3aone"`) || !strings.Contains(value.Regions["conversation-attention"], `<strong>1</strong>`) {
		t.Fatal("pending summary does not link to the displayed action", value.Regions["conversation-attention"])
	}
	for _, private := range []string{"private-message-id", "composer-root", "source_id", "claim_token", "provider_exchange_id"} {
		if strings.Contains(first.Body.String(), private) {
			t.Fatal("live view leaked or replaced private/input state", private)
		}
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing conditional identity")
	}
	if repeated := request("/api/updates?view=/", etag, "private-token-at-least-24-bytes"); repeated.Code != 304 || repeated.Body.Len() != 0 {
		t.Fatal("unchanged projection was retransmitted", repeated.Code)
	}
	c.status = "idle"
	c.approvals = nil
	updated := request("/api/updates?view=/", etag, "private-token-at-least-24-bytes")
	if updated.Code != 200 || updated.Header().Get("ETag") == etag {
		t.Fatal("status/approval changes did not invalidate view")
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &value); err != nil || value.Regions["conversation-attention"] != "" {
		t.Fatal("resolved actions remain in the attention summary", err)
	}
	if expired := request("/api/updates?view=/", etag, ""); expired.Code != 401 {
		t.Fatal("conditional request bypassed authentication")
	}
	c.allowed = false
	if revoked := request("/api/updates?view=/", etag, "private-token-at-least-24-bytes"); revoked.Code != 403 {
		t.Fatal("conditional request bypassed revocation")
	}
}

func TestPendingSummaryCountsInlineActionsOnceAndExcludesResolved(t *testing.T) {
	c := &updatesCore{approvals: []interaction.Approval{
		{ID: "inline", Status: "pending"},
		{ID: "separate", Status: "pending"},
		{ID: "resolved", Status: "approved"},
	}, messages: []conversation.Message{{MessageID: "approval:inline", Source: "security", Role: "assistant", Sequence: 1}}}
	s := &Server{Core: c}
	p := page{}
	if err := s.loadConversation(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	if p.PendingCount != 2 || p.FirstPending != "inline" || len(p.Approvals) != 1 || p.Approvals[0].ID != "separate" {
		t.Fatalf("inconsistent summary and inline/separate cards: %+v", p)
	}
	var body bytes.Buffer
	if err := layout.ExecuteTemplate(&body, "attention-summary", p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `href="/#approval-inline"`) || !strings.Contains(body.String(), `<strong>2</strong>`) {
		t.Fatal(body.String())
	}
}

func TestLiveHistoryCursorAndFailuresDoNotProducePartialUpdates(t *testing.T) {
	c := &updatesCore{allowed: true, messages: []conversation.Message{{Sequence: 2, Role: "user", Content: conversation.Text("older")}, {Sequence: 5, Role: "assistant", Content: conversation.Text("latest")}}}
	s := &Server{Core: c}
	w := httptest.NewRecorder()
	s.updates(w, httptest.NewRequest("GET", "/api/updates?view=/&before=4", nil))
	var result struct {
		Before, Latest int64
		Regions        map[string]string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Before != 4 || result.Latest != 5 || strings.Contains(result.Regions["conversation-messages"], "latest") || len(c.cursors) != 2 || c.cursors[0] != 4 || c.cursors[1] != 0 {
		t.Fatal("history cursor jumped to latest", result, c.cursors)
	}
	for _, path := range []string{"/api/updates?view=unknown", "/api/updates?view=/&before=-1", "/api/updates?view=/tasks&before=5"} {
		w = httptest.NewRecorder()
		s.updates(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	c.failure = true
	w = httptest.NewRecorder()
	s.updates(w, httptest.NewRequest("GET", "/api/updates?view=/", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") || w.Header().Get("ETag") != "" {
		t.Fatal("failed load emitted partial/private projection", w.Code, w.Body.String())
	}
}

func TestLiveProgressAndSettingsKeepPageQueryBoundaries(t *testing.T) {
	// These intentionally expose no Conversation/Approval/model query methods.
	for _, view := range []string{"/tasks", "/settings/models/new"} {
		s := &Server{Core: progressCore{}, Tasks: workspaceReader{}}
		w := httptest.NewRecorder()
		s.updates(w, httptest.NewRequest("GET", "/api/updates?view="+view, nil))
		if w.Code != 200 {
			t.Fatal(view, w.Code, w.Body.String())
		}
	}
}

type scopedApprovalsCore struct {
	*updatesCore
	ids []string
}

func (c *scopedApprovalsCore) WebApprovals(context.Context) ([]interaction.Approval, error) {
	return nil, errors.New("unbounded history requested")
}
func (c *scopedApprovalsCore) PageApprovals(_ context.Context, ids []string, includePending bool) ([]interaction.Approval, error) {
	c.ids = ids
	if !includePending {
		return nil, errors.New("missing pending actions")
	}
	return []interaction.Approval{{ID: "visible", Status: "approved"}, {ID: "other", Status: "pending"}}, nil
}
func TestChatRequestsOnlyHostAuthoredApprovalIDs(t *testing.T) {
	c := &scopedApprovalsCore{updatesCore: &updatesCore{messages: []conversation.Message{
		{MessageID: "approval:visible", Source: "security", Role: "assistant", Sequence: 1},
		{MessageID: "approval:forged", Source: "web", Role: "user", Sequence: 2},
	}}}
	s := &Server{Core: c}
	p := page{}
	if err := s.loadConversation(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	if len(c.ids) != 1 || c.ids[0] != "visible" || p.Messages[0].Approval == nil || p.PendingCount != 1 || len(p.Approvals) != 1 {
		t.Fatal(c.ids, p.Messages, p.Approvals)
	}
}
