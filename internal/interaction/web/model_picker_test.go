package web

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/llm"
	owner "github.com/z-chenhao/assistant/internal/plugin/modelconnections"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelPickerOwnedShapeCannotUseNativeFallback(t *testing.T) {
	for _, mode := range []string{"missing-reader", "missing-state", "bad-label", "valid"} {
		t.Run(mode, func(t *testing.T) {
			s := Server{Core: settingsCore{}, Models: pickerModels{profiles: []llm.Profile{}}}
			reference := "sha256:" + strings.Repeat("a", 64)
			s.ModelMenu = func(_ context.Context, f contract.Facts) (contract.Projection, bool, error) {
				out := owner.ProjectMenu(f)
				out.ReaderRevision = reference
				return out, true, nil
			}
			if mode != "missing-reader" {
				s.ModelPicker = func(_ context.Context, ref string) (contract.PickerPresentation, error) {
					if ref != reference {
						t.Fatal("picker did not use menu's graph")
					}
					out := owner.ProjectPicker()
					if mode == "missing-state" {
						out.English.States = out.English.States[:127]
					}
					if mode == "bad-label" {
						out.Chinese.Label = "\x1b[2J"
					}
					return out, nil
				}
			}
			out, err := s.modelChoices(context.Background(), false)
			if mode == "valid" {
				if err != nil || out.Picker == nil || out.ReaderRevision != reference {
					t.Fatal(out, err)
				}
			} else if err == nil {
				t.Fatal("invalid owned UI silently restored native")
			}
		})
	}
}
func TestModelPickerBrowserMergeRetainsOriginalReaderWithoutSendingItToProgram(t *testing.T) {
	reference := "sha256:" + strings.Repeat("b", 64)
	calls := 0
	s := Server{ModelMenu: func(_ context.Context, f contract.Facts) (contract.Projection, bool, error) {
		calls++
		if f.ReaderRevision != reference {
			t.Fatal("frozen merge reader lost")
		}
		raw, _ := json.Marshal(f)
		if strings.Contains(string(raw), reference) {
			t.Fatal("Host reader annotation leaked into plugin input")
		}
		return owner.ProjectMenu(f), true, nil
	}}
	body := `{"previous":[],"received":[],"reason":"ack","unavailable":false}`
	r := httptest.NewRequest("POST", "http://localhost/api/models/project", strings.NewReader(body))
	r.Header.Set("X-Steward-Model-Reader", reference)
	w := httptest.NewRecorder()
	s.composerModelProjection(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
	r = httptest.NewRequest("POST", "http://localhost/api/models/project", strings.NewReader(body))
	r.Header.Set("X-Steward-Model-Reader", "invalid")
	w = httptest.NewRecorder()
	s.composerModelProjection(w, r)
	if w.Code != 400 || calls != 1 {
		t.Fatal("invalid reader reached program", w.Code, calls)
	}
}
