package app

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func independentWatchdogPackage(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../plugin/servicewatchdog/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.1.0"`, `Version: "1.0.0"`, 1)
	code = strings.Replace(code, ", AlertAfterSeconds: 60", "", 1)
	code = strings.Replace(code, "Attempts: 3, RetryDelayMS: 60000", "Attempts: 2, RetryDelayMS: 120000", 1)

	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../cmd/plugins/service-watchdog").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/service-watchdog/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.0.0"
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
func TestServiceWatchdogActualProducerUpgradeRollbackDeactivationAndCold(t *testing.T) {
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
	h := a.Plugins()
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	h.Catalog.Launcher = carrier
	raw, err := os.ReadFile("../rsi/builtin/service-watchdog-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var c rsi.Case
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	c.Expected = json.RawMessage(`{"attempts":3,"retry_delay_ms":60000,"alert_after_seconds":60}`)
	owner := rsi.Policy{ID: "watchdog-original", AllowedContracts: []string{contract.Service}, Cases: []rsi.Case{c}}
	if err = h.Store.PutPolicy(ctx, owner); err != nil {
		t.Fatal(err)
	}
	prepare := func(source string) string {
		t.Helper()
		p, err := h.Install(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		g, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "service-watchdog", PackageDigest: p.Digest, Config: json.RawMessage(`{}`), StateNamespace: "service-watchdog", StateVersion: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return g.Revision
	}
	activate := func(id, base, target, policy string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: base, Target: target, PolicyID: policy, Goal: "independent restart recipe"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: base, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(revision string, attempts uint8, delay uint32, alertSeconds uint16) {
		t.Helper()
		r, err := a.serviceRestartRecipeWithHost(ctx, h)
		if err != nil || r.GraphRevision != revision || r.Recipe.Attempts != attempts || r.Recipe.RetryDelayMS != delay || r.Recipe.AlertAfterSeconds != alertSeconds {
			t.Fatal("wrong admitted producer", r, err)
		}
	}
	original := prepare(defaultFeaturePackage(t, "service-watchdog", nil))
	activate("watchdog-first", "", original, owner.ID)
	check(original, 3, 60000, 60)
	next := prepare(independentWatchdogPackage(t))
	owner.ID = "watchdog-independent"
	owner.Cases[0].Expected = json.RawMessage(`{"attempts":2,"retry_delay_ms":120000}`)
	if err = h.Store.PutPolicy(ctx, owner); err != nil {
		t.Fatal(err)
	}
	activate("watchdog-upgrade", original, next, owner.ID)
	check(next, 2, 120000, 0)
	activate("watchdog-rollback", next, original, "watchdog-original")
	check(original, 3, 60000, 60)
	if _, err = h.Store.Deactivate(ctx, "watchdog-off", original); err != nil {
		t.Fatal(err)
	}
	if r, err := a.serviceRestartRecipeWithHost(ctx, h); err == nil || r != nil {
		t.Fatal("deactivation restored native recipe", r, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	h = a.Plugins()
	h.Catalog.Launcher = carrier
	// First dependent read after actual reopen, with no activation or repair.
	if r, err := a.serviceRestartRecipeWithHost(ctx, h); err == nil || r != nil {
		t.Fatal("cold read restored policy", r, err)
	}
}
