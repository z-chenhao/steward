package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestCapabilityFormsActualUpgradeRollbackStopAndCold(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, e := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	in := contract.Request{Operation: "prepare", ID: "computer", WithView: true, Values: []contract.Field{}}
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), CapabilityForms: defaultFeaturePackage(t, "capability-settings", nil)}); err != nil {
		t.Fatal(err)
	}
	initial, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check := func(port string) {
		t.Helper()
		out, e := a.readCapabilityFormWithHost(ctx, host, in)
		if out.View == nil || out.Copy == nil {
			t.Fatal("selected view resource absent", e)
		}
		marker := `capabilityTr "Computer connection"`
		if port == "2222" {
			marker = `capabilityTr "Versioned computer connection"`
		}
		if !strings.Contains(out.View.Source, marker) {
			t.Fatal("actual view resource not selected")
		}
		if e != nil || out.Form == nil || out.Form.Computer.Port != port {
			t.Fatal("actual form policy not selected", out, e)
		}
	}
	a.capabilitySettings.Presentation = func(ctx context.Context, facts contract.PresentationFacts) (contract.Presentation, error) {
		return a.readCapabilityPresentationWithHost(ctx, host, facts)
	}
	if err = secrets.Put(ctx, "credential:capability-memory", []byte(strings.Repeat("k", 32))); err != nil {
		t.Fatal(err)
	}
	checkPresentation := func(prefix string) {
		t.Helper()
		facts, e := a.capabilitySettings.Detail(ctx, "memory")
		if e != nil || facts.Title != prefix+"Memory" || facts.PresentationUnavailable {
			t.Fatal("actual manager catalogue", facts, e)
		}
		tested, e := a.capabilitySettings.Test(ctx, "memory", facts.Revision, "encryption_key_ref: credential:capability-memory\n")
		if e != nil || !tested.Verified || tested.State != "ready" || tested.Message != prefix+"The referenced 32-byte encryption key is available. No memory was read or written." {
			t.Fatal("actual diagnostic lost host fact or owning copy", tested, e)
		}
	}
	checkPresentation("")
	check("22")
	var policyID string
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, initial.Revision).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	replace := map[string]string{}
	for _, name := range []string{"v1/forms.go", "plugin.go", "v1/presentation.go"} {
		source, e := filepath.Abs(filepath.Join("../plugin/capabilityforms", name))
		if e != nil {
			t.Fatal(e)
		}
		code, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		text := string(code)
		if name == "v1/forms.go" {
			if strings.Count(text, `c.Port = "22"`) != 1 {
				t.Fatal("port marker missing")
			}
			text = strings.Replace(text, `c.Port = "22"`, `c.Port = "2222"`, 1)
		} else if name == "v1/presentation.go" {
			text = strings.Replace(text, `v.Title = "Memory"`, `v.Title = "v2 Memory"`, 1)
			text = strings.Replace(text, "The referenced 32-byte encryption key is available. No memory was read or written.", "v2 The referenced 32-byte encryption key is available. No memory was read or written.", 1)
		} else {
			text = strings.Replace(text, `Version: "1.0.0"`, `Version: "1.1.0"`, 1)
		}
		replacement := filepath.Join(t.TempDir(), filepath.Base(name))
		if e = os.WriteFile(replacement, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		replace[source] = replacement
	}
	for _, name := range []string{"capabilities.html", "copy.json"} {
		source, e := filepath.Abs(filepath.Join("../plugin/capabilityforms/v1/view", name))
		if e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		text := string(raw)
		if name == "capabilities.html" {
			text = strings.Replace(text, `capabilityTr "Computer connection"`, `capabilityTr "Versioned computer connection"`, 1)
		} else {
			var entries []contract.Translation
			if e = json.Unmarshal(raw, &entries); e != nil {
				t.Fatal(e)
			}
			entries = append(entries, contract.Translation{Key: "Versioned computer connection", Value: "独立版本的桌面连接"})
			raw, _ = json.Marshal(entries)
			text = string(raw)
		}
		replacement := filepath.Join(t.TempDir(), name)
		if e = os.WriteFile(replacement, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		replace[source] = replacement
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": replace})
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("go", "build", "-overlay", overlayPath, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/capability-settings").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	raw, err := os.ReadFile("../../plugins/capability-settings/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	copyCapabilityAssets(t, directory)
	for source, replacement := range replace {
		if strings.Contains(source, "/view/") {
			raw, e := os.ReadFile(replacement)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(directory, "view", filepath.Base(source)), raw, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := host.Install(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, initial.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "capability-settings" {
			graph.Graph.Bindings[i].PackageDigest = installed.Digest
		}
	}
	next, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	policy, e := host.Store.Policy(ctx, policyID)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(policy)
	var variant rsi.Policy
	if e = json.Unmarshal(encoded, &variant); e != nil {
		t.Fatal(e)
	}
	variant.ID = "capability-presentation-v2"
	for i := range variant.Cases {
		if variant.Cases[i].Contract == contract.PresentationService {
			var expected contract.Presentation
			if e = json.Unmarshal(variant.Cases[i].Expected, &expected); e != nil {
				t.Fatal(e)
			}
			for j := range expected.Descriptions {
				if expected.Descriptions[j].ID == "memory" {
					expected.Descriptions[j].Title = "v2 Memory"
				}
			}
			expected.Copy.ProbeMemoryReady = "v2 " + expected.Copy.ProbeMemoryReady
			variant.Cases[i].Expected, _ = json.Marshal(expected)
		}
	}
	if e = host.Store.PutPolicy(ctx, variant); e != nil {
		t.Fatal(e)
	}
	selectedPolicy := variant.ID
	selectGraph := func(id, from, to string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: selectedPolicy, Goal: "independent capability form iteration"}); e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: from, Target: to, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	selectGraph("forms-upgrade", initial.Revision, next.Revision)
	check("2222")
	checkPresentation("v2 ")
	selectedPolicy = policyID
	selectGraph("forms-rollback", next.Revision, initial.Revision)
	check("22")
	checkPresentation("")
	if _, err = host.Store.Deactivate(ctx, "forms-stop", initial.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = a.readCapabilityFormWithHost(ctx, host, in); err == nil {
		t.Fatal("stop restored native policy")
	}
	retained, e := a.capabilitySettings.Detail(ctx, "memory")
	if e != nil || !retained.PresentationUnavailable || !retained.Verified || retained.Message != "The referenced 32-byte encryption key is available. No memory was read or written." {
		t.Fatal("stopped presentation erased accepted proof", retained, e)
	}
	if _, e = a.capabilitySettings.Test(ctx, "memory", retained.Revision, "encryption_key_ref: credential:capability-memory\n"); e == nil {
		t.Fatal("stopped presentation admitted a new probe")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	a.capabilitySettings.Presentation = func(ctx context.Context, facts contract.PresentationFacts) (contract.Presentation, error) {
		return a.readCapabilityPresentationWithHost(ctx, host, facts)
	}
	cold, e := a.capabilitySettings.Detail(ctx, "memory")
	if e != nil || !cold.PresentationUnavailable {
		t.Fatal("cold metadata restored native", cold, e)
	}
	if _, e = a.readCapabilityPresentationWithHost(ctx, host, contract.PresentationFacts{Root: root}); e == nil {
		t.Fatal("cold presentation service restored native")
	}
	if _, err = a.readCapabilityFormWithHost(ctx, host, in); err == nil {
		t.Fatal("first cold read restored native policy")
	}
}

func copyCapabilityAssets(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "view"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"capabilities.html", "copy.json"} {
		raw, err := os.ReadFile(filepath.Join("../plugin/capabilityforms/v1/view", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "view", name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
