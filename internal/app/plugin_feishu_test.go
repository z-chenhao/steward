package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeishuStatusPreparationFailureRetainsFacts(t *testing.T) {
	calls := 0
	a := &App{prepareFeaturePlugins: func(context.Context) error { calls++; return errors.New("private diagnostic") }}
	out := a.FeishuStatus(context.Background(), false)
	if calls != 1 || !out.GuidanceUnavailable || out.Action != "" || out.Channel != "feishu" || out.Mode != "disabled" || out.State != "not_configured" {
		t.Fatal("presentation changed trusted facts or fell back", out, calls)
	}
}
func TestFeishuStatusActualUpgradeRollbackStopAndCold(t *testing.T) {
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
	facts := contract.StatusFacts{State: "not_configured"}
	if _, managed, e := a.readFeishuStatusWithHost(ctx, host, facts); e != nil || managed {
		t.Fatal("historical embedding boundary lost", managed, e)
	}
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), Feishu: defaultFeaturePackage(t, "feishu-chat", nil)}); err != nil {
		t.Fatal(err)
	}
	initial, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var policyID string
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, initial.Revision).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	baseline, managed, err := a.readFeishuStatusWithHost(ctx, host, facts)
	if err != nil || !managed || baseline.Action == "" {
		t.Fatal(baseline, managed, err)
	}
	check := func(prefix string) {
		t.Helper()
		out, managed, e := a.readFeishuStatusWithHost(ctx, host, facts)
		if e != nil || !managed || out.Action != prefix+baseline.Action {
			t.Fatal("actual owning guidance not selected", out, managed, e)
		}
	}
	cardFacts := contract.CardFacts{Fallback: "Full answer", Components: []contract.CardComponent{}}
	checkCard := func(want string) {
		t.Helper()
		out, e := a.readFeishuCardWithHost(ctx, host, cardFacts)
		if e != nil || out.Text != want || out.Mode != "plain" {
			t.Fatal("actual card selection", out, e)
		}
	}
	checkFeedback := func(want string) {
		t.Helper()
		out, e := a.readFeishuCardWithHost(ctx, host, contract.CardFacts{Components: []contract.CardComponent{}, Feedback: true})
		if e != nil || out.FeedbackCopy == nil || out.FeedbackCopy.Saved != want {
			t.Fatal("actual feedback policy", out, e)
		}
	}
	checkFeedback("Feedback saved. Ghost will review it.")
	checkCard("Full answer")
	check("")
	source, e := filepath.Abs("../plugin/feishuchat/status.go")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	code := strings.Replace(string(raw), "Version: \"1.0.0\"", "Version: \"1.1.0\"", 1)
	marker := "return contract.StatusProjection{Action: action}"
	if strings.Count(code, marker) != 1 {
		t.Fatal("status marker missing")
	}
	code = strings.Replace(code, marker, `if action!="" {action="v2 "+action};`+marker, 1)
	dir := t.TempDir()
	replacement := filepath.Join(t.TempDir(), "status.go")
	if e = os.WriteFile(replacement, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	cardSource, e := filepath.Abs("../plugin/feishuchat/legacyv1/card.go")
	if e != nil {
		t.Fatal(e)
	}
	cardCode, e := os.ReadFile(cardSource)
	if e != nil {
		t.Fatal(e)
	}
	cardReplacement := filepath.Join(t.TempDir(), "card.go")
	cardVariant := strings.Replace(string(cardCode), "out := contract.CardProjection{", `f.Fallback="v2 "+f.Fallback;out := contract.CardProjection{`, 1)
	cardVariant = strings.Replace(cardVariant, "Feedback saved. Ghost will review it.", "v2 Feedback saved. Ghost will review it.", 1)
	if cardVariant == string(cardCode) {
		t.Fatal("card marker missing")
	}
	if e = os.WriteFile(cardReplacement, []byte(cardVariant), 0600); e != nil {
		t.Fatal(e)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: replacement, cardSource: cardReplacement}})
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	if e = os.WriteFile(overlayFile, overlay, 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("go", "build", "-overlay", overlayFile, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/feishu-chat").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	manifestRaw, e := os.ReadFile("../../plugins/feishu-chat/steward.plugin.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest rsiplugin.Manifest
	if e = rsiplugin.Decode(manifestRaw, &manifest); e != nil {
		t.Fatal(e)
	}
	manifest.Version = "1.1.0"
	manifest, e = rsi.Seal(dir, manifest)
	if e != nil {
		t.Fatal(e)
	}
	manifestRaw, _ = json.Marshal(manifest)
	if e = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), manifestRaw, 0600); e != nil {
		t.Fatal(e)
	}
	installed, e := host.Install(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	graph, e := host.Store.Graph(ctx, initial.Revision)
	if e != nil {
		t.Fatal(e)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "feishu-chat" {
			graph.Graph.Bindings[i].PackageDigest = installed.Digest
		}
	}
	next, e := host.Prepare(ctx, graph.Graph)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(policy)
	var variant rsi.Policy
	if e = json.Unmarshal(encoded, &variant); e != nil {
		t.Fatal(e)
	}
	variant.ID = "feishu-status-v2"
	for i := range variant.Cases {
		if variant.Cases[i].Contract == contract.Card {
			var expected contract.CardProjection
			if e = json.Unmarshal(variant.Cases[i].Expected, &expected); e != nil {
				t.Fatal(e)
			}
			expected.Text = "v2 " + expected.Text
			variant.Cases[i].Expected, _ = json.Marshal(expected)
		}
		if variant.Cases[i].Contract == contract.Status {
			var expected contract.StatusProjection
			if e = json.Unmarshal(variant.Cases[i].Expected, &expected); e != nil {
				t.Fatal(e)
			}
			if expected.Action != "" {
				expected.Action = "v2 " + expected.Action
			}
			variant.Cases[i].Expected, _ = json.Marshal(expected)
		}
	}
	if e = host.Store.PutPolicy(ctx, variant); e != nil {
		t.Fatal(e)
	}
	selectGraph := func(id, from, to, policy string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent Feishu status guidance"}); e != nil {
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
	selectGraph("feishu-upgrade", initial.Revision, next.Revision, variant.ID)
	check("v2 ")
	checkCard("v2 Full answer")
	checkFeedback("v2 Feedback saved. Ghost will review it.")
	selectGraph("feishu-rollback", next.Revision, initial.Revision, policy.ID)
	check("")
	checkCard("Full answer")
	checkFeedback("Feedback saved. Ghost will review it.")
	if _, e = host.Store.Deactivate(ctx, "feishu-stop", initial.Revision); e != nil {
		t.Fatal(e)
	}
	if _, managed, e := a.readFeishuStatusWithHost(ctx, host, facts); e == nil || !managed {
		t.Fatal("stop restored native guidance", managed, e)
	}
	if _, e := a.readFeishuCardWithHost(ctx, host, cardFacts); e == nil {
		t.Fatal("stop restored native card")
	}
	out := a.FeishuStatus(ctx, false)
	if !out.GuidanceUnavailable || out.Action != "" || out.State != "not_configured" || out.Mode != "disabled" {
		t.Fatal("stopped guidance changed facts", out)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	if _, managed, e := a.readFeishuStatusWithHost(ctx, host, facts); e == nil || !managed {
		t.Fatal("cold stop restored native guidance", managed, e)
	}
	if _, e := a.readFeishuCardWithHost(ctx, host, cardFacts); e == nil {
		t.Fatal("cold stop restored native card")
	}
	out = a.FeishuStatus(ctx, false)
	if !out.GuidanceUnavailable || out.Action != "" || out.State != "not_configured" {
		t.Fatal("cold stop lost factual status", out)
	}
}

func TestFeishuCardStorelessHistoricalEmbedding(t *testing.T) {
	a := &App{}
	out, err := a.readFeishuCard(context.Background(), contract.CardFacts{Fallback: "Historical answer", Components: []contract.CardComponent{}})
	if err != nil || out.Mode != "plain" || out.Text != "Historical answer" {
		t.Fatal(out, err)
	}
}
