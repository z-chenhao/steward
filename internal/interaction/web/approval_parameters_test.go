package web

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/interaction"
)

func TestApprovalParametersKeepOriginalHashAndResolvedOptions(t *testing.T) {
	c := &updatesCore{allowed: true, status: "idle", approvals: []interaction.Approval{{ID: "frozen", Tool: "builtin__computer_invoke", Arguments: `{"method":"scroll","args":[{"observationRevision":"original","index":0},"down"]}`, Hash: strings.Repeat("a", 64), Status: "pending", Parameters: `{"direction":"down","amount":50}`}}}
	c.approvals = append(c.approvals, playwrightApprovalSample(), transferApprovalSample(), uploadApprovalSample())
	s := &Server{Config: Config{TokenRef: "credential:test"}, Core: c, Secrets: fakeSecrets{}}
	for _, language := range []string{"en", "zh-CN"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", "/", language, ""))
		want := "Scroll amount"
		if language == "zh-CN" {
			want = "滚动距离"
		}
		budgetLabel := "Timeout (ms)"
		if language == "zh-CN" {
			budgetLabel = "超时时间（毫秒）"
		}
		if !strings.Contains(w.Body.String(), "<dd>approved-upload.txt</dd>") || !strings.Contains(w.Body.String(), "<dd>text/plain</dd>") || !strings.Contains(w.Body.String(), `value="`+strings.Repeat("b", 64)+`"`) || !strings.Contains(w.Body.String(), "<dd>export.bin</dd>") || !strings.Contains(w.Body.String(), "<dd>application/octet-stream</dd>") || !strings.Contains(w.Body.String(), budgetLabel) || !strings.Contains(w.Body.String(), "<dd>4000</dd>") || w.Code != 200 || !strings.Contains(w.Body.String(), want) || !strings.Contains(w.Body.String(), "<dd>50</dd>") || !strings.Contains(w.Body.String(), `value="`+strings.Repeat("a", 64)+`"`) {
			t.Fatal("approval lost immutable options, language or original hash", w.Code, w.Body.String())
		}
	}
}

// A read-only, representative control-plane fixture. Business dispatch and
// the immutable SQL plan are proved by the owning sqlite/Computer regressions.
func TestApprovalParametersBrowser(t *testing.T) {
	if os.Getenv("STEWARD_TEST_APP_BROWSER") != "1" {
		t.Skip("run by make test-browser")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s := languageServer()
	s.Config.TokenRef = ""
	s.Core.(*updatesCore).approvals = []interaction.Approval{{ID: "frozen-options", Tool: "builtin__computer_invoke", Arguments: `{"method":"scroll","handle_token":"opaque-surface","args":[{"observationRevision":"original-revision","index":0},"down"]}`, Hash: strings.Repeat("a", 64), Status: "pending", Parameters: `{"direction":"down","amount":50.5}`}}
	s.Core.(*updatesCore).approvals = append(s.Core.(*updatesCore).approvals, playwrightApprovalSample(), transferApprovalSample(), uploadApprovalSample())
	server := httptest.NewServer(s)
	defer server.Close()
	script, err := filepath.Abs("../../../tests/system/approval_parameters_browser.mjs")
	if err != nil {
		t.Fatal(err)
	}
	evidence := os.Getenv("STEWARD_APP_BROWSER_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	evidence = filepath.Join(evidence, "approval-parameters")
	cmd := exec.CommandContext(ctx, "node", script, server.URL, evidence)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("approval browser: %v\n%s", err, out)
	}
}

func playwrightApprovalSample() interaction.Approval {
	return interaction.Approval{ID: "playwright-options", Tool: "builtin__computer_invoke", Arguments: `{"method":"playwright.fill","handle_token":"opaque-capability","args":[{"selector":"#field","value":"original fill"}]}`, Hash: strings.Repeat("a", 64), Status: "pending", Parameters: `{"selector":"#field","value":"original fill","timeout_ms":4000}`}
}

func transferApprovalSample() interaction.Approval {
	return interaction.Approval{ID: "transfer-options", Tool: "builtin__computer_discover", Arguments: `{"method":"exportArtifact","args":[{"path":"imports/source"}]}`, Hash: strings.Repeat("a", 64), Status: "pending", Parameters: `{"name":"export.bin","media_type":"application/octet-stream"}`}
}

func uploadApprovalSample() interaction.Approval {
	return interaction.Approval{ID: "upload-options", Tool: "builtin__computer_invoke", Arguments: `{"method":"playwright.upload","handle_token":"opaque-upload-capability","args":[{"selector":"#upload","file":{"computerId":"original","path":"imports/original"}}]}`, Hash: strings.Repeat("b", 64), Status: "pending", Parameters: `{"selector":"#upload","timeout_ms":3000,"name":"approved-upload.txt","media_type":"text/plain"}`}
}
