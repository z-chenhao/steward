package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

type traceReader struct{}

func TestTraceCostRoundsDisplayAndKeepsExactSourceInTooltip(t *testing.T) {
	for _, tt := range []struct {
		metrics observability.Metrics
		want    []string
		absent  string
	}{
		{observability.Metrics{}, []string{"Not priced"}, "Estimated"},
		{observability.Metrics{Costs: []observability.Cost{{Currency: "USD", Micros: 42}}}, []string{">USD 0.00</span>", "USD 0.000042"}, "Estimated"},
		{observability.Metrics{Costs: []observability.Cost{{Currency: "USD", Micros: 736270, Estimated: true, Source: "catalog-v1 <script>"}}, Unpriced: 1}, []string{">USD 0.74</span>", "USD 0.736270", "catalog-v1 &lt;script&gt;"}, "<script>"},
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
		if strings.Contains(html.String(), "Partial") {
			t.Fatal("cost display added a partial badge")
		}
	}
}

func TestTraceRunningListShowsLatestMetricsWithoutPartialBadges(t *testing.T) {
	trace := observability.Trace{Execution: observability.Execution{ID: "running-trace", Role: "root", Status: "running", CreatedAt: "2026-11-08T03:17:02Z"}, Metrics: observability.Metrics{Generations: 3, KnownUsage: 2, Input: 295538, Output: 12581, Costs: []observability.Cost{{Currency: "USD", Micros: 736270}}, Unpriced: 1}}
	var out strings.Builder
	v := &traceView{List: observability.Traces{Items: []observability.Trace{trace}}}
	if err := localizedLayout("en").ExecuteTemplate(&out, "observability-page", page{Trace: v}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, want := range []string{`<thead><tr><th scope="col">Trace ID</th>`, `295538 / 12581`, `>USD 0.74</span>`, `data-copy-value="running-trace"`} {
		if !strings.Contains(body, want) {
			t.Fatal("missing latest metrics or first ID column", want, body)
		}
	}
	for _, absent := range []string{"No accepted output", "in progress", "Partial"} {
		if strings.Contains(body, absent) {
			t.Fatal("list retained redundant status text", absent)
		}
	}
	if elapsed(trace.CreatedAt, "") != "—" {
		t.Fatal("unfinished wall time was not unknown")
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
		case "skip":
			next := end + 1
			part.Next = &next
		case "overflow":
			part.Content += strings.Repeat("x", len(r.wire))
		case "error":
			return observability.Body{}, errors.New("capture unavailable")
		}
	}
	return part, nil
}

func TestRequestsKeepNativePagingWhenAssemblyCannotComplete(t *testing.T) {
	for _, fault := range []string{"length", "unavailable", "offset", "skip", "overflow", "error", "oversize"} {
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
	gen := observability.Observation{ID: "generation:g", Key: "g", RunID: "root-public", Name: "Model call", Type: "generation", Parent: root.ID, Step: &step, Status: "completed", Started: end - 800000, Ended: &end, Timing: "admission to acceptance"}
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
	for _, text := range []string{"Trace tree", "Time since trace start", "Trace overview", "Observation details", "Saved system prompt", "Private &lt;img", "Accepted &lt;script&gt;", "</html>"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatal("missing complete rendered view", text, w.Body.String())
		}
	}
	detail := request("/developer/observability/root-public?obs=generation:g&tab=metadata", true)
	for _, text := range []string{"Response headers received", "HTTP 200", "First response data", "Response stream idle timeout", "Raw observation"} {
		if !strings.Contains(detail.Body.String(), text) {
			t.Fatal("missing diagnostic view", text)
		}
	}
	for _, duplicate := range []string{"Saved system prompt", "Accepted &lt;script&gt;"} {
		if strings.Contains(detail.Body.String(), duplicate) {
			t.Fatal("details repeated input/output", duplicate)
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

func TestCompleteMessagesKeepLongLateToolAndEncryptedEvidence(t *testing.T) {
	saved := make([]map[string]any, 25)
	for i := range saved {
		saved[i] = map[string]any{"role": "user", "text": strings.Repeat("完整🦊", 1600)}
	}
	saved[24] = map[string]any{"role": "tool", "text": "last-message", "tool_call_id": "call-24", "image_refs": []string{"image:one"}, "memory_text": map[string]any{"sealed": "ciphertext", "encoded_bytes": 100}, "tool_calls": []any{map[string]any{"id": "nested", "name": "tool", "arguments": strings.Repeat("a", 1500)}}}
	raw, _ := json.Marshal(saved)
	messages, ok := completeMessages(string(raw))
	if !ok || len(messages) != 25 || messages[0].Text != saved[0]["text"] || messages[24].CallID != "call-24" || len(messages[24].Images) != 1 || !strings.HasPrefix(messages[24].Text, "last-message\n[Encrypted memory content") || !strings.Contains(messages[24].Calls, strings.Repeat("a", 1500)) {
		t.Fatal("saved message evidence truncated", messages)
	}
}

func TestPrimaryObservationsHideOnlyRoutineInternalsAndKeepDeepSelections(t *testing.T) {
	tree := observability.ObservationTree{Items: []observability.Observation{
		{ID: "root", Type: "agent"}, {ID: "prepare", Name: "Prepare context", Parent: "root"},
		{ID: "compact", Name: "Compact context", Parent: "root"}, {ID: "gen", Name: "Model call", Parent: "root"},
		{ID: "http", Name: "Provider HTTP", Parent: "gen"}, {ID: "error", Name: "Provider HTTP", Parent: "gen", Status: "failed"},
	}}
	rows, hidden := primaryObservations(tree)
	if len(rows) != 4 || hidden != 2 {
		t.Fatal(rows, hidden)
	}
	tree.Selected = tree.Items[4]
	rows, hidden = primaryObservations(tree)
	if len(rows) != 5 || hidden != 1 {
		t.Fatal("deep selected HTTP disappeared", rows, hidden)
	}
}

func TestGenerationOutputRateRequiresSameAttemptTerminalEvidence(t *testing.T) {
	tree := observability.ObservationTree{Selected: observability.Observation{Status: "completed"}, Generation: &observability.GenerationDetails{Metrics: observability.Metrics{KnownUsage: 1, Output: 100}}, ProviderTimings: []observability.ProviderTiming{
		{Attempt: "failed", Phase: "started", At: 1000000}, {Attempt: "failed", Phase: "first_text", At: 1100000},
		{Attempt: "accepted", Phase: "started", At: 2000000}, {Attempt: "accepted", Phase: "first_tool_call", At: 2500000}, {Attempt: "accepted", Phase: "stream_terminal", At: 4500000},
	}}
	if got := generationOutputRate(tree); got == nil || *got != 50 {
		t.Fatal(got)
	}
	tree.ProviderTimings = tree.ProviderTimings[:4]
	if generationOutputRate(tree) != nil {
		t.Fatal("missing terminal fabricated throughput")
	}
	tree.ProviderTimings = append(tree.ProviderTimings, observability.ProviderTiming{Attempt: "other", Phase: "stream_terminal", At: 4500000})
	if generationOutputRate(tree) != nil {
		t.Fatal("different attempts mixed")
	}
	tree.Generation.Metrics.KnownUsage = 0
	if generationOutputRate(tree) != nil {
		t.Fatal("missing usage became zero")
	}
}

type completeIOReader struct {
	traceReader
	input, output string
}

func (r completeIOReader) ObservationBody(_ context.Context, q observability.BodyQuery) (observability.Body, error) {
	raw := r.input
	if q.Field == "output" {
		raw = r.output
	}
	end := min(len(raw), q.Offset+8192)
	b := observability.Body{Available: true, Length: len(raw), Content: raw[q.Offset:end]}
	if end < len(raw) {
		b.Next = &end
	}
	return b, nil
}
func (r completeIOReader) ChatPreview(context.Context, observability.ObservationQuery) ([]observability.ChatPreview, error) {
	return nil, errors.New("complete input must not fall back to truncated preview")
}
func TestGenerationReadsCompleteInputAndOutputAcrossSavedBodyPages(t *testing.T) {
	messages := make([]map[string]string, 25)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "text": strings.Repeat("long saved message ", 400)}
	}
	input, _ := json.Marshal(messages)
	output, _ := json.Marshal(map[string]any{"text": strings.Repeat("accepted result ", 1000), "tool_calls": []any{}})
	reader := completeIOReader{input: string(input), output: string(output)}
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Inspector: &inspectionReader{}, Traces: reader}
	v := &traceView{Run: "root-public", Node: "generation:g", Tab: "overview"}
	if err := s.loadTrace(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	if !v.InputComplete || len(v.Messages) != 25 || v.Input.Content != string(input) || v.Input.Next != nil || v.Output.Content != string(output) || v.Output.Next != nil || v.ModelOutput == nil || v.ModelOutput.Text != strings.Repeat("accepted result ", 1000) {
		t.Fatal("body paging silently truncated the model exchange")
	}
}

func TestActivityRangeLinkKeepsFiltersAndClearsPagination(t *testing.T) {
	v := &traceView{Query: observability.TraceQuery{Search: "report", Session: "private-session", ExecutionQuery: observability.ExecutionQuery{Role: "root", Status: "completed", Cursor: "older"}}}
	link, err := url.Parse(traceRangeURL(v, "2026-11-08T00:00:00.123456Z", "2026-11-08T00:00:01Z"))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	if q.Has("cursor") || q.Get("search") != "report" || q.Get("role") != "root" || q.Get("status") != "completed" || q.Get("session") != "private-session" || q.Get("since") != "2026-11-08T00:00:00.123456Z" {
		t.Fatal(link)
	}
}

func TestTraceActivityNativeBarsHaveValidHeightAndExactNavigation(t *testing.T) {
	v := &traceView{List: observability.Traces{Total: 3, Activity: []observability.TraceBucket{{At: "2026-11-08T00:00:00Z", Until: "2026-11-08T00:00:05Z", Count: 3}}}}
	var out strings.Builder
	if err := localizedLayout("en").ExecuteTemplate(&out, "observability-page", page{Trace: v}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "ZgotmplZ") || !strings.Contains(body, `height="100"`) || !strings.Contains(body, "until=2026-11-08T00%3A00%3A05Z") {
		t.Fatal(body)
	}
}

func TestTimelinePreservesParallelIntervalsAndDoesNotInventUnknownEnds(t *testing.T) {
	base := int64(1793235600000000)
	end := base + 1000000
	aEnd, bEnd := base+600000, base+800000
	tree := observability.ObservationTree{Trace: observability.Trace{Execution: observability.Execution{CreatedAt: "2026-10-29T01:00:00Z", EndedAt: "2026-10-29T01:00:01Z"}}, Items: []observability.Observation{{ID: "root", Started: base, Ended: &end}, {ID: "a", Parent: "root", Started: base + 100000, Ended: &aEnd}, {ID: "b", Parent: "root", Started: base + 200000, Ended: &bEnd}, {ID: "point", Parent: "root", Started: base + 300000}}}
	rows := traceRows(tree)
	if len(rows) != 4 || rows[1].X >= rows[2].X || rows[1].X+rows[1].Width <= rows[2].X || rows[3].Ended != nil || rows[3].Running {
		t.Fatal(rows)
	}
	tree.Trace.EndedAt = ""
	tree.Items[0].Ended = nil
	tree.Items[0].Type = "agent"
	tree.Items[1].Ended = nil
	tree.Items[1].Type, tree.Items[1].Status = "generation", "pending"
	rows = traceRows(tree)
	if !rows[0].Running || !rows[1].Running || rows[0].Ended != nil || rows[1].Ended != nil || rows[3].Running {
		t.Fatal("ongoing execution and unknown event ends were conflated", rows)
	}
}

func TestActivityGeometryKeepsClippedBucketWidths(t *testing.T) {
	buckets := []observability.TraceBucket{{At: "2026-11-08T00:00:00.000500Z", Until: "2026-11-08T00:00:00.001000Z"}, {At: "2026-11-08T00:00:00.001000Z", Until: "2026-11-08T00:00:00.002000Z"}, {At: "2026-11-08T00:00:00.002000Z", Until: "2026-11-08T00:00:00.002500Z"}}
	for i, want := range []float64{250, 500, 250} {
		if got := activityWidth(buckets[i], buckets); got != want {
			t.Fatal(i, got, want)
		}
	}
	if activityX(buckets[1], buckets) != 250 || activityX(buckets[2], buckets) != 750 {
		t.Fatal("time axis positions differ from clipped intervals")
	}
}
