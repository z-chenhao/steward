package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type formViewCore struct {
	identityCore
	view ui.FormView
	err  error
}

func (c formViewCore) WebFormView(_ context.Context, ref string) (ui.FormView, error) {
	if ref != "ui:original#1" {
		return ui.FormView{}, agent.ErrNotFound
	}
	return c.view, c.err
}

type revokedFormViewCore struct{ formViewCore }

func (revokedFormViewCore) CheckChannel(context.Context, string, string, string) error {
	return errors.New("revoked")
}

func TestUIFormViewRouteRetainsAuthOriginalReferenceAndPlainData(t *testing.T) {
	view := ui.FormView{Props: ui.FormProps{Title: "Original\r\n中文\x00<script>", Fields: []ui.FormField{{Name: "notes", Label: "Notes", Type: "textarea"}}}, Defaults: map[string]string{"notes": ""}}
	core := formViewCore{view: view}
	for _, tc := range []struct {
		token, revoked bool
		status         int
	}{{false, false, 401}, {true, true, 403}, {true, false, 200}} {
		s := &Server{Core: core, Config: Config{TokenRef: "credential:test"}, Secrets: fakeSecrets{}}
		if tc.revoked {
			s.Core = revokedFormViewCore{core}
		}
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/ui/form-view?ref=ui:original%231", nil)
		if tc.token {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("form view bypassed ordinary guard", tc, w.Code)
		}
		if tc.status == 200 {
			var got ui.FormView
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Props.Title != view.Props.Title || w.Header().Get("Content-Type") != "application/json" || strings.Contains(w.Body.String(), "<script>") {
				t.Fatal("form data reinterpreted or became markup", w.Body.String())
			}
		}
	}
	for _, query := range []string{"", "ref=", "ref=ui:original%231&ref=other", "ref=ui:original%231&url=https://outside.invalid", "ref=ui:original%231&form_id=other", "ref=%xx"} {
		w := httptest.NewRecorder()
		(&Server{Core: core}).formView(w, httptest.NewRequest("GET", "/ui/form-view?"+query, nil))
		if w.Code != 400 {
			t.Fatal("ambiguous identity accepted", query, w.Code)
		}
	}
	for _, tc := range []struct {
		core formViewCore
		ref  string
	}{{formViewCore{err: errors.New("PRIVATE_STORAGE_DETAILS")}, "ui:original%231"}, {core, "ui:missing%231"}} {
		w := httptest.NewRecorder()
		(&Server{Core: tc.core}).formView(w, httptest.NewRequest("GET", "/ui/form-view?ref="+tc.ref, nil))
		if w.Code != 404 || strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatal("unavailable view leaked details", w.Code, w.Body.String())
		}
	}
}
