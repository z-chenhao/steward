package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

type traceReader struct{}

func TestTraceCostDisclosesEstimateSourceAndPartialCoverage(t *testing.T) {
	for _, tt := range []struct {
		metrics observability.Metrics
		want    []string
		absent  string
	}{
		{observability.Metrics{}, []string{"Not priced"}, "Estimated"},
		{observability.Metrics{Costs: []observability.Cost{{Currency: "USD", Micros: 42}}}, []string{"USD 0.000042"}, "Estimated"},
		{observability.Metrics{Costs: []observability.Cost{{Currency: "USD", Micros: 42, Estimated: true, Source: "catalog-v1 <script>"}}, Unpriced: 1}, []string{"USD 0.000042", "Partial", "catalog-v1 &lt;script&gt;"}, "<script>"},
	} {
		var html strings.Builder
		if err := localizedLayout("en").ExecuteTemplate(&html, "trace-cost", tt.metrics); err != nil {
			t.Fatal(err)
		}
		for _, want := range tt.want {
			if !strings.Contains(html.String(), want) {
				t.Fatal("missing cost disclosure", want, html.String())
			}
		}
		if strings.Contains(html.String(), tt.absent) {
			t.Fatal("misleading or unsafe cost", html.String())
		}
		if strings.Contains(html.String(), ">Estimated<") {
			t.Fatal("cost rows repeated the column-level explanation")
		}
	}
}

func TestTraceLabelUsesIncomingMessageOrWorkerTitle(t *testing.T) {
	for _, tc := range []struct{ role, title, input, want string }{
		{"main", "Main conversation", "Plan my trip", "Plan my trip"},
		{"main", "Main conversation", "", "Main conversation"},
		{"root", "Research itinerary", "Research itinerary", "Research itinerary"},
	} {
		trace := observability.Trace{Execution: observability.Execution{Role: tc.role}, Title: tc.title, Input: tc.input}
		if got := traceLabel(trace); got != tc.want {
			t.Fatalf("label=%q, want %q", got, tc.want)
		}
	}
}

func TestIdentifierKeepsCompleteEscapedValueAndIndependentNavigation(t *testing.T) {
	const value = `session:0123456789-very-long-identifier-<unsafe>&"`
	var out strings.Builder
	if err := localizedLayout("en").ExecuteTemplate(&out, "identifier", identifier("Session", value, "/developer/observability?session=full")); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`data-copy-value="session:0123456789-very-long-identifier-&lt;unsafe&gt;&amp;&#34;"`, `</a><button`, `disabled`, `href="/developer/observability?session=full"`} {
		if !strings.Contains(html, want) {
			t.Fatal("identifier lost full evidence, native route, or escaping", want, html)
		}
	}
}

type pagedWireReader struct {
	traceReader
	wire  string
	calls int
	fault string
}

func (r *pagedWireReader) ObservationBody(_ context.Context, q observability.BodyQuery) (observability.Body, error) {
	r.calls++
	end := min(q.Offset+8192, len(r.wire))
	part := observability.Body{Available: true, Length: len(r.wire), Content: r.wire[q.Offset:end]}
	if end < len(r.wire) {
		part.Next = &end
	}
	if r.calls > 1 {
		switch r.fault {
		case "length":
			part.Length++
		case "unavailable":
			part.Available = false
		case "offset":
			part.Next = &q.Offset
		case "error":
			return observability.Body{}, errors.New("capture unavailable")
		}
	}
	return part, nil
}

func TestRequestsKeepNativePagingWhenAssemblyCannotComplete(t *testing.T) {
	for _, fault := range []string{"length", "unavailable", "offset", "error", "oversize"} {
		t.Run(fault, func(t *testing.T) {
			length := 10000
			if fault == "oversize" {
				length = 2 << 20
			}
			reader := &pagedWireReader{wire: `{"instructions":"` + strings.Repeat("x", length) + `"}`, fault: fault}
			s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Inspector: &inspectionReader{}, Traces: reader}
			v := &traceView{Run: "root-public", Node: "generation:g", Tab: "wire"}
			if err := s.loadTrace(context.Background(), v); err != nil {
				t.Fatal(err)
			}
			if v.Wire.Content != reader.wire[:8192] || v.Wire.Next == nil || *v.Wire.Next != 8192 || v.Wire.Length != len(reader.wire) {
				t.Fatal("failed assembly discarded the original paging route", v.Wire)
			}
			wantCalls := 2
			if fault == "oversize" {
				wantCalls = 1
			}
			if reader.calls != wantCalls {
				t.Fatal("unbounded capture reads", reader.calls, wantCalls)
			}
		})
	}
}

func TestRequestsAssembleCompleteSavedJSONForReading(t *testing.T) {
	reader := &pagedWireReader{wire: `[{"attempt":"test","body":{"instructions":"` + strings.Repeat("instruction ", 4000) + `","messages":[{"role":"user","content":"你好 <script>safe text</script>"}]}}]`}
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Inspector: &inspectionReader{}, Traces: reader}
	v := &traceView{Run: "root-public", Node: "generation:g", Tab: "wire"}
	if err := s.loadTrace(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	if reader.calls < 2 || v.Wire.Next != nil || v.Wire.Content != reader.wire {
		t.Fatal("request was left as a partial JSON fragment", reader.calls, v.Wire.Next)
	}
	var html strings.Builder
	if err := localizedLayout("en").ExecuteTemplate(&html, "observation-detail", page{Trace: v}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), "data-wire-reader") || strings.Contains(html.String(), "<script>safe text</script>") {
		t.Fatal("request reader lost safe original fallback")
	}
}

func (traceReader) Traces(context.Context, observability.TraceQuery) (observability.Traces, error) {
	return observability.Traces{Items: []observability.Trace{{Execution: observability.Execution{ID: "root-public", Role: "root", Status: "completed", CreatedAt: "2026-10-29T01:00:00Z", EndedAt: "2026-10-29T01:00:01Z"}, Title: "Review report", Session: "conversation:owner", Model: "fixture-model"}}, Next: "next-public", Total: 1}, nil
}
func (traceReader) Observations(_ context.Context, q observability.ObservationQuery) (observability.ObservationTree, error) {
	step := int64(0)
	end := int64(1793235601000000)
	root := observability.Observation{ID: "agent:root-public", Key: "root-public", RunID: "root-public", Name: "Root Worker", Type: "agent", Status: "completed", Started: end - 1000000, Ended: &end, Timing: "lifecycle"}
	gen := observability.Observation{ID: "generation:g", Key: "g", RunID: "root-public", Name: "Generate response", Type: "generation", Parent: root.ID, Step: &step, Status: "completed", Started: end - 800000, Ended: &end, Timing: "admission to acceptance"}
	tree := observability.ObservationTree{Trace: observability.Trace{Execution: observability.Execution{ID: "root-public", Role: "root", Root: "root-public", Status: "completed", CreatedAt: "2026-10-29T01:00:00Z", EndedAt: "2026-10-29T01:00:01Z"}, Title: "Review report", Session: "conversation:owner", Model: "fixture-model"}, Items: []observability.Observation{root, gen}, Selected: root}
	if q.Selected == "generation:next" {
		gen.ID = q.Selected
		gen.Key = "next"
		step = 1
		gen.Step = &step
		tree.Items[1] = gen
	}
	if q.Selected == gen.ID {
		tree.Selected = gen
		tree.ProviderTimings = []observability.ProviderTiming{
			{Attempt: "fixture", Phase: "headers", At: end - 700000, HTTPStatus: 200},
			{Attempt: "fixture", Phase: "first_body", At: end - 600000},
			{Attempt: "fixture", Phase: "stream_idle_timeout", At: end},
		}
	}
	return tree, nil
}
func (traceReader) ObservationBody(context.Context, observability.BodyQuery) (observability.Body, error) {
	return observability.Body{Available: true, Content: `{"text":"Accepted <script>unsafe()</script>","tool_calls":[]}`, Length: 68}, nil
}
func (traceReader) ChatPreview(context.Context, observability.ObservationQuery) ([]observability.ChatPreview, error) {
	return []observability.ChatPreview{{Index: 0, Role: "system", Text: "Saved system prompt", Source: "safety"}, {Index: 1, Role: "user", Text: "Private <img src=x>", Source: "conversation"}}, nil
}

func TestTraceWorkbenchAuthenticatedCompleteEscapedAndValidated(t *testing.T) {
	s := &Server{Config: Config{TokenRef: "credential:test"}, Core: &updatesCore{allowed: true, status: "idle"}, Secrets: fakeSecrets{}, Inspector: &inspectionReader{}, Traces: traceReader{}}
	request := func(path string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		if auth {
			r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/developer/observability/root-public?obs=generation:g", "/api/developer/traces", "/api/developer/observations?run=root-public", "/api/developer/observation-body?run=root-public&obs=generation:g"} {
		if w := request(path, false); w.Code != 401 {
			t.Fatal("trace auth", path, w.Code)
		}
		if w := request(path, true); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := request("/developer/observability/root-public?obs=generation:g", true)
	for _, text := range []string{"Trace tree", "Timeline", "Observation details", "Saved system prompt", "Private &lt;img", "Accepted &lt;script&gt;", "Response headers received · HTTP 200", "First response data", "Response stream idle timeout", "</html>"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatal("missing complete rendered view", text, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "<script>unsafe()") || strings.Contains(w.Body.String(), "<img src=x") {
		t.Fatal("context executed as HTML")
	}
	for _, path := range []string{"/developer/observability?run=x&tab=bad", "/api/developer/observations", "/api/developer/observation-body?run=x&obs=x&field=checkpoint", "/developer/observability?run=x&offset=-1", "/developer/observability?role=main&role=root"} {
		if w := request(path, true); w.Code != 400 {
			t.Fatal("invalid trace query accepted", path, w.Code)
		}
	}
}

func TestTraceRowsGroupActualParentsAndBoundWaterfall(t *testing.T) {
	tree := observability.ObservationTree{Trace: observability.Trace{Execution: observability.Execution{CreatedAt: "2026-10-29T01:00:00Z", EndedAt: "2026-10-29T01:00:01Z"}}, Items: []observability.Observation{{ID: "root"}, {ID: "a", Parent: "root"}, {ID: "b", Parent: "root"}, {ID: "grandchild", Parent: "a"}}}
	rows := traceRows(tree)
	if len(rows) != 4 || rows[2].ID != "grandchild" || rows[3].ID != "b" || !rows[0].HasChildren || len(rows[2].Guides) != 2 || rows[3].HasChildren {
		t.Fatal(rows)
	}
	for _, r := range rows {
		if r.X < 0 || r.X+r.Width > 1000 {
			t.Fatal("invalid waterfall geometry", r)
		}
	}
	tree.Items = []observability.Observation{{ID: "fragment", Parent: "outside"}}
	rows = traceRows(tree)
	if len(rows) != 1 || !rows[0].ParentOutside || rows[0].Depth != 0 {
		t.Fatal(rows)
	}
	tree.Items = []observability.Observation{{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"}}
	if len(traceRows(tree)) != 2 {
		t.Fatal("cycle escaped bounded rendering")
	}
}

type detailOnlyReader struct{ traceReader }

func (detailOnlyReader) Traces(context.Context, observability.TraceQuery) (observability.Traces, error) {
	return observability.Traces{}, observability.ErrNotFound
}
func TestTraceDetailDoesNotDependOnGlobalList(t *testing.T) {
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Inspector: &inspectionReader{}, Traces: detailOnlyReader{}}
	w := httptest.NewRecorder()
	s.show(w, httptest.NewRequest("GET", "/developer/observability/root-public?obs=generation:g", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), `class="obs-trace-list"`) {
		t.Fatal("detail still depends on the list", w.Code, w.Body.String())
	}
}

func TestTraceListConditionsSurviveDetailsAndClearWithoutCursor(t *testing.T) {
	r := httptest.NewRequest("GET", "/developer/observability?search=Review+%26+report&role=root&status=completed&session=owner&since=2026-10-01T00:00&cursor=older", nil)
	v, err := traceQuery(r)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := url.Parse(traceURL(v, "run", "root-public"))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Query().Get("search") != "Review & report" || detail.Query().Get("cursor") != "older" {
		t.Fatal(detail)
	}
	v.Run = "root-public"
	cleared, _ := url.Parse(traceClearURL(v, "role"))
	q := cleared.Query()
	if cleared.Path != "/developer/observability" || q.Has("role") || q.Has("cursor") || q.Get("search") != "Review & report" || q.Get("status") != "completed" || q.Get("session") != "owner" || !q.Has("since") {
		t.Fatal(cleared)
	}
	timeCleared, _ := url.Parse(traceClearURL(v, "time"))
	if timeCleared.Query().Has("since") || timeCleared.Query().Has("until") || timeCleared.Query().Has("cursor") {
		t.Fatal(timeCleared)
	}
	preset, _ := url.Parse(tracePresetURL(v, 24))
	since, err := time.Parse(time.RFC3339Nano, preset.Query().Get("since"))
	if err != nil || time.Since(since) < 23*time.Hour || time.Since(since) > 25*time.Hour || preset.Query().Has("cursor") || preset.Query().Get("search") != "Review & report" {
		t.Fatal(preset, err)
	}
	for _, query := range []string{"search=a&search=b", "search=" + strings.Repeat("a", 201)} {
		if _, err := traceQuery(httptest.NewRequest("GET", "/developer/observability?"+query, nil)); err == nil {
			t.Fatal("unbounded/ambiguous search accepted", query)
		}
	}
}

func TestTraceJSONDisplayPreservesNumericEvidence(t *testing.T) {
	body := `{"integer":9007199254740993,"decimal":1.234567890123456789,"large":1e100,"duplicate":1,"duplicate":2,"escaped":"\u0061","text":"完整上下文🦊<img src=x>"}`
	formatted := prettyBody(body)
	for _, value := range []string{"9007199254740993", "1.234567890123456789", "1e100"} {
		if !strings.Contains(formatted, value) {
			t.Fatal("display rounded archived numeric evidence", formatted)
		}
	}
	if strings.Count(formatted, `"duplicate"`) != 2 || !strings.Contains(formatted, `\u0061`) {
		t.Fatal("formatting rewrote recorded fields or escapes", formatted)
	}
	if prettyBody(`{"partial":`) != `{"partial":` {
		t.Fatal("partial JSON was rewritten")
	}
}

func TestTraceOperationColorUsesRecordedSpanMeaning(t *testing.T) {
	if observationKind(observability.Observation{Type: "span", Name: "Provider HTTP"}) != "http" {
		t.Fatal("HTTP span lost its operation identity")
	}
	if observationKind(observability.Observation{Type: "span", Timing: "approval wait"}) != "wait" {
		t.Fatal("approval wait lost its operation identity")
	}
	if observationKind(observability.Observation{Type: "span", Timing: "observed execution segment"}) != "span" {
		t.Fatal("generic execution was guessed into a different type")
	}
}
