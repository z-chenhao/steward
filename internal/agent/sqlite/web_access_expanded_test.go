package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func TestWebAccessAuxiliaryLedgerBudgetReplayAndFences(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			ctx := context.Background()
			calls := []llm.ToolCall{{ID: "search", Name: webaccess.SearchName, Arguments: `{"query":"public query","workflow":"auto-summary"}`}}
			s, r, c, executor, exchange := admitWebSearch(t, calls, blocked)
			defer r.Release(ctx, c)
			invs, e := executor.Store.Accept(ctx, exchange, calls, executor.Registry.Describe())
			if e != nil {
				t.Fatal(e)
			}
			if e = executor.Store.Start(ctx, invs[0].ID); e != nil {
				t.Fatal(e)
			}
			run, e := r.Load(ctx, c)
			if e != nil {
				t.Fatal(e)
			}
			dispatched := 0
			r.ConfigureWebAccess(webaccess.Config{}, &loopProvider{store: s, respond: func(req llm.GenerationRequest) (llm.GenerationResult, error) {
				dispatched++
				before, _ := encoded(run.Snapshot)
				after, _ := encoded(req.Snapshot)
				if req.ToolGeneration == nil || before != after || len(req.Tools) > 0 || len(req.Messages) != 2 {
					t.Error(req)
				}
				return llm.GenerationResult{Text: "summary evidence", Usage: &llm.TokenUsage{InputTokens: 7, OutputTokens: 2}}, nil
			}})
			ts := executor.Store.(*tools)
			request := webaccess.ModelCall{Kind: "summary", Messages: []llm.Message{{Role: "system", Text: "Use evidence only"}, {Role: "user", Text: "public evidence"}}}
			first, e := ts.webModelGeneration(ctx, invs[0], run, "summary", request)
			if blocked {
				if e == nil || e.Error() != "BUDGET_APPROVAL_REQUIRED" || dispatched != 0 {
					t.Fatal(first, e, dispatched)
				}
				return
			}
			if e != nil || dispatched != 1 {
				t.Fatal(first, e, dispatched)
			}
			again, e := ts.webModelGeneration(ctx, invs[0], run, "summary", request)
			if e != nil || again.Text != first.Text || dispatched != 1 {
				t.Fatal(again, e, dispatched)
			}
			request.Messages[1].Text = "altered evidence"
			if _, e = ts.webModelGeneration(ctx, invs[0], run, "summary", request); e == nil {
				t.Fatal("changed accepted request was reused")
			}
			if e = r.write(ctx, func(tx *sql.Tx) error {
				var taskID string
				if e := tx.QueryRowContext(ctx, `SELECT task_id FROM run WHERE run_id=?`, run.ID).Scan(&taskID); e != nil {
					return e
				}
				messages, e := r.taskHandoff(ctx, tx, taskID, 2)
				if e == nil && len(messages) != 0 {
					t.Fatal("auxiliary evidence became final task handoff", messages)
				}
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if scalar(t, s, `SELECT count(*) FROM provider_usage WHERE generation_id IN (SELECT generation_id FROM model_generation WHERE run_id=? AND step_index<0)`, run.ID) != 1 {
				t.Fatal("usage missing")
			}
			if e = r.Release(ctx, c); e != nil {
				t.Fatal(e)
			}
			if _, e = ts.webModelGeneration(ctx, invs[0], run, "summary", request); e == nil {
				t.Fatal("stale claim accepted")
			}
		})
	}
	if acceptedWebQuery(webaccess.SearchName, `{"queries":["first","second"]}`, "third") || !acceptedWebQuery(webaccess.SearchName, `{"queries":["first","second"]}`, "second") {
		t.Fatal("query scope validation")
	}
}
func TestWebAccessMainCodeModeReadUsesScopedPersistedDocument(t *testing.T) {
	testWebAccessMainCodeModeRead(t, false)
}
func TestWebPluginReadMainCodeModeUsesScopedPersistedDocument(t *testing.T) {
	testWebAccessMainCodeModeRead(t, true)
}
func testWebAccessMainCodeModeRead(t *testing.T, managed bool) {
	t.Helper()
	cfg := webaccess.Config{}
	defs := append(webaccess.Definitions(cfg), codemode.Definition())
	s, r, a, p := childFixture(t, defs...)
	var production []tool.Definition
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		d.Spec.CodeModeOnly = spec.Name != codemode.ToolName
		production = append(production, d)
	}
	registry, e := tool.NewRegistry(production)
	if e != nil {
		t.Fatal(e)
	}
	r.registry, a.Registry = registry, registry
	r.ConfigureJavaScript(realCodeRuntime(t))
	r.ConfigureWebAccess(cfg, p)
	if managed {
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	}
	var imageBytes bytes.Buffer
	if e = png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	normalized, e := webcontent.NormalizeImage(base64.StdEncoding.EncodeToString(imageBytes.Bytes()), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	fetched := 0
	full := strings.Repeat("中文 facts ", 2000) + "Unique final marker"
	r.webContentFetch = func(context.Context, webcontent.Config, string, string) (webaccess.Page, error) {
		fetched++
		return webaccess.Page{URL: "https://example.com/", Text: full, Untrusted: true, Image: normalized}, nil
	}
	phase := 0
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		source := `return await tools.builtin__fetch_content({url:"https://example.com/"});`
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			if phase > 0 {
				return llm.GenerationResult{Text: "Evidence read completed"}, nil
			}
			phase++
			match := regexp.MustCompile(`responseId[^a-zA-Z0-9]+(artifact:[a-zA-Z0-9_-]+)`).FindStringSubmatch(req.Messages[len(req.Messages)-1].Text)
			if len(match) != 2 {
				t.Fatal("missing responseId")
			}
			ref := match[1]
			source = fmt.Sprintf(`return await tools.builtin__get_search_content({responseId:%q,urlIndex:0,findText:"unique FINAL marker",findMode:"case-insensitive"});`, ref)
		}
		args, _ := json.Marshal(map[string]string{"source": source})
		return calls(codemode.ToolName, string(args)), nil
	}
	mustSubmit(t, a, "web-access-own-main", "Read source evidence")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = a.Drain(ctx); e != nil {
		t.Fatal(e)
	}
	if fetched != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) < 2 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded'`, webaccess.ReadName) != 1 {
		t.Fatal("content was not restored from the saved artifact", fetched)
	}
	var mediaResult string
	if e = s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, webaccess.ContentName).Scan(&mediaResult); e != nil {
		t.Fatal(e)
	}
	var result tool.Result
	if json.Unmarshal([]byte(mediaResult), &result) != nil || len(result.ImageRefs) != 1 || len(result.ArtifactRefs) != 2 {
		t.Fatal("image and evidence were not committed together", mediaResult)
	}
	var raw string
	if e = s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE tool_name=?`, webaccess.ReadName).Scan(&raw); e != nil || !strings.Contains(raw, "Unique final marker") {
		t.Fatal(raw, e)
	}
}
