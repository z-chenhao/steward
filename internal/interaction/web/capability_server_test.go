package web_test

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/config/setup"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/llm/secret"
)

func TestDefaultMemoryHasOneEnabledEntryAndPreferenceLayout(t *testing.T) {
	s, _, root := capabilityFixture(t)
	cfg, err := config.Load("", os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Defaults = &config.Defaults{Memory: true, Search: true, Skills: true}
	m := setup.New(cfg, secretmemory.New())
	m.PrepareDefaults(context.Background())
	s.Capabilities = m
	response := capabilityRequest(s, "GET", "/settings/capabilities", nil)
	body := response.Body.String()
	if response.Code != 200 || strings.Count(body, `class="object-row"`) != 5 || !strings.Contains(body, "Enabled") || strings.Contains(body, `href="/settings/capabilities/embedding"`) || strings.Contains(body, "Not verified") {
		t.Fatal(response.Code, body)
	}
	response = capabilityRequest(s, "GET", "/settings/capabilities/memory", nil)
	body = response.Body.String()
	if !strings.Contains(body, `class="form-panel settings-group"`) || !strings.Contains(body, `<noscript>`) || !strings.Contains(body, `href="/settings/capabilities/embedding"`) || strings.Contains(body, `data-capability-import`) || strings.Contains(body, "Successful candidate tests expire") {
		t.Fatal(body)
	}
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "configuration": {v.Text}, "mode": {"off"}, "action": {"apply"}}
	response = capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	loaded, err := config.Load("", os.LookupEnv)
	if err != nil || loaded.Defaults.Memory || loaded.Defaults.Search || loaded.Embedding != nil {
		t.Fatal(loaded.Defaults, err)
	}
	if _, err = os.Stat(filepath.Join(root, "default-memory.json")); err != nil {
		t.Fatal("disabling memory removed its key reference", err)
	}
}

func TestPreferenceStyleSaveChecksBeforePersisting(t *testing.T) {
	s, m, root := capabilityFixture(t)
	s.CapabilityForms = func(_ context.Context, in contract.Request) (contract.Result, error) {
		return capabilityforms.Project(in), nil
	}
	t.Setenv("STEWARD_APPLY_KEY", strings.Repeat("a", 32))
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "mode": {"custom"}, "key_ref": {"env:STEWARD_APPLY_MISSING"}, "action": {"apply"}}
	response := capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "Memory key unavailable") {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("failed connection saved configuration", err)
	}
	values.Set("key_ref", "env:STEWARD_APPLY_KEY")
	response = capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	loaded, err := config.Load("", os.LookupEnv)
	if err != nil || loaded.Memory == nil || !loaded.Defaults.Search {
		t.Fatal(loaded.Memory, err)
	}
	response = capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code != 409 {
		t.Fatal("stale form overwrote configuration", response.Code)
	}
}

type capabilityCore struct{ web.Core }

func (capabilityCore) CheckChannel(context.Context, string, string, string) error { return nil }

func (capabilityCore) WebStatus(context.Context) (web.Status, error) {
	return web.Status{Readiness: "ready", Status: "idle"}, nil
}
func capabilityFixture(t *testing.T) (*web.Server, *setup.Manager, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_DATA_ROOT", root)
	cfg, err := config.Load("", os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	manager := setup.New(cfg, secret.Environment{})
	return &web.Server{Core: capabilityCore{}, Capabilities: manager}, manager, root
}
func capabilityRequest(s *web.Server, method, path string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
	req.RemoteAddr = "127.0.0.1:9000"
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	s.ServeHTTP(response, req)
	return response
}
func TestCapabilityGuidedServerPreservesExactProofAndDraft(t *testing.T) {
	s, m, root := capabilityFixture(t)
	ctx := context.Background()
	t.Setenv("STEWARD_GUIDED_KEY", strings.Repeat("a", 32))
	t.Setenv("STEWARD_GUIDED_OTHER", strings.Repeat("b", 32))
	v, err := m.Detail(ctx, "memory")
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "configuration": {v.Text}, "enabled": {"yes"}, "key_ref": {"env:STEWARD_GUIDED_KEY"}, "action": {"test"}}
	tested := capabilityRequest(s, "POST", "/capability/configure", values)
	if tested.Code != 303 {
		t.Fatal(tested.Code, tested.Body.String())
	}
	v, err = m.Detail(ctx, "memory")
	if err != nil || !v.Verified || !v.Candidate {
		t.Fatal(v, err)
	}
	values.Set("configuration", v.Text)
	values.Set("action", "save")
	values.Set("key_ref", "env:STEWARD_GUIDED_OTHER")
	edited := capabilityRequest(s, "POST", "/capability/configure", values)
	if edited.Code != 409 || !strings.Contains(edited.Body.String(), `value="env:STEWARD_GUIDED_OTHER"`) {
		t.Fatal("edited candidate saved or draft lost", edited.Code, edited.Body.String())
	}
	if _, err = os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("edited save wrote config", err)
	}
	values.Set("key_ref", "env:STEWARD_GUIDED_KEY")
	saved := capabilityRequest(s, "POST", "/capability/configure", values)
	if saved.Code != 303 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	loaded, err := config.Load("", os.LookupEnv)
	if err != nil || loaded.Memory == nil || loaded.Memory.EncryptionKeyRef != "env:STEWARD_GUIDED_KEY" {
		t.Fatal(loaded.Memory, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil || strings.Contains(string(raw), strings.Repeat("a", 32)) {
		t.Fatal("secret leaked", err)
	}
	v, _ = m.Detail(ctx, "memory")
	if !v.PendingRestart || v.Active {
		t.Fatal("saved settings changed active config", v)
	}
}
func TestCapabilityGuidedServerValidationAndFailedTestAreHonest(t *testing.T) {
	s, m, _ := capabilityFixture(t)
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "enabled": {"yes"}, "key_ref": {"env:STEWARD_NONEXISTENT_GUIDED_KEY"}, "action": {"test"}}
	tested := capabilityRequest(s, "POST", "/capability/configure", values)
	if tested.Code != 303 {
		t.Fatal(tested.Code, tested.Body.String())
	}
	tested = capabilityRequest(s, "GET", tested.Header().Get("Location"), nil)
	if tested.Code != 200 || !strings.Contains(tested.Body.String(), "Memory key unavailable") || !strings.Contains(tested.Body.String(), `value="env:STEWARD_NONEXISTENT_GUIDED_KEY"`) {
		t.Fatal(tested.Code, tested.Body.String())
	}
	if strings.Contains(tested.Body.String(), `value="save" >`) {
		t.Fatal("failed probe enabled save")
	}
	values = url.Values{"editor": {"guided"}, "capability": {"embedding"}, "revision": {v.Revision}, "enabled": {"yes"}, "endpoint": {"http://127.0.0.1:9/v1"}, "model": {`<script>alert("x")</script>`}, "dimensions": {"oops"}, "action": {"test"}}
	invalid := capabilityRequest(s, "POST", "/capability/configure", values)
	if invalid.Code != 422 || !strings.Contains(invalid.Body.String(), `value="oops"`) || !strings.Contains(invalid.Body.String(), "&lt;script&gt;") || strings.Contains(invalid.Body.String(), `<script>alert`) {
		t.Fatal("invalid guided draft was lost or unescaped", invalid.Code, invalid.Body.String())
	}
}
func TestCapabilityGuidedServerStaleRevisionCannotSave(t *testing.T) {
	s, m, root := capabilityFixture(t)
	t.Setenv("STEWARD_GUIDED_STALE", strings.Repeat("s", 32))
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "enabled": {"yes"}, "key_ref": {"env:STEWARD_GUIDED_STALE"}, "action": {"test"}}
	if tested := capabilityRequest(s, "POST", "/capability/configure", values); tested.Code != 303 {
		t.Fatal(tested.Code, tested.Body.String())
	}
	v, _ = m.Detail(context.Background(), "memory")
	values.Set("configuration", v.Text)
	values.Set("action", "save")
	original := []byte("version: 1\napp:\n  data_root: " + root + "\n")
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	stale := capabilityRequest(s, "POST", "/capability/configure", values)
	after, _ := os.ReadFile(path)
	if stale.Code != 409 || string(after) != string(original) {
		t.Fatal("stale guided save changed file", stale.Code, stale.Body.String())
	}
}
func TestCapabilityPagesUseFieldsWithoutExposingConfigurationDocuments(t *testing.T) {
	s, _, _ := capabilityFixture(t)
	for _, id := range []string{"memory", "embedding", "mcp", "skills", "execution", "computer"} {
		response := capabilityRequest(s, http.MethodGet, "/settings/capabilities/"+id, nil)
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, `class="capability-guided"`) || !strings.Contains(body, `name="editor" value="guided"`) || strings.Contains(body, `name="configuration"`) || strings.Contains(body, "YAML") || strings.Contains(body, "config.yaml") || strings.Contains(body, "Download example") {
			t.Fatal(id, response.Code, body)
		}
	}
}

func TestCapabilityGuidedServerIgnoresPostedDocumentAndKeepsNativeRows(t *testing.T) {
	s, m, root := capabilityFixture(t)
	t.Setenv("STEWARD_GUIDED_UNKNOWN", strings.Repeat("u", 32))
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "configuration": {"encryption_key_ref: env:STEWARD_GUIDED_UNKNOWN\nunrepresented: true\n"}, "enabled": {"yes"}, "key_ref": {"env:STEWARD_GUIDED_UNKNOWN"}, "action": {"test"}}
	checked := capabilityRequest(s, "POST", "/capability/configure", values)
	if checked.Code != 303 {
		t.Fatal("guided form trusted the posted document", checked.Code, checked.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("test-only settings written", err)
	}
	values = url.Values{"editor": {"guided"}, "capability": {"mcp"}, "revision": {v.Revision}, "enabled": {"yes"}, "provider_count": {"1"}, "provider.0.original": {"-1"}, "provider.0.name": {"<img src=x onerror=alert(1)>"}, "provider.0.command": {"/private/mcp"}, "provider.0.directory": {"/private/state"}, "provider.0.args": {"argument with spaces"}, "provider.0.tool_count": {"1"}, "provider.0.tool.0.name": {"lookup"}, "provider.0.tool.0.permission": {"read_only"}, "action": {"add-provider"}}
	added := capabilityRequest(s, "POST", "/capability/configure", values)
	body := added.Body.String()
	if added.Code != 200 || !strings.Contains(body, `name="provider_count" value="2"`) || !strings.Contains(body, "argument with spaces") || !strings.Contains(body, "&lt;img") || strings.Contains(body, "<img src=x") {
		t.Fatal("native provider row action lost data or escaped content", added.Code, body)
	}
}

func TestOrdinaryCapabilityPageDoesNotRebaseOntoFailedProbe(t *testing.T) {
	s, m, _ := capabilityFixture(t)
	v, _ := m.Detail(context.Background(), "memory")
	failed, err := m.Test(context.Background(), "memory", v.Revision, "encryption_key_ref: env:STEWARD_MISSING_DRAFT_KEY\n")
	if err != nil || failed.Verified {
		t.Fatal("expected a failed probe", err)
	}
	candidate, err := m.Detail(context.Background(), "memory")
	if err != nil || !candidate.Candidate {
		t.Fatal("test did not create an independent candidate", err)
	}
	response := capabilityRequest(s, http.MethodGet, "/settings/capabilities/memory", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), `value="env:STEWARD_MISSING_DRAFT_KEY"`) {
		t.Fatal("saved settings were replaced by a failed candidate", response.Code)
	}
}

type capabilityProbeCounter struct {
	web.CapabilitySettings
	tests, saves int
}

func (c *capabilityProbeCounter) Test(ctx context.Context, id, revision, text string) (web.Capability, error) {
	c.tests++
	return c.CapabilitySettings.Test(ctx, id, revision, text)
}
func (c *capabilityProbeCounter) Save(ctx context.Context, id, revision, text string) error {
	c.saves++
	return c.CapabilitySettings.Save(ctx, id, revision, text)
}
func TestCapabilityPluginFailureCannotTestSaveOrFallBack(t *testing.T) {
	s, m, _ := capabilityFixture(t)
	counter := &capabilityProbeCounter{CapabilitySettings: m}
	s.Capabilities = counter
	s.CapabilityForms = func(context.Context, contract.Request) (contract.Result, error) {
		return contract.Result{}, errors.New("private plugin failure")
	}
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "mode": {"off"}, "action": {"apply"}}
	response := capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code != 422 || counter.tests != 0 || counter.saves != 0 || strings.Contains(response.Body.String(), "private plugin failure") {
		t.Fatal(response.Code, counter.tests, counter.saves, response.Body.String())
	}
}

func TestCapabilityInvalidOwnedViewCannotProbeOrSave(t *testing.T) {
	s, m, _ := capabilityFixture(t)
	counter := &capabilityProbeCounter{CapabilitySettings: m}
	s.Capabilities = counter
	s.CapabilityForms = func(_ context.Context, in contract.Request) (contract.Result, error) {
		out := capabilityforms.Project(in)
		out.View.Source = strings.Replace(out.View.Source, `action="/capability/configure"`, `action="/model/delete"`, 1)
		out.View.Digest = rsiplugin.FileDigest([]byte(out.View.Source))
		return out, nil
	}
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "mode": {"off"}, "action": {"apply"}}
	response := capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code < 400 || counter.tests != 0 || counter.saves != 0 {
		t.Fatal("invalid owned view admitted effects", response.Code, counter.tests, counter.saves)
	}
}

func TestCapabilityEncoderCannotSubstituteAuthorizedCredentialReference(t *testing.T) {
	s, m, _ := capabilityFixture(t)
	counter := &capabilityProbeCounter{CapabilitySettings: m}
	s.Capabilities = counter
	t.Setenv("STEWARD_CHOSEN_CAPABILITY_KEY", strings.Repeat("a", 32))
	t.Setenv("STEWARD_FORGED_CAPABILITY_KEY", strings.Repeat("b", 32))
	s.CapabilityForms = func(_ context.Context, in contract.Request) (contract.Result, error) {
		out := capabilityforms.Project(in)
		if in.Operation == "submit" {
			out.Text = strings.Replace(out.Text, "env:STEWARD_CHOSEN_CAPABILITY_KEY", "env:STEWARD_FORGED_CAPABILITY_KEY", 1)
		}
		return out, nil
	}
	v, _ := m.Detail(context.Background(), "memory")
	values := url.Values{"editor": {"guided"}, "capability": {"memory"}, "revision": {v.Revision}, "mode": {"custom"}, "key_ref": {"env:STEWARD_CHOSEN_CAPABILITY_KEY"}, "action": {"apply"}}
	response := capabilityRequest(s, "POST", "/capability/configure", values)
	if response.Code < 400 || counter.tests != 0 || counter.saves != 0 {
		t.Fatal("candidate substituted credential authority", response.Code, counter.tests, counter.saves)
	}
}
