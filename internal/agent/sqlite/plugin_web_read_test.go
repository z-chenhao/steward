package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

// This is initial fixture authorization, before Tool execution/recovery. Agent
// visibility alone deliberately does not grant arbitrary Run artifact access.
func grantWebReadFixture(t *testing.T, s *Store, claim agent.RunClaim, ref string) {
	t.Helper()
	ctx, owner := context.Background(), artifactOwner(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error {
		return s.Artifacts().PinTx(ctx, tx, owner, ref, "run", claim.RunID())
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWebPluginReadPublishedInterpreterSurvivesUpgradeRetirementAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	r.ConfigureWebAccess(cfg, nil)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	full := strings.Repeat("原始证据🧭 ", 1000) + "UNIQUE final marker"
	r.webContentFetch = func(context.Context, webcontent.Config, string, string) (webaccess.Page, error) {
		return webaccess.Page{URL: "https://example.com/", Text: full, Untrusted: true}, nil
	}
	clip := func(limit int) int {
		for !utf8.ValidString(full[:limit]) {
			limit--
		}
		return limit
	}
	oldEnd, newEnd := clip(8000), clip(6000)
	name, args := webaccess.ContentName, `{"url":"https://example.com/"}`
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if len(req.Tools) == 0 {
			return llm.GenerationResult{Text: "The exact evidence is retained in the original artifact; continue reading the requested source."}, nil
		}
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "evidence observed"}, nil
		}
		return calls(name, args), nil
	}
	sequence := 0
	queue := func(ref string) string {
		t.Helper()
		sequence++
		parts := conversation.Text("Read this exact evidence")
		if ref != "" {
			parts = append(parts, conversation.ContentPart{Type: "file", ArtifactRef: ref})
		}
		receipt, err := r.Submit(ctx, fmt.Sprintf("web-read-%d", sequence), parts)
		if err != nil {
			t.Fatal(err)
		}
		return receipt.RunID
	}
	result := func(id string) tool.Result {
		t.Helper()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, name).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var out tool.Result
		if json.Unmarshal([]byte(raw), &out) != nil {
			t.Fatal(raw)
		}
		return out
	}
	produce := func() string {
		t.Helper()
		name, args = webaccess.ContentName, `{"url":"https://example.com/"}`
		out := result(queue(""))
		var envelope struct {
			ResponseID string `json:"responseId"`
		}
		if out.Error != nil || json.Unmarshal([]byte(out.Text), &envelope) != nil || envelope.ResponseID == "" {
			t.Fatal(out)
		}
		return envelope.ResponseID
	}
	read := func(ref string, want int) {
		t.Helper()
		name, args = webaccess.ReadName, fmt.Sprintf(`{"responseId":%q}`, ref)
		out := result(queue(ref))
		if want == 0 {
			if out.Error == nil || out.Error.Code != "PROVIDER_UNAVAILABLE" {
				t.Fatal("native fallback or hidden failure", out)
			}
			return
		}
		var value webaccess.Slice
		if out.Error != nil || json.Unmarshal([]byte(out.Text), &value) != nil || value.Offset != 0 || value.NextOffset != want || value.TotalBytes != len(full) || value.Text != full[:want] || !value.More || !utf8.ValidString(value.Text) {
			t.Fatal("interpreter/bytes changed", value.Offset, value.NextOffset, value.TotalBytes, out.Error)
		}
	}
	native := produce()
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read(native, oldEnd) // original UTF-8 clipping, not rune-count offsets
	old := produce()
	rawDocument, _ := json.Marshal(webaccess.Document{Version: 1, Pages: []webaccess.Page{{Text: full}}})
	imported := putArtifact(t, s, artifactOwner(t, s), string(rawDocument))
	name, args = webaccess.ReadName, fmt.Sprintf(`{"responseId":%q}`, imported)
	queued := queue(imported) // actual immutable Run admitted before replacement
	second, pkg := selectWebVersion(t, h, first.Revision, "1.7.0", 5, 0)
	out := result(queued)
	var queuedValue webaccess.Slice
	if out.Error != nil || json.Unmarshal([]byte(out.Text), &queuedValue) != nil || queuedValue.NextOffset != oldEnd {
		t.Fatal("queued reader rebound", out)
	}
	read(old, oldEnd)
	read(imported, newEnd)
	newRef := produce()
	read(newRef, newEnd)
	// This is a real rollback, retirement and new execution, without rewriting
	// the published reference or repairing its original binding in the fixture.
	activateDelegation(t, h, "web-read-rollback", second, first.Revision, "web-search-owner")
	if err = h.Store.Retire(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	read(newRef, newEnd)
	read(imported, oldEnd)
	if _, err = h.Store.Deactivate(ctx, "web-read-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	read(old, oldEnd)
	read(newRef, newEnd)
	read(native, oldEnd)
	read(imported, 0)
	if err = h.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	read(old, 0)
	if err = h.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err = h.Store.Quarantine(ctx, second); err != nil {
		t.Fatal(err)
	}
	read(newRef, 0)
	lateFence := 0
	s.MainBoundary = func(point string) {
		if point != "before_web_read_commit" {
			return
		}
		s.MainBoundary = nil
		lateFence++
		if err := h.Store.Quarantine(ctx, first.Revision); err != nil {
			t.Fatal(err)
		}
	}
	name, args = webaccess.ReadName, fmt.Sprintf(`{"responseId":%q}`, old)
	blocked := queue(old)
	if err := a.Drain(ctx); !errors.Is(err, rsi.ErrRevoked) || lateFence != 1 {
		t.Fatal("original interpreter revoked after method but before receipt", lateFence, err)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND tool_name=? AND status='succeeded'`, blocked, webaccess.ReadName) != 0 {
		t.Fatal("revoked original reference acquired a public receipt")
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call WHERE instance_id='web-access'`) != 0 {
		t.Fatal("read created a second opaque journal")
	}
}

func TestWebPluginReadComplete24MiBDocumentAndExactUnicodeFind(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "complete-web-read", Name: webaccess.ReadName}}
	var ref string
	marker := "中文🧭 FINAL marker"
	prefix, suffix := `{"version":0,"version":1,"unknown":"compatible","pages":[{"text":"`, marker+`"}]}`
	document := prefix + strings.Repeat("a", webaccess.MaxEvidenceBytes-len(prefix)-len(suffix)) + suffix
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		ref = putArtifact(t, s, artifactOwner(t, s), document)
		calls[0].Arguments = fmt.Sprintf(`{"responseId":%q,"findText":"中文🧭 FINAL marker","findMode":"exact","limit":600}`, ref)
	})
	defer r.Release(ctx, claim)
	grantWebReadFixture(t, s, claim, ref)
	defer keepRunClaim(r, claim)()
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		data, _ := json.Marshal(results)
		t.Fatal(string(data), err)
	}
	var got webaccess.Slice
	wantBytes := webaccess.MaxEvidenceBytes - len(prefix) - len(`"}]}`)
	if json.Unmarshal([]byte(results[0].Text), &got) != nil || got.Found == nil || !*got.Found || got.TotalBytes != wantBytes || got.NextOffset != wantBytes || got.More || !strings.HasSuffix(got.Text, marker) || !utf8.ValidString(got.Text) || got.Text != strings.Repeat("a", 400)+marker {
		t.Fatal("complete evidence was truncated/reinterpreted", got.Offset, got.NextOffset, got.TotalBytes)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("read added an opaque journal")
	}
}

func TestWebPluginReadRejectsAcceptedForgedMatchWithoutNativeFallback(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "proof-web-read", Name: webaccess.ReadName}}
	var ref string
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		initial, err := h.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selectWebVersion(t, h, initial.Revision, "1.8.0", 5, 0)
		ref = putArtifact(t, s, artifactOwner(t, s), `{"version":1,"pages":[{"text":"original UNIQUE evidence"}]}`)
		calls[0].Arguments = fmt.Sprintf(`{"responseId":%q,"findText":"unique","findMode":"case-insensitive"}`, ref)
	})
	defer r.Release(ctx, claim)
	grantWebReadFixture(t, s, claim, ref)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" {
		data, _ := json.Marshal(results)
		t.Fatal("forged position/native fallback", string(data), err)
	}
}

func TestWebPluginReadLateForgetAndGuardianCannotAcceptPrivateResult(t *testing.T) {
	for _, mode := range []string{"forget", "stop"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{}
			calls := []llm.ToolCall{{ID: "private-read", Name: webaccess.ReadName}}
			var ref string
			var h rsi.Host
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				ref = putArtifact(t, s, artifactOwner(t, s), `{"version":1,"pages":[{"text":"PRIVATE_WEB_READER_RESULT"}]}`)
				calls[0].Arguments = fmt.Sprintf(`{"responseId":%q}`, ref)
			})
			defer r.Release(ctx, claim)
			grantWebReadFixture(t, s, claim, ref)
			owner := idAgent(t, s)
			reached := 0
			s.MainBoundary = func(point string) {
				if point != "after_web_read_method" {
					return
				}
				s.MainBoundary = nil
				reached++
				if mode == "forget" {
					if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
						t.Fatal(err)
					}
				} else if err := h.Store.Stop(ctx, true); err != nil {
					t.Fatal(err)
				}
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if reached != 1 {
				t.Fatal("did not execute actual isolated reader", reached, err)
			}
			if mode == "forget" {
				if !errors.Is(err, agent.ErrClaimLost) {
					t.Fatal("Forget hidden", results, err)
				}
			} else if !errors.Is(err, rsi.ErrRevoked) {
				t.Fatal("Guardian stop hidden", results, err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded' AND instr(result_json,'PRIVATE_WEB_READER_RESULT')>0`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
				t.Fatal("late private result was accepted")
			}
			if mode == "stop" {
				if err = h.Store.Stop(ctx, false); err != nil {
					t.Fatal(err)
				}
				if err = r.Release(ctx, claim); err != nil {
					t.Fatal(err)
				}
				next, err := r.Claim(ctx, claim.RunID())
				if err != nil {
					t.Fatal(err)
				}
				defer r.Release(ctx, next)
				recovered := tool.Executor{Registry: executor.Registry, Store: r.ToolStore(next), Concurrency: 1}
				again, err := recovered.Execute(ctx, exchange, calls)
				if err != nil || len(again) != 1 || again[0].Error != nil || !strings.Contains(again[0].Text, "PRIVATE_WEB_READER_RESULT") {
					t.Fatal("safe read cannot resume", again, err)
				}
			}
		})
	}
}

func TestWebPluginReadOriginalVersionAfterRealReleaseRecoveryAndClaim(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "recover-read", Name: webaccess.ReadName}}
	var ref string
	var h rsi.Host
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
		ref = putArtifact(t, s, artifactOwner(t, s), `{"version":1,"pages":[{"text":"`+strings.Repeat("x", 10000)+`"}]}`)
		calls[0].Arguments = fmt.Sprintf(`{"responseId":%q}`, ref)
	})
	grantWebReadFixture(t, s, claim, ref)
	initial, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invocations, err := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
	if err != nil || len(invocations) != 1 {
		t.Fatal(invocations, err)
	}
	if err = executor.Store.Start(ctx, invocations[0].ID); err != nil {
		t.Fatal(err)
	}
	selectWebVersion(t, h, initial.Revision, "1.7.0", 5, 0)
	if err = r.Release(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if pending, e := r.Next(ctx); e != nil || pending != claim.RunID() {
		t.Fatal("original Run was not recovered", pending, e)
	}
	next, err := r.Claim(ctx, claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	recovered := tool.Executor{Registry: executor.Registry, Store: r.ToolStore(next), Concurrency: 1}
	results, err := recovered.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil {
		data, _ := json.Marshal(results)
		t.Fatal(string(data), err)
	}
	var value webaccess.Slice
	if json.Unmarshal([]byte(results[0].Text), &value) != nil || value.NextOffset != 8000 || value.Text != strings.Repeat("x", 8000) || value.TotalBytes != 10000 {
		t.Fatal("recovery selected active reader", value.NextOffset, value.TotalBytes)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='succeeded'`, invocations[0].ID) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("recovery lost original Tool")
	}
}

func TestWebPluginReadProofRejectsFalseAbsenceAndChangedExplicitWindow(t *testing.T) {
	text := "前言 marker 🧭 trailing"
	found, absent := true, false
	start := strings.Index(text, "marker")
	q := webaccess.ReadRequest{FindText: "MARKER", FindMode: "case-insensitive"}
	valid := webaccess.ReadPlan{NextOffset: len(text), MatchStart: start, MatchEnd: start + 6, Found: &found}
	if got, err := acceptWebReadPlan(text, q, valid); err != nil || got.Text != text || got.Found == nil || !*got.Found {
		t.Fatal(got, err)
	}
	for _, p := range []webaccess.ReadPlan{
		{Found: &absent},
		{NextOffset: len(text), MatchStart: start + 1, MatchEnd: start + 6, Found: &found},
		{Offset: 1, NextOffset: len(text), MatchStart: start, MatchEnd: start + 6, Found: &found},
		{NextOffset: start, MatchStart: start, MatchEnd: start + 6, Found: &found},
		{NextOffset: len(text) + 1, MatchStart: start, MatchEnd: start + 6, Found: &found},
	} {
		if got, err := acceptWebReadPlan(text, q, p); err == nil || got.Text != "" {
			t.Fatal("forged evidence accepted", p, got, err)
		}
	}
	q.FindText = "absent"
	if got, err := acceptWebReadPlan(text, q, webaccess.ReadPlan{Found: &absent}); err != nil || got.TotalBytes != len(text) || got.Found == nil || *got.Found || got.More || got.NextOffset != 0 {
		t.Fatal(got, err)
	}
	// Explicit one-byte requests inside a multi-byte rune retain V1's empty
	// clipped slice. Unspecified limits may evolve, but must make progress.
	if got, err := acceptWebReadPlan(text, webaccess.ReadRequest{Offset: 1, Limit: 1}, webaccess.ReadPlan{}); err != nil || got.Text != "" || got.Offset != 0 || !got.More {
		t.Fatal(got, err)
	}
	if _, err := acceptWebReadPlan("abcdef", webaccess.ReadRequest{Limit: 3}, webaccess.ReadPlan{NextOffset: 4}); err == nil {
		t.Fatal("explicit limit changed")
	}
	if _, err := acceptWebReadPlan("abcdef", webaccess.ReadRequest{}, webaccess.ReadPlan{NextOffset: 4}); err != nil {
		t.Fatal("candidate default frozen", err)
	}
}
