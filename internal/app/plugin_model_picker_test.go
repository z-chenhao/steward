package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelPickerActualIndependentUpgradePinnedReaderRollbackAndCold(t *testing.T) {
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
	defer func() { a.Close() }()
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, e := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), ModelConnections: defaultFeaturePackage(t, "model-connections", nil)}); err != nil {
		t.Fatal(err)
	}
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	facts := contract.Facts{Candidates: []contract.Candidate{}, Catalogues: []contract.Catalogue{}}
	check := func(reference string, variant bool) {
		t.Helper()
		input := facts
		input.ReaderRevision = reference
		menu, managed, e := a.readModelMenuWithHost(ctx, host, input)
		if e != nil || !managed || menu.ReaderRevision == "" {
			t.Fatal("missing actual frozen menu identity", menu, e)
		}
		if reference != "" && menu.ReaderRevision != reference {
			t.Fatal("requested frozen reader changed")
		}
		picker, e := a.readModelPickerWithHost(ctx, host, menu.ReaderRevision)
		if e != nil {
			t.Fatal(e)
		}
		prefix := ""
		if variant {
			prefix = "v2 "
		}
		if picker.English.Label != prefix+"Model" || picker.Chinese.Label != prefix+"模型" || len(picker.English.States) != 128 {
			t.Fatal("wrong actual picker version", picker.English.Label, e)
		}
		if picker.English.States[2].Busy == variant || !picker.English.States[4].Busy {
			t.Fatal("view rewrite changed switching lock or ignored independent loading policy")
		}
	}
	check("", false)
	directory := defaultFeaturePackage(t, "model-connections", nil)
	replacements := map[string]string{}
	for _, name := range []string{"menu.go", "picker.go"} {
		source, _ := filepath.Abs(filepath.Join("../plugin/modelconnections", name))
		raw, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		code := string(raw)
		if name == "menu.go" {
			code = strings.Replace(code, `Version: "1.1.0"`, `Version: "1.2.0"`, 1)
		} else {
			code = strings.Replace(code, `t("Model")`, `"v2 " + t("Model")`, 1)
			code = strings.Replace(code, "saving || loading", "saving || (loading && ready)", 1)
			code = strings.Replace(code, `row.PendingText = t("Loading models…")`, `row.PendingText = t("Loading models…") + " v2"`, 1)
		}
		target := filepath.Join(t.TempDir(), name)
		if e = os.WriteFile(target, []byte(code), 0600); e != nil {
			t.Fatal(e)
		}
		replacements[source] = target
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": replacements})
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayFile, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("go", "build", "-overlay", overlayFile, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/model-connections").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "steward.plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.2.0"
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
	graph, err := host.Store.Graph(ctx, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "model-connections" {
			graph.Graph.Bindings[i].PackageDigest = installed.Digest
		}
	}
	second, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	var oldPolicy string
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&oldPolicy); err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, oldPolicy)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "independent-picker-view"
	for i := range policy.Cases {
		if policy.Cases[i].Contract == contract.Picker {
			var expected contract.PickerPresentation
			if e := json.Unmarshal(policy.Cases[i].Expected, &expected); e != nil {
				t.Fatal(e)
			}
			expected.English.Label = "v2 Model"
			expected.Chinese.Label = "v2 模型"
			for _, view := range []*contract.PickerView{&expected.English, &expected.Chinese} {
				for j := range view.States {
					view.States[j].Busy = view.States[j].ID&4 != 0 || (view.States[j].ID&1 != 0 && view.States[j].ID&2 != 0)
					if view.States[j].ID&1 == 0 {
						view.States[j].PendingText += " v2"
					}
				}
			}
			policy.Cases[i].Expected, _ = json.Marshal(expected)
		}
	}
	if err = host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	activate := func(id, from, to, policyID string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policyID, Goal: "independent complete picker presentation"}); e != nil {
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
	activate("picker-upgrade", first.Revision, second.Revision, policy.ID)
	check("", true)
	check(first.Revision, false)
	activate("picker-rollback", second.Revision, first.Revision, oldPolicy)
	if err = host.Store.Retire(ctx, installed.Digest); err != nil {
		t.Fatal(err)
	}
	check("", false)
	check(second.Revision, true)
	if err = host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, e := a.readModelPickerWithHost(ctx, host, second.Revision); e == nil {
		t.Fatal("Guardian stop ignored by retained picker")
	}
	if err = host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	check(second.Revision, true)
	if _, err = host.Store.Deactivate(ctx, "picker-disable", first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, e := a.readModelMenuWithHost(ctx, host, facts); e == nil {
		t.Fatal("deactivation restored current native presentation")
	}
	// Retained references are read-only; they do not restore the active choice.
	check(second.Revision, true)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	if _, _, e := a.readModelMenuWithHost(ctx, host, facts); e == nil {
		t.Fatal("cold current read restored native")
	}
	check(second.Revision, true)
	// An explicitly selected pre-picker1.0 snapshot retains its original UI.
	legacyDirectory := defaultFeaturePackage(t, "model-connections", nil)
	source, _ := filepath.Abs("../plugin/modelconnections/menu.go")
	code, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	text := string(code)
	start := strings.Index(text, "\t\tcontract.Picker:")
	end := strings.Index(text, "\t\tcontract.ConnectionPolicy:")
	if start < 0 || end < start {
		t.Fatal("current picker handler boundary absent")
	}
	text = text[:start] + text[end:]
	text = strings.Replace(text, `Version: "1.1.0"`, `Version: "1.0.0"`, 1)
	replacement := filepath.Join(t.TempDir(), "legacy-menu.go")
	if e = os.WriteFile(replacement, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	legacyOverlay, _ := json.Marshal(map[string]map[string]string{"Replace": {source: replacement}})
	legacyOverlayFile := filepath.Join(t.TempDir(), "legacy-overlay.json")
	if e = os.WriteFile(legacyOverlayFile, legacyOverlay, 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("go", "build", "-overlay", legacyOverlayFile, "-o", filepath.Join(legacyDirectory, "plugin"), "../../cmd/plugins/model-connections").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	raw, e = os.ReadFile(filepath.Join(legacyDirectory, "steward.plugin.json"))
	if e != nil {
		t.Fatal(e)
	}
	var legacyManifest rsiplugin.Manifest
	if e = rsiplugin.Decode(raw, &legacyManifest); e != nil {
		t.Fatal(e)
	}
	legacyManifest.Version = "1.0.0"
	services := legacyManifest.Provides[:0]
	for _, service := range legacyManifest.Provides {
		if service.Contract != contract.Picker {
			services = append(services, service)
		}
	}
	legacyManifest.Provides = services
	legacyManifest, e = rsi.Seal(legacyDirectory, legacyManifest)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(legacyManifest)
	if e = os.WriteFile(filepath.Join(legacyDirectory, "steward.plugin.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	legacyPackage, e := host.Install(ctx, legacyDirectory)
	if e != nil {
		t.Fatal(e)
	}
	legacyGraph, e := host.Store.Graph(ctx, first.Revision)
	if e != nil {
		t.Fatal(e)
	}
	for i := range legacyGraph.Graph.Bindings {
		if legacyGraph.Graph.Bindings[i].InstanceID == "model-connections" {
			legacyGraph.Graph.Bindings[i].PackageDigest = legacyPackage.Digest
		}
	}
	retained, e := host.Prepare(ctx, legacyGraph.Graph)
	if e != nil {
		t.Fatal(e)
	}
	legacyPolicy, e := host.Store.Policy(ctx, oldPolicy)
	if e != nil {
		t.Fatal(e)
	}
	legacyPolicy.ID = "pre-picker-original-owner"
	contracts := legacyPolicy.AllowedContracts[:0]
	for _, name := range legacyPolicy.AllowedContracts {
		if name != contract.Picker {
			contracts = append(contracts, name)
		}
	}
	legacyPolicy.AllowedContracts = contracts
	cases := legacyPolicy.Cases[:0]
	for _, c := range legacyPolicy.Cases {
		if c.Contract != contract.Picker {
			cases = append(cases, c)
		}
	}
	legacyPolicy.Cases = cases
	if e = host.Store.PutPolicy(ctx, legacyPolicy); e != nil {
		t.Fatal(e)
	}
	activate("picker-explicit-historical", "", retained.Revision, legacyPolicy.ID)
	check("", false)
	check(second.Revision, true)
	profiles, e := a.Models().ListProfiles(ctx)
	if e != nil || len(profiles) != 0 {
		t.Fatal("display program changed actual model facts", profiles, e)
	}
	var calls int
	if e = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT count(*) FROM rsi_call`).Scan(&calls); e != nil || calls != 0 {
		t.Fatal("pure picker created effect receipts", calls, e)
	}
}
