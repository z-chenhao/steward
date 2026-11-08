package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/memory"
)

type ownerMemoryReader struct {
	queries []memory.InspectionQuery
	failure error
}

func (m *ownerMemoryReader) InspectMemory(_ context.Context, q memory.InspectionQuery) (memory.InspectionPage, error) {
	m.queries = append(m.queries, q)
	return memory.InspectionPage{Total: 1, Items: []memory.InspectionRecord{{Ref: "memory:real", Content: "I prefer 茉莉茶 <img src=x onerror=unsafe()>", ContentState: "available", SourceCount: 1, Tier: "core", Origin: "automatic", Status: "active", CreatedAt: time.Now(), Sources: []memory.InspectionSource{{Kind: "conversation", Ref: "conversation:source", Text: "Original user evidence"}}}}}, m.failure
}
func TestMemoryRoutesRequireOwnerAndRejectInvalidQueriesAndWrites(t *testing.T) {
	reader := &ownerMemoryReader{}
	s := languageServer()
	s.Memory = reader
	request := func(method, path string, auth bool) *httptest.ResponseRecorder {
		r := languageRequest(method, path, "en", "")
		if !auth {
			r.Header.Del("Authorization")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/developer/memory", "/api/developer/memory"} {
		if w := request("GET", path, false); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := request("POST", path, true); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	for _, query := range []string{"layer=database", "sql=SELECT+1", "ref%7Csearch=ignored", "agent=other", "tier=invalid", "status=deleted", "limit=0", "limit=51", "limit=bad", "search=a&search=b", "ref=run:foreign", "source=conversation:other", "offset=-1", "view=maintenance&search=x"} {
		if w := request("GET", "/api/developer/memory?"+query, true); w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	if len(reader.queries) != 0 {
		t.Fatal("invalid request reached reader")
	}
	for _, layer := range []string{memory.Transcripts, memory.Notes, memory.Representations, memory.Injected} {
		response := request("GET", "/developer/memory?layer="+layer, true)
		if response.Code != 200 || reader.queries[len(reader.queries)-1].Layer != layer {
			t.Fatal("layer query not preserved", layer, response.Code)
		}
	}
	list := request("GET", "/developer/memory", true)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "茉莉茶 &lt;img") || strings.Contains(list.Body.String(), "<img src=x") || strings.Contains(list.Body.String(), "/developer/database") {
		t.Fatal(list.Code, list.Body.String())
	}
	detail := request("GET", "/developer/memory?ref=memory:real&source=conversation:source", true)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "Original user evidence") || !strings.Contains(detail.Body.String(), "data-split-view") {
		t.Fatal(detail.Code, detail.Body.String())
	}
	api := request("GET", "/api/developer/memory?search=茉莉茶", true)
	if api.Code != 200 || !strings.Contains(api.Body.String(), `"content":"I prefer 茉莉茶`) || api.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(api.Code, api.Header(), api.Body.String())
	}
	for _, path := range []string{"/developer/database", "/api/developer/database"} {
		if w := request("GET", path, true); w.Code != 404 {
			t.Fatal("removed Database route remains", path, w.Code)
		}
	}
}
func TestMemoryFailuresAreSafeAndLocalized(t *testing.T) {
	s := languageServer()
	reader := &ownerMemoryReader{}
	s.Memory = reader
	for _, entry := range []struct {
		err  error
		code int
	}{{memory.ErrInspectionInvalid, 400}, {memory.ErrInspectionNotFound, 404}, {errors.New("private key and host path"), 503}} {
		reader.failure = entry.err
		for _, path := range []string{"/developer/memory", "/api/developer/memory"} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, languageRequest("GET", path, "zh-CN", ""))
			if w.Code != entry.code || strings.Contains(w.Body.String(), "private key") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	reader.failure = nil
	w := httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", "/developer/memory", "zh-CN", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "记忆内容") {
		t.Fatal(w.Code, w.Body.String())
	}
}

type maintenanceMemoryReader struct{}

func (maintenanceMemoryReader) InspectMemory(_ context.Context, q memory.InspectionQuery) (memory.InspectionPage, error) {
	records := []memory.MaintenanceRecord{
		{Ref: "memory-event:priced", Operation: "dream", Status: "applied", Attempts: 1, CreatedAt: time.Now(), Metrics: &memory.MaintenanceMetrics{Generations: 2, KnownUsageGenerations: 2, InputTokens: 340, OutputTokens: 50, CacheReadTokens: 128, DocumentsWritten: 2, LaterToolReads: 1, Costs: []memory.MaintenanceCost{{Currency: "USD", ReferenceMicros: 125000, PricedGenerations: 2}}}},
		{Ref: "memory-event:unknown", Operation: "dream", Status: "rejected", Attempts: 1, Error: "MEMORY_INVALID_PROPOSAL", CreatedAt: time.Now(), Metrics: &memory.MaintenanceMetrics{Generations: 1, UnpricedGenerations: 1}},
	}
	if q.Ref != "" {
		for _, record := range records {
			if strings.Replace(record.Ref, "memory-event:", "memory:maintenance-", 1) == q.Ref {
				return memory.InspectionPage{Total: 2, Maintenance: []memory.MaintenanceRecord{record}}, nil
			}
		}
		return memory.InspectionPage{}, memory.ErrInspectionNotFound
	}
	return memory.InspectionPage{Total: 2, Maintenance: records}, nil
}
func TestMemoryMaintenanceRoutesShowPricedUnknownAndNativeDetail(t *testing.T) {
	s := languageServer()
	s.Memory = maintenanceMemoryReader{}
	for _, path := range []string{"/developer/memory?view=maintenance", "/developer/memory?view=maintenance&ref=memory:maintenance-priced", "/api/developer/memory?view=maintenance&ref=memory:maintenance-priced"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", path, "en", ""))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.HasPrefix(path, "/developer/") && !strings.Contains(w.Body.String(), "0.125000") {
			t.Fatal("priced maintenance missing", w.Body.String())
		}
	}
	if link := memoryURL(memory.InspectionQuery{View: "maintenance"}, "ref", "memory-event:priced"); !strings.Contains(link, "view=maintenance") || !strings.Contains(link, "ref=memory%3Amaintenance-priced") {
		t.Fatal(link)
	}
	if e := (memory.InspectionQuery{View: "maintenance", Ref: "memory:some-document"}).Validate(); !errors.Is(e, memory.ErrInspectionInvalid) {
		t.Fatal(e)
	}
}
