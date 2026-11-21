package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/tool"
)

type correctionWebFixture struct {
	ownerHTTPCore
	runs     *Runs
	memories *memorysqlite.Store
	receipt  agent.Receipt
}

func (f *correctionWebFixture) WebApprovals(context.Context) ([]interaction.Approval, error) {
	return nil, nil
}
func (f *correctionWebFixture) Conversation(context.Context, int64) (conversation.Page, error) {
	return conversation.Page{}, nil
}
func (f *correctionWebFixture) WaitForRuntime(context.Context) error { return nil }
func (f *correctionWebFixture) AcceptInbound(ctx context.Context, e interaction.InboundEvent) (agent.Receipt, error) {
	receipt, err := f.runs.AcceptInbound(ctx, e)
	f.receipt = receipt
	return receipt, err
}
func (f *correctionWebFixture) InspectMemory(ctx context.Context, q memory.InspectionQuery) (memory.InspectionPage, error) {
	return f.runs.InspectMemory(ctx, q, f.memories)
}
func (f *correctionWebFixture) InspectMemoryPresented(ctx context.Context, q memory.InspectionQuery, rev string) (memory.InspectionPage, *memory.InspectionPresentation, error) {
	return f.runs.InspectMemoryPresented(ctx, q, f.memories, rev)
}
func TestMemoryInspectorCorrectionSubmittedInputRestrictsRealMutations(t *testing.T) {
	ctx := context.Background()
	s, r, history, a, model := loopFixture(t)
	mem, vault := evidenceMemoryVault(t, s)
	r.ConfigureMemory(mem, vault)
	registry, err := tool.NewRegistry([]tool.Definition{memory.Definition()})
	if err != nil {
		t.Fatal(err)
	}
	a.Registry = registry
	r.registry = registry
	source := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/memory-inspector").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	sealMemoryInspectorTestPackage(t, source)
	cases, err := builtin.MemoryInspectorCases("1.1.1", source)
	if err != nil {
		t.Fatal(err)
	}
	installRuntimePackage(t, s, r, "memory-inspector", rsi.Policy{ID: "correction-owner", AllowedContracts: []string{contract.Presentation}, Cases: cases[:1]}, source)
	if _, err := s.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	write := func(key, content string) string {
		t.Helper()
		v, e := r.ManageMemory(ctx, key, memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Review preference", Content: content})
		if e != nil {
			t.Fatal(e)
		}
		return v["ref"].(string)
	}
	target := write("first", "Send a review on Thursday.")
	other := write("second", "Send a review on Sunday.")
	owner := recall.Scope{UserID: s.OwnerID(), AgentID: idAgent(t, s)}
	core := &correctionWebFixture{runs: r, memories: mem}
	server := &web.Server{Core: core, Memory: core}
	request := func(method, path, contentType string, body *bytes.Buffer) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, body)
		req.RemoteAddr = "127.0.0.1:9000"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
		return w
	}
	detail := request("GET", "/developer/memory?ref="+url.QueryEscape(target), "", &bytes.Buffer{})
	link := regexp.MustCompile(`href="([^"]*correct_memory=[^"]+)"`).FindStringSubmatch(detail.Body.String())
	if detail.Code != 200 || len(link) != 2 {
		t.Fatalf("missing correction link on original inspection: %d, %s", detail.Code, detail.Body.String())
	}
	openForm := func(path string) (string, string) {
		t.Helper()
		form := request("GET", path, "", &bytes.Buffer{})
		id := regexp.MustCompile(`name="source_id" value="([^"]+)"`).FindStringSubmatch(form.Body.String())
		draft := regexp.MustCompile(`<textarea[^>]*name="text"[^>]*>([^<]*)</textarea>`).FindStringSubmatch(form.Body.String())
		if form.Code != 200 || len(draft) != 2 || len(id) != 2 {
			t.Fatalf("missing editable form: %d", form.Code)
		}
		return html.UnescapeString(id[1]), html.UnescapeString(draft[1])
	}
	id, draft := openForm(html.UnescapeString(link[1]))
	stale, _ := openForm(html.UnescapeString(link[1]))
	if !strings.Contains(draft, target) || !memory.IsCorrectionSource(id) || id == stale {
		t.Fatal("selected form lacks a fresh bound identity")
	}
	submit := func(source, text string, mutations ...string) agent.Receipt {
		t.Helper()
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		for name, value := range map[string]string{"source_id": source, "text": text} {
			if err := form.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		response := request("POST", "/message", form.FormDataContentType(), &buf)
		if response.Code != http.StatusSeeOther || core.receipt.MessageID == "" {
			t.Fatalf("submit=%d %s", response.Code, response.Body.String())
		}
		observed := false
		model.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
			if observed {
				return llm.GenerationResult{Text: "Finished checking the request."}, nil
			}
			for _, m := range req.Messages {
				if m.Role == "user" && m.Origin != "runtime" && m.Text == text {
					observed = true
				}
			}
			if !observed {
				t.Fatal("model did not receive submitted user input")
			}
			return calls(memory.ManageName, mutations...), nil
		}
		mustDrain(t, a)
		accepted, err := history.Get(ctx, owner.AgentID, core.receipt.MessageID)
		if !observed || err != nil || accepted.Role != "user" || accepted.Source != "web" || accepted.SourceEventID != source || len(accepted.Content) != 1 || accepted.Content[0].Text != text {
			t.Fatal("submission missing from original user history", accepted, err)
		}
		return core.receipt
	}
	mut := func(ref string, rev int64, content string) string {
		v, err := json.Marshal(memory.Mutation{Operation: "write", Layer: memory.Notes, Title: "Review preference", Content: content, MemoryRef: ref, Revision: rev})
		if err != nil {
			t.Fatal(err)
		}
		return string(v)
	}
	inspect := func(ref string) memory.InspectionRecord {
		t.Helper()
		page, err := r.InspectMemory(ctx, memory.InspectionQuery{Ref: ref}, mem)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("inspect %s: %+v, %v", ref, page, err)
		}
		return page.Items[0]
	}
	failures := func(code string) int {
		return scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND json_extract(result_json,'$.error.code')=?`, memory.ManageName, code)
	}
	observations := func(receipt agent.Receipt) int {
		return scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE source_type='conversation' AND source_ref=?`, receipt.MessageID)
	}
	friday := "Send a review on Friday."
	// Editing or even replacing the visible reference cannot transfer the
	// selection. The fake provider deliberately proposes the valid sibling.
	submitted := strings.Replace(draft, target, other, 1) + friday
	selected := submit(id, submitted, mut(other, 1, friday), mut(target, 2, friday), mut(target, 1, friday))
	prior, sibling := inspect(target), inspect(other)
	if prior.Status != "superseded" || prior.Revision != 1 || prior.ReplacedBy == "" || sibling.Status != "active" || sibling.Revision != 1 || sibling.Content != "Send a review on Sunday." {
		t.Fatal("wrong original or same-title sibling after correction", prior, sibling)
	}
	corrected := inspect(prior.ReplacedBy)
	doc, err := mem.ReadDocument(ctx, owner, 0, corrected.Ref)
	if err != nil || corrected.Status != "active" || corrected.Supersedes != target || corrected.Revision != 2 || doc.Content != friday {
		t.Fatal("selected original did not get the expected successor", corrected, doc, err)
	}
	hasOriginal := false
	for _, source := range doc.Sources {
		if source.Type == "conversation" && source.Ref == "conversation:"+selected.MessageID {
			hasOriginal = true
		}
	}
	if !hasOriginal || failures("PERMISSION_DENIED") != 2 || observations(selected) != 0 {
		t.Fatal("missing exact input provenance, wrong target/revision receipts, or duplicate unscoped extraction")
	}
	// The second tab still submits its old exact target. A newer revision
	// cannot be chosen by either the model or the stale text.
	old := submit(stale, draft+"Use Saturday instead.", mut(target, 1, "Saturday"), mut(corrected.Ref, 2, "Saturday"))
	if failures("CONFLICT") != 1 || failures("PERMISSION_DENIED") != 3 || observations(old) != 0 || inspect(corrected.Ref).Content != friday || inspect(other).Content != sibling.Content {
		t.Fatalf("stale form: conflicts=%d denied=%d unscoped=%d current=%+v sibling=%+v", failures("CONFLICT"), failures("PERMISSION_DENIED"), observations(old), inspect(corrected.Ref), inspect(other))
	}
	plain, _ := openForm("/")
	if memory.IsCorrectionSource(plain) {
		t.Fatal("subsequent chat retained correction selection")
	}
	regular := submit(plain, "Update the Sunday preference.", mut(other, 1, "Change Sunday to Monday."))
	ordinary := inspect(other)
	if observations(regular) != 1 || ordinary.Status != "superseded" || ordinary.ReplacedBy == "" || inspect(ordinary.ReplacedBy).Content != "Change Sunday to Monday." || inspect(corrected.Ref).Content != friday {
		t.Fatal("ordinary chat lost its previous memory behavior", ordinary)
	}
}
