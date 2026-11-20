package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/z-chenhao/assistant/internal/memory"
)

// The link carries only the inspected identity and revisions. Opening it is a read.
func memoryCorrectionURL(v *memoryView) string {
	if v.Presentation == nil || !v.Presentation.CorrectionDraft || v.Selected == nil || !correctableMemory(*v.Selected) {
		return ""
	}
	return "/?" + url.Values{"correct_memory": {v.Selected.Ref}, "memory_revision": {strconv.FormatInt(v.Selected.Revision, 10)}, "reader_revision": {v.Presentation.ReaderRevision}}.Encode()
}

func correctableMemory(record memory.InspectionRecord) bool {
	return record.Status == "active" && record.ContentState == "available" && record.Layer != memory.Transcripts
}

// Recheck current owner visibility and the exact displayed document before
// rendering the draft. The existing composer retains its normal submit boundary.
func (s *Server) memoryCorrectionDraft(ctx context.Context, r *http.Request) (string, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", memory.ErrInspectionInvalid
	}
	q := memory.InspectionQuery{Ref: values.Get("correct_memory")}
	readerRevision := values.Get("reader_revision")
	revision, err := strconv.ParseInt(values.Get("memory_revision"), 10, 64)
	if len(values) != 3 || len(values["correct_memory"]) != 1 || len(values["memory_revision"]) != 1 || len(values["reader_revision"]) != 1 || q.Ref == "" || q.Validate() != nil || revision < 0 || err != nil || readerRevision == "" || !validModelReader(readerRevision) {
		return "", memory.ErrInspectionInvalid
	}
	reader, ok := s.Memory.(MemoryPresentedReader)
	if !ok {
		return "", memory.ErrInspectionNotFound
	}
	page, view, err := reader.InspectMemoryPresented(ctx, q, readerRevision)
	if err != nil {
		return "", err
	}
	if view == nil || !view.CorrectionDraft || view.ReaderRevision != readerRevision || len(page.Items) != 1 {
		return "", memory.ErrInspectionNotFound
	}
	record := page.Items[0]
	if record.Ref != q.Ref || record.Revision != revision || !correctableMemory(record) {
		return "", memory.ErrInspectionNotFound
	}
	prompt := "Please correct memory %s (revision %d). My correction: "
	if revision == 0 {
		prompt = "Please correct memory %s. My correction: "
		return fmt.Sprintf(translate(requestLanguage(r), prompt), record.Ref), nil
	}
	return fmt.Sprintf(translate(requestLanguage(r), prompt), record.Ref, revision), nil
}
