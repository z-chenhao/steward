package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
)

func TestModelConnectionBrowserMatches48ExecutedNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/browser-merge-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Facts    contract.BrowserFacts `json:"facts"`
		Expected []contract.Choice     `json:"expected"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 48 {
		t.Fatal("native cases missing", len(cases))
	}
	for i, c := range cases {
		for _, managed := range []bool{true, false} {
			calls := 0
			s := Server{ModelMenu: func(_ context.Context, f contract.Facts) (contract.Projection, bool, error) {
				calls++
				return owner.ProjectMenu(f), managed, nil
			}}
			body, _ := json.Marshal(c.Facts)
			r := httptest.NewRequest("POST", "/api/models/project", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			if err = s.post(w, r); err != nil {
				t.Fatal(err)
			}
			var got []contract.Choice
			if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || calls != 1 || !reflect.DeepEqual(got, c.Expected) {
				t.Fatalf("case=%d managed=%t got=%v expected=%v code=%d err=%v", i, managed, got, c.Expected, w.Code, err)
			}
		}
	}
}
func TestModelConnectionBrowserRejectsInvalidPolicyWithoutNativeFallback(t *testing.T) {
	f := contract.BrowserFacts{Reason: "ack", Previous: []contract.BrowserChoice{{Choice: contract.Choice{Key: "account:old", Label: "Original"}, SortKey: "original"}}, Received: []contract.BrowserChoice{{Choice: contract.Choice{Key: "profile:current:2", Label: "Current"}, SortKey: "current"}}}
	for _, mode := range []string{"independent-received-only", "stopped", "missing", "unknown-source", "out-of-range", "negative-index", "duplicate", "forged-current", "forged-label"} {
		t.Run(mode, func(t *testing.T) {
			s := Server{ModelMenu: func(_ context.Context, facts contract.Facts) (contract.Projection, bool, error) {
				out := owner.ProjectMenu(facts)
				switch mode {
				case "independent-received-only":
					refs := []contract.ChoiceRef{{Source: "received", Index: 0}}
					out.Order = &refs
				case "stopped":
					return out, true, llm.ErrNoBridge
				case "missing":
					out.Order = nil
				case "unknown-source":
					(*out.Order)[0].Source = "profile-store"
				case "out-of-range":
					(*out.Order)[0].Index = 2
				case "negative-index":
					(*out.Order)[0].Index = -1
				case "duplicate":
					refs := append(*out.Order, (*out.Order)[0])
					out.Order = &refs
				case "forged-current":
					out.Current = "foreign"
				case "forged-label":
					out.Choices = []contract.Choice{{Key: "foreign", Label: "Injected"}}
				}
				return out, true, nil
			}}
			body, _ := json.Marshal(f)
			r := httptest.NewRequest("POST", "/api/models/project", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			if err := s.post(w, r); err != nil {
				t.Fatal(err)
			}
			if mode == "independent-received-only" {
				var got []contract.Choice
				err := json.Unmarshal(w.Body.Bytes(), &got)
				if err != nil || w.Code != 200 || len(got) != 1 || got[0] != f.Received[0].Choice {
					t.Fatal("owner replacement ignored", w.Code, w.Body.String(), err)
				}
			} else if w.Code != 503 {
				t.Fatal("invalid/stopped policy fell back", w.Code, w.Body.String())
			}
		})
	}
	for _, body := range []string{`{"reason":"activate"}`, `{"reason":"ack","current":"foreign"}`, `{"reason":"ack"} {}`, `{"reason":"ack","previous":[{"key":"x","label":"safe","sort_key":"safe","credential_ref":"private"}]}`} {
		s := Server{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/models/project", strings.NewReader(body))
		_ = s.post(w, r)
		if w.Code != 400 {
			t.Fatal("unknown/authoritative input admitted", w.Code, body)
		}
	}
}
