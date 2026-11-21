package web

import (
	"context"
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/memory"
)

type correctionReader struct {
	ownerMemoryReader
	view      *memoryView
	reference string
}

func (rd *correctionReader) InspectMemoryPresented(_ context.Context, q memory.InspectionQuery, revision string) (memory.InspectionPage, *memory.InspectionPresentation, error) {
	rd.queries = append(rd.queries, q)
	rd.reference = revision
	return rd.view.Page, rd.view.Presentation, rd.failure
}

func TestMemoryInspectorCorrectionOpensEditableDraftForExactVisibleRecord(t *testing.T) {
	v := originalMemoryViewFixture(t, "document-detail")
	v.Selected.Revision = 7
	v.Presentation.CorrectionDraft = true
	v.Presentation.ReaderRevision = "sha256:" + strings.Repeat("a", 64)
	source, err := os.ReadFile("../../plugin/memoryinspector/view/memory.html")
	if err != nil {
		t.Fatal(err)
	}
	s := languageServer()
	rd := &correctionReader{view: v}
	s.Memory = rd
	for _, lang := range []string{"en", "zh-CN"} {
		r := languageRequest("GET", "/developer/memory", lang, "")
		tmpl, err := compileMemoryView(string(source), memoryViewBindings(v, r), requestLayout(r))
		if err != nil {
			t.Fatal(err)
		}
		out, err := renderMemoryView(tmpl, v)
		if err != nil {
			t.Fatal(err)
		}
		label, draft := "Correct in chat", "Please correct memory memory:original (revision 7). My correction: "
		if lang == "zh-CN" {
			label, draft = "在对话中纠正", "请纠正记忆 memory:original（修订 7）。我想改为："
		}
		target := "/?" + url.Values{"correct_memory": {v.Selected.Ref}, "memory_revision": {"7"}, "reader_revision": {v.Presentation.ReaderRevision}}.Encode()
		if !strings.Contains(string(out), `href="`+html.EscapeString(target)+`">`+label+`</a>`) {
			t.Fatal("missing exact correction link", lang)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", target, lang, ""))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="text"`) || !strings.Contains(w.Body.String(), draft+"</textarea>") || strings.Contains(w.Body.String(), "PRIVATE original") {
			t.Fatalf("composer draft failed for %s: %d", lang, w.Code)
		}
		if rd.reference != v.Presentation.ReaderRevision || rd.queries[len(rd.queries)-1] != (memory.InspectionQuery{Ref: v.Selected.Ref}) {
			t.Fatal("lookup did not bind the selected graph and record")
		}
	}
}

func TestMemoryInspectorCorrectionRevalidatesBeforeOpeningDraft(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*correctionReader)
		want   int
	}{
		{"newer revision", func(rd *correctionReader) { rd.view.Selected.Revision++ }, 404},
		{"other record", func(rd *correctionReader) { rd.view.Selected.Ref = "memory:other" }, 404},
		{"retired", func(rd *correctionReader) { rd.view.Selected.Status = "retired" }, 404},
		{"forgotten", func(rd *correctionReader) { rd.view.Selected.ContentState = "forgotten" }, 404},
		{"transcript", func(rd *correctionReader) { rd.view.Selected.Layer = memory.Transcripts }, 404},
		{"removed", func(rd *correctionReader) { rd.view.Page.Items = nil }, 404},
		{"old plugin", func(rd *correctionReader) { rd.view.Presentation.CorrectionDraft = false }, 404},
		{"different graph", func(rd *correctionReader) { rd.view.Presentation.ReaderRevision = "sha256:" + strings.Repeat("b", 64) }, 404},
		{"lost visibility", func(rd *correctionReader) { rd.failure = memory.ErrInspectionUnavailable }, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := originalMemoryViewFixture(t, "document-detail")
			v.Presentation.ReaderRevision = "sha256:" + strings.Repeat("a", 64)
			v.Presentation.CorrectionDraft = true
			rd := &correctionReader{view: v}
			target := memoryCorrectionURL(v)
			tc.change(rd)
			s := languageServer()
			s.Memory = rd
			w := httptest.NewRecorder()
			s.ServeHTTP(w, languageRequest("GET", target, "en", ""))
			if w.Code != tc.want || strings.Contains(w.Body.String(), "Please correct memory") || strings.Contains(w.Body.String(), "PRIVATE original") {
				t.Fatal("changed record produced draft", w.Code)
			}
			if !correctableMemory(*v.Selected) || !v.Presentation.CorrectionDraft {
				if memoryCorrectionURL(v) != "" {
					t.Fatal("offered correction for inaccessible record or frozen reader")
				}
			}
		})
	}
	v := originalMemoryViewFixture(t, "document-detail")
	v.Presentation.ReaderRevision = "sha256:" + strings.Repeat("a", 64)
	v.Presentation.CorrectionDraft = true
	rd := &correctionReader{view: v}
	s := languageServer()
	s.Memory = rd
	target := memoryCorrectionURL(v)
	for _, altered := range []string{target + "&text=foreign", target + "&extra=%zz", target + "&correct_memory=memory:other", strings.Replace(target, "memory_revision=0", "memory_revision=-1", 1)} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", altered, "en", ""))
		if w.Code != 400 || len(rd.queries) != 0 {
			t.Fatal("invalid query reached private inspection", w.Code)
		}
	}
	w := httptest.NewRecorder()
	r := languageRequest("GET", target, "en", "")
	r.Header.Del("Authorization")
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="/session"`) || strings.Contains(w.Body.String(), "Please correct memory") || len(rd.queries) != 0 {
		t.Fatal("unauthenticated query reached inspection", w.Code)
	}
}
