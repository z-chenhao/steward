package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"database/sql"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func managedFormFixture(t *testing.T, formViews ...bool) (*Store, *Runs, rsi.Host, conversation.Message, ui.Component) {
	t.Helper()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	var host rsi.Host
	if len(formViews) > 0 && formViews[0] {
		host = installUIRuntimeAtFormBoundary(t, s, r, false, true)
	} else {
		host = installUIRuntimePlugin(t, s, r)
	}
	ctx := context.Background()
	if _, err := s.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	var step atomic.Int32
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		if step.Add(1) == 1 {
			return calls(ui.ShowName, `{"title":"Answer","fallback":"Answer","components":[{"name":"steward.form","props":{"title":"Answer","fields":[{"name":"answer","label":"Answer","type":"textarea","required":true}]}}]}`), nil
		}
		return llm.GenerationResult{Text: "Please answer."}, nil
	}
	mustSubmit(t, a, "managed-form", "Ask a question")
	mustDrain(t, a)
	var raw string
	if err := s.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m conversation.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	projection, err := s.WebProjection(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(projection, &spec); err != nil {
		t.Fatal(err)
	}
	var c ui.Component
	for _, v := range spec.Components {
		if v.FormID != "" {
			c = v
		}
	}
	if c.FormID == "" {
		t.Fatal("missing form")
	}
	return s, r, host, m, c
}

func TestUIManagedFormAdmissionRollbackRetryAndOfflineRestore(t *testing.T) {
	testUIManagedFormAdmissionRollbackRetryAndOfflineRestore(t, false)
}

func TestUIFormViewAdmissionKeepsOriginalFormIDTransactionAndOfflineRestore(t *testing.T) {
	testUIManagedFormAdmissionRollbackRetryAndOfflineRestore(t, true)
}

func testUIManagedFormAdmissionRollbackRetryAndOfflineRestore(t *testing.T, formViews bool) {
	s, r, host, m, c := managedFormFixture(t, formViews)
	if formViews {
		var originalRef string
		if err := s.db.QueryRow(`SELECT ui_ref||'#0' FROM ui_widget`).Scan(&originalRef); err != nil {
			t.Fatal(err)
		}
		// WebProjection carries the frozen view reference. The ordinary WebBubbles
		// path binds WidgetRef later, as it did before this view migration.
		if c.ViewRef != originalRef {
			t.Fatal("actual form view did not bind original ordinal", c.ViewRef, originalRef)
		}
	}

	ctx := context.Background()
	var err error
	before := scalar(t, s, `SELECT count(*) FROM main_input`)
	if _, err := r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": string([]byte{0xff})}); !errors.Is(err, conversation.ErrInvalid) {
		t.Fatal("invalid UTF-8 was replaced before candidate validation", err)
	}

	if _, err = s.db.Exec(`CREATE TRIGGER fail_form_admission BEFORE INSERT ON main_input BEGIN SELECT RAISE(ABORT,'form admission fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": "line one\r\nline two"}); err == nil {
		t.Fatal("fault not exercised")
	}
	if scalar(t, s, `SELECT count(*) FROM ui_form_answer`) != 0 || scalar(t, s, `SELECT count(*) FROM main_input`) != before {
		t.Fatal("unaccepted answer was frozen")
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_form_admission`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		receipt agent.Receipt
		err     error
	}
	done := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			receipt, err := r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": "line one\r\nline two"})
			done <- outcome{receipt, err}
		}()
	}
	one, two := <-done, <-done
	if one.err != nil || two.err != nil || !reflect.DeepEqual(one.receipt, two.receipt) {
		t.Fatalf("double submission did not converge: %#v %#v errors: %v / %v", one.receipt, two.receipt, one.err, two.err)
	}
	receipt := one.receipt

	if scalar(t, s, `SELECT count(*) FROM ui_form_answer`) != 1 || scalar(t, s, `SELECT count(*) FROM main_input`) != before+1 {
		t.Fatal("answer and receipt did not commit together")
	}
	content, found, err := s.ChannelSourceContent(ctx, "web", web.Subject, web.Destination, "ui-form:"+c.FormID)
	if err != nil || !found {
		t.Fatal(content, found, err)
	}
	expected := []conversation.ContentPart{{Type: "text", Text: "Response to: Answer\n"}, {Type: "text", Text: "Answer: line one\nline two\n"}}
	if !reflect.DeepEqual(content, expected) {
		t.Fatal("native answer semantics changed", content)
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Store.Deactivate(ctx, "disable-after-form-answer", status.Revision); err != nil {
		t.Fatal(err)
	}
	// The actual owner action is followed immediately by the dependent restore.
	if err = s.RestoreWebForm(ctx, m, &c, content); err != nil || !c.Submitted || c.Response != `{"answer":"line one\nline two"}` {
		t.Fatal("restore after deactivation failed", c, err)
	}
	r.plugins = nil // Simulate unavailable execution infrastructure, not a reselect.
	retry, err := r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": "line one\nline two"})
	if err != nil || !reflect.DeepEqual(retry, receipt) {
		t.Fatal("cross-mode retry re-executed or changed receipt", retry, receipt, err)
	}
	if err = s.RestoreWebForm(ctx, m, &c, content); err != nil {
		t.Fatal("offline restore needs live candidate", err)
	}
	bad := append([]conversation.ContentPart(nil), content...)
	bad[1].Text = "Answer: forged\n"
	if err = s.RestoreWebForm(ctx, m, &c, bad); !errors.Is(err, conversation.ErrConflict) {
		t.Fatal("unaccepted content restored", err)
	}
	if _, err = r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": "changed"}); err == nil {
		t.Fatal("missing candidate silently fell back")
	}
	if scalar(t, s, `SELECT count(*) FROM ui_form_answer`) != 1 || scalar(t, s, `SELECT count(*) FROM main_input`) != before+1 {
		t.Fatal("retry or conflict created input")
	}
}

func TestUIManagedFormRevocationBeforePolicyAndAdmission(t *testing.T) {
	for _, boundary := range []string{"before_ui_form_policy", "before_ui_form_admission"} {
		for _, kind := range []string{"forget", "guardian-stop"} {
			t.Run(boundary+"/"+kind, func(t *testing.T) {
				s, r, host, m, c := managedFormFixture(t)
				ctx := context.Background()
				before := scalar(t, s, `SELECT count(*) FROM main_input`)
				fired := false
				s.MainBoundary = func(name string) {
					if name != boundary || fired {
						return
					}
					fired = true
					if kind == "forget" {
						if err := s.Transaction(ctx, func(tx *sql.Tx) error {
							var owner string
							if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
								return err
							}
							return r.revokeMemory(ctx, tx, owner)
						}); err != nil {
							t.Fatal(err)
						}
					} else if err := host.Store.Stop(ctx, true); err != nil {
						t.Fatal(err)
					}
				}
				_, err := r.AcceptWebForm(ctx, m, c.FormID, map[string]string{"answer": "do not admit revoked input"})
				want := agent.ErrClaimLost
				if kind == "guardian-stop" {
					want = rsi.ErrRevoked
				}
				if !fired || !errors.Is(err, want) {
					t.Fatal("revocation did not reject original admission", fired, err, want)
				}
				if scalar(t, s, `SELECT count(*) FROM ui_form_answer`) != 0 || scalar(t, s, `SELECT count(*) FROM main_input`) != before {
					t.Fatal("revoked input or answer was accepted")
				}
			})
		}
	}
}
