package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/skilllibrary/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func installSkillPlugin(t *testing.T, s *Store, r *Runs) rsi.Host {
	t.Helper()
	if err := r.ConfigureSkillLibrary(); err != nil {
		t.Fatal(err)
	}
	return installRuntimeFeature(t, s, r, "skill-library", rsi.Policy{ID: "skill-owner-fixture", AllowedContracts: []string{contract.SearchContract, contract.ResourceContract}, Cases: []rsi.Case{
		{InstanceID: "skill-library", Contract: contract.SearchContract, Input: json.RawMessage(`{"query":"","limit":8,"entries":[{"name":"zeta","description":"research"},{"name":"alpha","description":"coding"}]}`), Expected: json.RawMessage(`{"indices":[1,0]}`)},
		{InstanceID: "skill-library", Contract: contract.ResourceContract, Input: json.RawMessage(`{"path":""}`), Expected: json.RawMessage(`{"path":"SKILL.md"}`)},
		{InstanceID: "skill-library", Contract: contract.ResourceContract, Input: json.RawMessage(`{"path":"references/details.md"}`), Expected: json.RawMessage(`{"path":"references/details.md"}`)},
	}})
}
func skillRuntime(t *testing.T) (*Store, *Runs, agent.Actor, *loopProvider, string) {
	t.Helper()
	s, r, _, a, p := loopFixture(t)
	root := t.TempDir()
	for _, name := range []string{"alpha", "zeta"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + name + " reusable research\n---\nPRIVATE_SKILL_BODY_" + name
		for file, content := range map[string][]byte{"SKILL.md": []byte(body), "QUICK.md": []byte("QUICK_PRIVATE_" + name), "binary.bin": {0xff, 0x00, 0xfe}} {
			if err := os.WriteFile(filepath.Join(root, name, file), content, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	defs, err := skill.Definitions(skill.Config{Root: root, Scope: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	r.registry = registry
	a.Registry = registry
	if err = r.ConfigureSkillLibrary(); err != nil {
		t.Fatal(err)
	}
	return s, r, a, p, root
}
func skillReceipts(t *testing.T, s *Store, run string) map[string]tool.Result {
	t.Helper()
	rows, err := s.db.Query(`SELECT tool_name,result_json FROM tool_invocation WHERE run_id=?`, run)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]tool.Result{}
	for rows.Next() {
		var name, raw string
		if err = rows.Scan(&name, &raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		if err = json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		out[name] = result
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func skillMainPolicy(req llm.GenerationRequest) (llm.GenerationResult, error) {
	if req.Messages[len(req.Messages)-1].Role == "tool" {
		return llm.GenerationResult{Text: "method loaded"}, nil
	}
	return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "list", Name: "builtin__skill_list", Arguments: `{}`}, {ID: "view", Name: "builtin__skill_view", Arguments: `{"name":"alpha"}`}}}, nil
}
func TestSkillPluginActualMainAndLegacyFrozenResources(t *testing.T) {
	s, r, a, p, root := skillRuntime(t)
	r.ConfigurePlugins(rsi.NewCatalog(filepath.Dir(s.path)))
	old := mustSubmit(t, a, "skill-before", "perform specialized research")
	installSkillPlugin(t, s, r)
	p.respond = skillMainPolicy
	mustDrain(t, a)
	for name, result := range skillReceipts(t, s, old.RunID) {
		if result.Error != nil {
			t.Fatal("legacy reader changed", name, result)
		}
	}
	current := mustSubmit(t, a, "skill-after", "perform specialized research again")
	mustDrain(t, a)
	results := skillReceipts(t, s, current.RunID)
	if len(results) != 2 || !strings.Contains(results["builtin__skill_list"].Text, "alpha") || !strings.Contains(results["builtin__skill_view"].Text, "PRIVATE_SKILL_BODY_alpha") {
		t.Fatal("selected methods not consumed", results)
	}
	if strings.Contains(results["builtin__skill_list"].Text, "version") || strings.Contains(results["builtin__skill_list"].Text, "scope") || strings.Contains(results["builtin__skill_list"].Text, "PRIVATE_SKILL_BODY") {
		t.Fatal("discovery exposed private resources", results)
	}
	assertFrozenContextRun(t, s, current.RunID)
	if err := os.WriteFile(filepath.Join(root, "alpha", "SKILL.md"), []byte("unreviewed replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	next := mustSubmit(t, a, "skill-drift", "read exact approved method")
	mustDrain(t, a)
	if result := skillReceipts(t, s, next.RunID)["builtin__skill_view"]; result.Error == nil || result.Error.Code != "NON_DETERMINISTIC_REPLAY" {
		t.Fatal("unreviewed bytes substituted", result)
	}
}
func TestSkillPluginRejectsInventedDuplicateAndOversizedSelection(t *testing.T) {
	entries := []skill.Summary{{Name: "alpha", Description: "exact source"}}
	for _, p := range []contract.SearchPlan{{Indices: []int{1}}, {Indices: []int{-1}}, {Indices: []int{0, 0}}} {
		if _, err := skillSelection(entries, 2, p); !errors.Is(err, rsi.ErrInvalid) {
			t.Fatal("accepted invented/duplicate catalog entry", p, err)
		}
	}
}
func TestSkillPluginDisabledDoesNotRestoreNativeDiscoveryOrView(t *testing.T) {
	ctx := context.Background()
	s, r, a, p, _ := skillRuntime(t)
	h := installSkillPlugin(t, s, r)
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Store.Deactivate(ctx, "skill-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	p.respond = skillMainPolicy
	run := mustSubmit(t, a, "skill-disabled", "discover a method")
	mustDrain(t, a)
	results := skillReceipts(t, s, run.RunID)
	if len(results) != 2 {
		t.Fatal(results)
	}
	for name, result := range results {
		if result.Error == nil || result.Error.Code != "PROVIDER_UNAVAILABLE" {
			t.Fatal("native fallback after deactivation", name, result)
		}
	}
}

func skillVersionPackage(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	path, err := filepath.Abs("../../plugin/skilllibrary/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.1"`, `Version: "1.1.0"`, 1)
	code = strings.Replace(code, `path = "SKILL.md"`, `path = "QUICK.md"`, 1)
	code = strings.Replace(code, `f.Entries[indices[i]].Name < f.Entries[indices[j]].Name`, `f.Entries[indices[i]].Name > f.Entries[indices[j]].Name`, 1)
	override := filepath.Join(t.TempDir(), "plugin.go")
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/skill-library").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../../plugins/skill-library/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = "1.1.0"
	m, err = rsi.Seal(source, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return source
}
func TestSkillPluginIndependentUpgradeRollbackAndRetainedRun(t *testing.T) {
	ctx := context.Background()
	s, r, a, p, _ := skillRuntime(t)
	h := installSkillPlugin(t, s, r)
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := mustSubmit(t, a, "skill-old-version", "use frozen old method")
	pkg, err := h.Install(ctx, skillVersionPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "skill-library", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "skill-library", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := h.Store.Policy(ctx, "skill-owner-fixture")
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = "skill-owner-v11"
	policy.Cases[0].Expected = json.RawMessage(`{"indices":[0,1]}`)
	policy.Cases[1].Expected = json.RawMessage(`{"path":"QUICK.md"}`)
	if err = h.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	choose := func(id, baseline, target, policyID string) {
		t.Helper()
		if err := h.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policyID, Goal: "independent method discovery and resource entry"}); err != nil {
			t.Fatal(err)
		}
		decision, err := h.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	choose("skill-upgrade", first.Revision, graph.Revision, policy.ID)
	p.respond = skillMainPolicy
	mustDrain(t, a)
	assertVersion := func(run, firstName, body string) {
		t.Helper()
		results := skillReceipts(t, s, run)
		if !strings.HasPrefix(results["builtin__skill_list"].Text, `[{"name":"`+firstName+`"`) || !strings.Contains(results["builtin__skill_view"].Text, body) {
			t.Fatal("wrong independent version", results)
		}
	}
	assertVersion(old.RunID, "alpha", "PRIVATE_SKILL_BODY_alpha")
	current := mustSubmit(t, a, "skill-new-version", "use independently selected method")
	mustDrain(t, a)
	assertVersion(current.RunID, "zeta", "QUICK_PRIVATE_alpha")
	retained := mustSubmit(t, a, "skill-retained-version", "retain pending method")
	choose("skill-rollback", graph.Revision, first.Revision, "skill-owner-fixture")
	if err = h.Store.Retire(ctx, pkg.Digest); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	assertVersion(retained.RunID, "zeta", "QUICK_PRIVATE_alpha")
	rolled := mustSubmit(t, a, "skill-rolled-version", "use rolled back method")
	mustDrain(t, a)
	assertVersion(rolled.RunID, "alpha", "PRIVATE_SKILL_BODY_alpha")
	if err = h.Store.Quarantine(ctx, first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = h.ReadService(ctx, first.Revision, contract.ResourceContract, json.RawMessage(`{"path":""}`), nil); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("quarantined selection invoked", err)
	}

}

func TestSkillPluginPrivateResourceBinaryAndLateForget(t *testing.T) {
	for _, mode := range []string{"binary", "quarantine", "forget"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, r, a, p, _ := skillRuntime(t)
			h := installSkillPlugin(t, s, r)
			args := `{"name":"alpha"}`
			if mode == "binary" {
				args = `{"name":"alpha","path":"binary.bin"}`
			}
			call := llm.ToolCall{ID: "read", Name: "builtin__skill_view", Arguments: args}
			p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
				return llm.GenerationResult{ToolCalls: []llm.ToolCall{call}}, nil
			}
			receipt := mustSubmit(t, a, "skill-resource-"+mode, "read the approved resource")
			c, err := r.Claim(ctx, receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Release(ctx, c)
			if err = r.Initialize(ctx, c); err != nil {
				t.Fatal(err)
			}
			run, err := r.Load(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			gen, err := r.Generation(ctx, c, run)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Generate(ctx, gen.Request, nil); err != nil {
				t.Fatal(err)
			}
			if mode != "binary" {
				driver := r.skillRead
				owner := idAgent(t, s)
				r.skillRead = func(ctx context.Context, args string) tool.Result {
					result := driver(ctx, args)
					if mode == "forget" {
						if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
							t.Fatal(err)
						}
					} else {
						status, err := h.Store.Status(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if err = h.Store.Quarantine(ctx, status.Revision); err != nil {
							t.Fatal(err)
						}
					}
					return result
				}
			}
			executor := tool.Executor{Registry: a.Registry, Store: r.ToolStore(c)}
			results, err := executor.Execute(ctx, gen.Request.GenerationID, []llm.ToolCall{call})
			if mode == "forget" {
				if !errors.Is(err, agent.ErrClaimLost) || len(results) != 0 {
					t.Fatal("late file crossed Forget", results, err)
				}
				return
			}
			if err != nil || len(results) != 1 {
				t.Fatal(results, err)
			}
			if mode == "quarantine" {
				if results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" || results[0].Text != "" {
					t.Fatal("late private file crossed quarantine", results)
				}
				return
			}
			if results[0].Error != nil || len(results[0].ArtifactRefs) != 1 {
				t.Fatal("binary resource lost artifact normalization", results)
			}
			reader, err := s.Artifacts().Open(ctx, artifactOwner(t, s), results[0].ArtifactRefs[0])
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			raw, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(raw, []byte{0xff, 0, 0xfe}) {
				t.Fatal("binary original changed", raw, err)
			}
		})
	}
}

func TestSkillPluginCannotRewriteRequestedResourceOrTraverse(t *testing.T) {
	for _, path := range []string{"../private", "/etc/passwd", "", "assets/../SKILL.md"} {
		if validSkillResourcePath("", path) {
			t.Fatal("invalid resource entered native driver", path)
		}
	}
	if validSkillResourcePath("references/details.md", "SKILL.md") {
		t.Fatal("explicit resource silently substituted")
	}
	if !validSkillResourcePath("", "QUICK.md") || !validSkillResourcePath("assets/image.bin", "assets/image.bin") {
		t.Fatal("frozen relative resources denied")
	}
	if _, err := skillSelection([]skill.Summary{{Name: "a"}, {Name: "b"}}, 1, contract.SearchPlan{Indices: []int{0, 1}}); !errors.Is(err, rsi.ErrInvalid) {
		t.Fatal("output limit expanded", err)
	}
}
