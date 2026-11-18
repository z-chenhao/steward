package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"

	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	feishuv1 "github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"
)

func TestReplyUsesOnlyEscapedFrozenSourceAndNeverFallsBack(t *testing.T) {
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.EscapedPath() != "/open-apis/im/v1/messages/om_source%3F%23/reply" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer access-token" {
			t.Error("reply escaped original scope", r.URL)
		}
		var q map[string]any
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		if len(q) != 4 || q["uuid"] != "stable-key" || q["msg_type"] != "text" || q["reply_in_thread"] != false || q["content"] != `{"text":"safe"}` {
			t.Error("wrong reply wire contract", q)
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"remote"}}`))
		} else {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":230011}`))
		}
	}))
	defer peer.Close()
	c := fixtureClient()
	c.Config.APIBase = peer.URL
	id, err := c.Reply(context.Background(), "om_source?#", "stable-key", "text", map[string]string{"text": "safe"})
	if err != nil || id != "remote" {
		t.Fatal(id, err)
	}
	if _, err = c.Reply(context.Background(), "om_source?#", "stable-key", "text", map[string]string{"text": "safe"}); err == nil || calls != 2 {
		t.Fatal("missing source fell back or was treated as success", err, calls)
	}
	for _, source := range []string{"", "..", "om/other", "om\\other", "om\nother"} {
		if _, err = c.Reply(context.Background(), source, "key", "text", nil); err == nil || calls != 2 {
			t.Fatal("invalid source reached transport", source)
		}
	}
}

func TestLinkedProjectionKeepsFullSemanticsAndApprovalArguments(t *testing.T) {
	e := delivery.Envelope{Request: delivery.Request{Intent: "completion"}, Message: conversation.Message{Sequence: 147, Content: []conversation.ContentPart{{Type: "text", Text: strings.Repeat("完整结论", 1000)}, {Type: "file", ArtifactRef: "artifact:report"}}}}
	p := composeProjection(e, "https://workspace.example.test/", "om_origin")
	if len([]rune(p.Transport.Card["elements"].([]any)[0].(map[string]any)["text"].(map[string]string)["content"])) != 1201 || !strings.Contains(p.Fallback, strings.Repeat("完整结论", 1000)) || p.Transport.IncludeArtifacts {
		t.Fatal("projection lost semantics or repeated detailed files")
	}
	raw, _ := json.Marshal(p.Transport.Card)
	if !strings.Contains(string(raw), "/?before=148#message-147") {
		t.Fatal(string(raw))
	}
	full := composeProjection(e, "", "om_origin")
	if !full.Transport.IncludeArtifacts || full.Transport.Card == nil {
		t.Fatal("unlinked result cannot retain complete delivery")
	}
	e.Request.Intent = "approval"
	approval := composeProjection(e, "https://workspace.example.test", "om_origin")
	if !approval.Transport.IncludeArtifacts || strings.Contains(string(mustJSON(t, approval.Transport.Card)), "Open full response") {
		t.Fatal("approval arguments were shortened")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWorkspaceLinkScopeUsesConfiguredOriginWithoutDisclosingIt(t *testing.T) {
	c := fixtureClient()
	for origin, want := range map[string]string{
		"": "", "http://127.0.0.1:8787": "same_device_only", "https://[::1]/": "same_device_only",
		"https://127.14.1.2": "same_device_only", "https://[::ffff:127.0.0.2]": "same_device_only",
		"https://LOCALHOST.": "same_device_only", "https://steward.localhost": "same_device_only",
		"https://localhost.team.example": "configured_https", "https://work.example/": "configured_https",
	} {
		c.Config.WorkspaceURL = origin
		facts := c.ConnectionFacts()
		if facts.WorkspaceLinkScope != want {
			t.Errorf("%q: got %q, want %q", origin, facts.WorkspaceLinkScope, want)
		}
		if origin != "" && strings.Contains(string(mustJSON(t, facts)), origin) {
			t.Errorf("reported raw origin %q", origin)
		}
	}
}

func TestWorkspaceOriginRejectsCredentialAndNavigationInjection(t *testing.T) {
	c := fixtureClient().Config
	c.EncryptKeyRef, c.VerificationTokenRef, c.AccessTokenRef = "env:KEY", "env:VERIFY", "env:ACCESS"
	for _, base := range []string{"https://workspace.example.test", "http://127.0.0.1:8787", "http://[::1]:8787/", ""} {
		c.WorkspaceURL = base
		if err := c.Validate(); err != nil {
			t.Fatal(base, err)
		}
	}
	for _, base := range []string{"http://remote.example.test", "https://user:secret@workspace.example.test", "https://workspace.example.test?token=secret", "https://workspace.example.test?", "https://workspace.example.test#fragment", "https://workspace.example.test/#", "https://workspace.example.test/path", "javascript:alert(1)"} {
		c.WorkspaceURL = base
		if err := c.Validate(); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe workspace URL accepted/leaked", base, err)
		}
	}
}

type projectionStore struct {
	raw             json.RawMessage
	source, receipt string
	hash            string
	saves           int
	saveErr         error
}

func (s *projectionStore) DeliveryBinding(context.Context, delivery.Request) (interaction.Binding, error) {
	return interaction.Binding{Subject: "tenant:app:owner", Destination: "chat", Enabled: true}, nil
}
func (s *projectionStore) DeliveryReplySource(context.Context, delivery.Request) (string, error) {
	return s.source, nil
}
func (s *projectionStore) FeishuTaskFeedbackCard(context.Context, string) (string, string, bool, error) {
	return "", "", false, nil
}
func (s *projectionStore) LoadProjection(_ context.Context, _ string, hash string) (json.RawMessage, bool, error) {
	if s.raw == nil {
		return nil, false, nil
	}
	if s.hash != hash {
		return nil, true, delivery.ErrConflict
	}
	return s.raw, true, nil
}
func (s *projectionStore) SaveProjection(_ context.Context, _ string, hash string, _ int, spec any) (json.RawMessage, error) {
	s.saves++
	if s.saveErr != nil {
		return nil, s.saveErr
	}
	if s.raw == nil {
		s.raw, _ = json.Marshal(spec)
		s.hash = hash
	}
	if s.hash != hash {
		return nil, delivery.ErrConflict
	}
	return s.raw, nil
}
func (s *projectionStore) ChannelReceipt(context.Context, string, int, string) (string, error) {
	return s.receipt, nil
}
func (s *projectionStore) AcceptChannelReceipt(_ context.Context, _ string, _ int, _ string, remote string) error {
	s.receipt = remote
	return nil
}

func TestDeliveryProjectionFreezesLinkAndPreservesLegacyCard(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmtBool(legacy), func(t *testing.T) {
			store := &projectionStore{source: "om_source"}
			r := delivery.Request{DeliveryID: "delivery:message", IdempotencyKey: "delivery:message", MessageID: "message", ConversationID: "owner", Channel: "feishu", DestinationRef: "chat", Intent: "completion", CreatedAt: time.Now().UnixMicro()}
			e := delivery.Envelope{Request: r, Message: conversation.Message{MessageID: r.MessageID, ConversationID: r.ConversationID, Role: "assistant", Sequence: 42, Content: conversation.Text(strings.Repeat("Full reply ", 200))}}
			if legacy {
				store.source = ""
				store.raw = mustJSON(t, ui.Compose(e))
				store.hash = ui.Hash(e.Message)
			}
			calls := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var q struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
				}
				if legacy {
					if strings.Contains(q.Content, "Open full response") || !strings.Contains(q.Content, strings.Repeat("Full reply ", 200)) {
						t.Error("old projection was shortened", q.Content)
					}
				} else if strings.Contains(q.Content, "changed.example.test") || !strings.Contains(q.Content, "original.example.test") {
					t.Error("frozen link changed", q.Content)
				}
				if calls == 1 {
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"code":99991663}`))
				} else {
					_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"remote"}}`))
				}
			}))
			defer peer.Close()
			c := fixtureClient()
			c.Config.APIBase = peer.URL
			c.Config.WorkspaceURL = "https://original.example.test"
			projections := 0
			a := Adapter{Client: c, Store: store, ProjectCard: func(_ context.Context, f contract.CardFacts) (contract.CardProjection, error) {
				projections++
				if projections > 1 || legacy {
					return contract.CardProjection{}, errors.New("current plugin unavailable")
				}
				return feishuv1.ProjectCard(f), nil
			}}
			if observed := a.Send(context.Background(), e); observed.Outcome != delivery.Retry {
				t.Fatal(observed)
			}
			store.saveErr = errors.New("current projection writer unavailable")
			a.Client.Config.WorkspaceURL = "https://changed.example.test"
			if observed := a.Send(context.Background(), e); observed.Outcome != delivery.Delivered || calls != 2 {
				t.Fatal(observed, calls)
			}
			if observed := a.Query(context.Background(), e); observed.Outcome != delivery.Delivered || calls != 2 {
				t.Fatal("stored receipt caused a send", observed, calls)
			}
			expectedSaves := 1
			if legacy {
				expectedSaves = 0
			}
			expectedProjections := 1
			if legacy {
				expectedProjections = 0
			}
			if projections != expectedProjections {
				t.Fatal("frozen recovery depended on current plugin", projections)
			}
			if store.saves != expectedSaves {
				t.Fatal("accepted projection was re-prepared", store.saves, expectedSaves)
			}
		})
	}
}
func fmtBool(v bool) string {
	if v {
		return "legacy"
	}
	return "linked"
}

func TestUnknownDeliveryQueryDoesNotAcceptOrSendNewProjection(t *testing.T) {
	store := &projectionStore{source: "om_source"}
	request := delivery.Request{DeliveryID: "delivery:unknown", IdempotencyKey: "delivery:unknown", MessageID: "unknown", ConversationID: "owner", Channel: "feishu", DestinationRef: "chat", Intent: "completion", CreatedAt: time.Now().UnixMicro()}
	envelope := delivery.Envelope{Request: request, Message: conversation.Message{MessageID: request.MessageID, ConversationID: request.ConversationID, Role: "assistant", Sequence: 7, Content: conversation.Text("Original result")}}
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("query attempted a remote effect")
		w.WriteHeader(500)
	}))
	defer peer.Close()
	client := fixtureClient()
	client.Config.APIBase = peer.URL
	adapter := Adapter{Client: client, Store: store}
	if observed := adapter.Query(context.Background(), envelope); observed.Outcome != delivery.Unknown || store.raw != nil || store.saves != 0 || calls != 0 {
		t.Fatal("query acquired a fresh presentation or send", observed, store.saves, calls)
	}
}

// A frozen presentation belongs to the exact accepted message. Neither a retry
// nor recovery may reuse it for changed content under the same delivery key.
func TestFrozenDeliveryRejectsEnvelopeDriftBeforeTransport(t *testing.T) {
	store := &projectionStore{source: "om_source"}
	request := delivery.Request{DeliveryID: "delivery:frozen", IdempotencyKey: "delivery:frozen", MessageID: "frozen", ConversationID: "owner", Channel: "feishu", DestinationRef: "chat", Intent: "completion", CreatedAt: time.Now().UnixMicro()}
	envelope := delivery.Envelope{Request: request, Message: conversation.Message{MessageID: request.MessageID, ConversationID: request.ConversationID, Role: "assistant", Sequence: 8, Content: conversation.Text("Accepted result")}}
	store.raw = mustJSON(t, composeProjection(envelope, "https://original.example.test", store.source))
	store.hash = ui.Hash(envelope.Message)
	original := string(store.raw)
	envelope.Message.Content = conversation.Text("Different result")
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("changed envelope reached transport")
		w.WriteHeader(500)
	}))
	defer peer.Close()
	client := fixtureClient()
	client.Config.APIBase = peer.URL
	adapter := Adapter{Client: client, Store: store}
	for _, observed := range []delivery.Observation{adapter.Send(context.Background(), envelope), adapter.Query(context.Background(), envelope)} {
		if observed.Outcome != delivery.Unknown {
			t.Fatal("drift was accepted", observed)
		}
	}
	if calls != 0 || store.saves != 0 || string(store.raw) != original || store.receipt != "" {
		t.Fatal("drift changed accepted projection or produced effects", calls, store.saves)
	}
}

func TestManagedCardFailureCannotFallBackOrSend(t *testing.T) {
	store := &projectionStore{source: "om_source"}
	request := delivery.Request{DeliveryID: "delivery:stopped", IdempotencyKey: "delivery:stopped", MessageID: "stopped", ConversationID: "owner", Channel: "feishu", DestinationRef: "chat", Intent: "completion", CreatedAt: time.Now().UnixMicro()}
	envelope := delivery.Envelope{Request: request, Message: conversation.Message{MessageID: request.MessageID, ConversationID: request.ConversationID, Role: "assistant", Sequence: 9, Content: conversation.Text("New result")}}
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("stopped plugin reached transport") }))
	defer peer.Close()
	client := fixtureClient()
	client.Config.APIBase = peer.URL
	adapter := Adapter{Client: client, Store: store, ProjectCard: func(context.Context, contract.CardFacts) (contract.CardProjection, error) {
		return contract.CardProjection{}, errors.New("stopped")
	}}
	if observed := adapter.Send(context.Background(), envelope); observed.Outcome != delivery.Unknown || calls != 0 || store.saves != 0 || store.raw != nil {
		t.Fatal("managed failure fell back", observed, calls, store.saves)
	}
}
func TestCardPresentationCannotAcquireLinksOrFeedback(t *testing.T) {
	for _, mode := range []string{"linked", "feedback", "unknown"} {
		if _, err := bindCard(ui.Spec{}, contract.CardProjection{Mode: mode}, contract.CardFacts{}, "", 0, "", "", ""); err == nil {
			t.Fatal("unauthorized card mode", mode)
		}
	}
	if _, err := bindCard(ui.Spec{}, contract.CardProjection{Mode: "plain"}, contract.CardFacts{Feedback: true}, "", 0, "", "feedback:one", ""); err == nil {
		t.Fatal("feedback callback dropped")
	}
}
