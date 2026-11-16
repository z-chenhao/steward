package rsi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/media/docx"
	docxreader "github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func wordInput(t *testing.T, text string, padding int) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	files := [][2]string{
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`},
	}
	for _, file := range files {
		w, err := z.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if padding > 0 {
		w, err := z.CreateHeader(&zip.FileHeader{Name: "word/unused.bin", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(make([]byte, padding)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func docxPackageSource(t *testing.T) string {
	return docxPackageVersion(t, "1.0.1")
}

func docxPackageVersion(t *testing.T, version string) string {
	t.Helper()
	source := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/docx-reader")
	if version != "1.0.1" {
		implementation, err := filepath.Abs("../plugin/docxreader/plugin.go")
		if err != nil {
			t.Fatal(err)
		}
		code, err := os.ReadFile(implementation)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.Replace(string(code), `Version: "1.0.1"`, `Version: "`+version+`"`, 1)
		if updated == string(code) {
			t.Fatal("plugin version fixture did not change")
		}
		override := filepath.Join(t.TempDir(), "plugin.go")
		if err = os.WriteFile(override, []byte(updated), 0600); err != nil {
			t.Fatal(err)
		}
		overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{implementation: override}})
		path := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(path, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("go", "build", "-overlay", path, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/docx-reader")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err := os.ReadFile("../../plugins/docx-reader/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	manifest, err = rsi.Seal(source, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func installDocx(t *testing.T, f *fixture) rsi.ResolvedGraph {
	t.Helper()
	ctx := context.Background()
	source := docxPackageSource(t)
	pkg, err := f.host.Install(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "docx", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "docx", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy := rsi.Policy{ID: "docx-gates", AllowedContracts: []string{docxreader.Contract}, AllowedRequests: []string{"input.read"}, Cases: []rsi.Case{
		{InstanceID: "docx", Contract: docxreader.Contract, Input: json.RawMessage(`{"input_id":"document"}`), Inputs: []rsi.InputData{{ID: "document", Data: wordInput(t, "合同正文🧭", 0)}}, Expected: json.RawMessage(`{"text":"合同正文🧭"}`)},
		{InstanceID: "docx", Contract: docxreader.Contract, Input: json.RawMessage(`{"input_id":"document"}`), Inputs: []rsi.InputData{{ID: "document", Data: []byte("not a Word file")}}, Expected: json.RawMessage(`{"error_code":"INVALID_ARGUMENT"}`)},
	}}
	if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	status, _ := f.host.Store.Status(ctx)
	if err = f.host.Propose(ctx, rsi.Candidate{ID: "docx-candidate", Baseline: status.Revision, Target: g.Revision, PolicyID: policy.ID, Goal: "migrate actual Word extraction"}); err != nil {
		t.Fatal(err)
	}
	decision, err := f.host.Verify(ctx, "docx-candidate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: "activate-docx", Expected: status.Revision, Target: g.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestDocxPluginReadsLargeInputAndExistingEvidenceTool(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t, evidence.Definition())
	doc := wordInput(t, strings.Repeat("合同正文🧭", 500), 2<<20)
	if len(doc) <= rsiplugin.MaxFrameBytes {
		t.Fatal("fixture did not exercise out-of-frame input")
	}
	staged, err := f.store.Artifacts().Stage(ctx, artifact.Input{Name: "contract.docx", MediaType: docx.MediaType}, bytes.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	diagnostics, _ := f.store.Diagnostics(ctx)
	var ref string
	if err = f.store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		ref, err = f.store.Artifacts().CommitTx(ctx, tx, artifact.Scope{Owner: true, UserID: f.store.OwnerID(), AgentID: diagnostics.AgentID}, staged, "docx-input")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	parts := []conversation.ContentPart{{Type: "text", Text: "读取合同"}, {Type: "file", ArtifactRef: ref}}
	legacy, err := f.runs.Submit(ctx, "legacy-docx", parts)
	if err != nil {
		t.Fatal(err)
	}
	g := installDocx(t, f)
	// The old owner-facing evidence API now executes the independently built
	// parser; it receives only this authorized object's verified bytes.
	page, err := f.runs.ReadEvidence(ctx, evidence.Request{Ref: ref, Limit: 4096})
	if err != nil || page.Representation != "docx_text" || !strings.HasPrefix(page.Text, "合同正文🧭") {
		t.Fatal(page, err)
	}
	if page.ReaderRevision != g.Revision {
		t.Fatal("owner pagination did not pin interpreter", page.ReaderRevision)
	}
	f.actor.Provider = recordingProvider{store: f.store, respond: func(req llm.GenerationRequest) llm.GenerationResult {
		for _, m := range req.Messages {
			if m.Role == "tool" {
				return llm.GenerationResult{Text: "read scoped Word evidence"}
			}
		}
		args, _ := json.Marshal(evidence.Request{Kind: "artifact", Ref: tool.ObjectID(ref), Limit: 4096})
		return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "read-docx", Name: evidence.ToolName, Arguments: string(args)}}}
	}}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var legacyCalls int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=?`, legacy.RunID).Scan(&legacyCalls); err != nil || legacyCalls != 0 {
		t.Fatal("historical Run changed interpreters", legacyCalls, err)
	}
	run, err := f.runs.Submit(ctx, "read-docx", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = f.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, run.RunID, evidence.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != nil || !strings.Contains(result.Text, "docx_text") || !strings.Contains(result.Text, "合同正文") {
		t.Fatal("native evidence path missed plugin", raw)
	}
	var calls int
	if err = f.db.QueryRow(`SELECT count(*) FROM rsi_call WHERE run_id=? AND instance_id='docx' AND result_json IS NOT NULL`, run.RunID).Scan(&calls); err != nil || calls != 1 {
		t.Fatal("extraction receipt missing", calls, err)
	}
	input := json.RawMessage(`{"input_id":"document"}`)
	if _, err = f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: run.RunID}, "docx", docxreader.Contract, "same-input", input, []rsi.InputData{{ID: "document", Data: doc}}); err != nil {
		t.Fatal(err)
	}
	changed := append(bytes.Clone(doc), 0)
	if _, err = f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: run.RunID}, "docx", docxreader.Contract, "same-input", input, []rsi.InputData{{ID: "document", Data: changed}}); !errors.Is(err, rsi.ErrConflict) {
		t.Fatal("changed source reused receipt", err)
	}
	punctuation := wordInput(t, strings.Repeat("&lt;", 200000), 0)
	value, err := f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: run.RunID}, "docx", docxreader.Contract, "large-text", input, []rsi.InputData{{ID: "document", Data: punctuation}})
	var large docxreader.Response
	if err != nil || json.Unmarshal(value, &large) != nil || large.Text != strings.Repeat("<", 200000) {
		t.Fatal("valid bounded text exceeded transport frame", len(value), err)
	}
	if _, err = f.host.Store.Deactivate(ctx, "disable-docx", g.Revision); err != nil {
		t.Fatal(err)
	}
	if page, err = f.runs.ReadEvidence(ctx, evidence.Request{Ref: ref, ReaderRevision: g.Revision, Offset: 4096, Limit: 4096}); err != nil || page.ReaderRevision != g.Revision || page.Text == "" {
		t.Fatal("archived owner cursor lost interpreter", page, err)
	}
	if _, err = f.runs.ReadEvidence(ctx, evidence.Request{Ref: ref}); !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatal("disabled feature resurrected native reader", err)
	}
	disabled, err := f.runs.Submit(ctx, "disabled-docx", parts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, disabled.RunID, evidence.ToolName).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil || result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
		t.Fatal("disabled reader did not fail explicitly", raw, err)
	}
	if err = f.host.Store.Quarantine(ctx, g.Revision); err != nil {
		t.Fatal(err)
	}
	if page, err = f.runs.ReadEvidence(ctx, evidence.Request{Ref: ref, ReaderRevision: g.Revision}); err == nil || page.Text != "" {
		t.Fatal("quarantine did not revoke archived cursor", page, err)
	}
}

func TestDocxBootstrapRespectsExistingSelectionAndDeactivation(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	source := docxPackageSource(t)
	if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: source}); err != nil {
		t.Fatal(err)
	}
	first, err := f.host.Store.Status(ctx)
	if err != nil || first.Revision == "" || first.Epoch != 1 {
		t.Fatal(first, err)
	}
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: source}); err != nil {
		t.Fatal(err)
	}
	again, _ := f.host.Store.Status(ctx)
	if again != first {
		t.Fatal("bootstrap changed selected graph", again, first)
	}
	if _, err = f.host.Store.Deactivate(ctx, "remove-default-docx", first.Revision); err != nil {
		t.Fatal(err)
	}
	disabled, _ := f.host.Store.Status(ctx)
	if err = builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: source}); err != nil {
		t.Fatal(err)
	}
	after, _ := f.host.Store.Status(ctx)
	if after != disabled {
		t.Fatal("bootstrap reactivated a disabled feature", after, disabled)
	}
}

func TestDocxIndependentUpgradeRollbackRetainsActualBinaries(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	first := installDocx(t, f)
	old, err := f.actor.Submit(ctx, "docx-before-upgrade", "old work")
	if err != nil {
		t.Fatal(err)
	}
	secondPackage, err := f.host.Install(ctx, docxPackageVersion(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "docx", PackageDigest: secondPackage.Digest, Config: json.RawMessage(`{}`), StateNamespace: "docx", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.ReadService(ctx, second.Revision, docxreader.Contract, json.RawMessage(`{"input_id":"document"}`), []rsi.InputData{{ID: "document", Data: wordInput(t, "private source", 0)}}); !errors.Is(err, rsi.ErrNotFound) {
		t.Fatal("unactivated candidate received owner input", err)
	}
	policy, err := f.host.Store.Policy(ctx, "docx-gates")
	if err != nil {
		t.Fatal(err)
	}
	activateVersion := func(id, baseline, target string) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy.ID, Goal: "compatible independently built DOCX release"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	activateVersion("docx-upgrade", first.Revision, second.Revision)
	data := wordInput(t, "Source 保真", 0)
	read := func(run, key string) {
		t.Helper()
		value, err := f.host.CallWithInput(ctx, rsiplugin.Execution{RunID: run}, "docx", docxreader.Contract, key, json.RawMessage(`{"input_id":"document"}`), []rsi.InputData{{ID: "document", Data: data}})
		if err != nil || string(value) != `{"text":"Source 保真"}` {
			t.Fatal("pinned binary changed", string(value), err)
		}
	}
	read(old.RunID, "old-version-after-upgrade")
	if err = f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	newRun, err := f.actor.Submit(ctx, "docx-after-upgrade", "new work")
	if err != nil {
		t.Fatal(err)
	}
	read(newRun.RunID, "new-version")
	var previous, current string
	if err = f.db.QueryRow(`SELECT coalesce(graph_revision,'') FROM rsi_run WHERE run_id=?`, old.RunID).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT coalesce(graph_revision,'') FROM rsi_run WHERE run_id=?`, newRun.RunID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if previous != first.Revision || current != second.Revision {
		t.Fatal("mixed parser versions", previous, current)
	}
	activateVersion("docx-rollback", second.Revision, first.Revision)
	if err = f.host.Store.Retire(ctx, secondPackage.Digest); err != nil {
		t.Fatal(err)
	}
	read(newRun.RunID, "retained-v2-after-rollback-and-retirement")
	read(old.RunID, "retained-v1")
}
