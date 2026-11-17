package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Delay only the test candidate's nonempty navigation projection, inside its real
// sandboxed process. No production hook, capability or data contract is added.
func memoryInspectorDelayedProcessPackage(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	original, err := filepath.Abs("../../plugin/memoryinspector/presentation.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	needle := "func project(req contract.Request) contract.Result {"
	if strings.Count(string(raw), needle) != 1 {
		t.Fatal("projection entry changed")
	}
	override := filepath.Join(t.TempDir(), "presentation.go")
	code := strings.Replace(string(raw), needle, needle+"\n if len(req.Navigation)>0 {time.Sleep(2*time.Second)}", 1)
	if err = os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: override}})
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", overlayPath, "-o", filepath.Join(source, "plugin"), "../../../cmd/plugins/memory-inspector").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	raw, err = os.ReadFile("../../../plugins/memory-inspector/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(source, "views"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"memory.html", "memory.css", "copy.json", "memory-worker.js"} {
		raw, err = os.ReadFile(filepath.Join("../../plugin/memoryinspector/view", file))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(source, "views", file), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
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

func TestMemoryInspectorActualProcessLateForgetReplacementPrivacyAndGuardian(t *testing.T) {
	source := memoryInspectorDelayedProcessPackage(t)
	raw, err := os.ReadFile("../../rsi/builtin/memory-inspector-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []rsi.Case
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) < 1 {
		t.Fatal("independent Owner oracle missing", err)
	}
	for _, mode := range []string{"unchanged", "forget", "replacement", "privacy-epoch", "stop"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, r, _, _, _ := loopFixture(t)
			vault, _ := evidenceMemoryVault(t, s)
			owner := idAgent(t, s)
			if err := vault.Apply(ctx, evidenceFact("late-inspector-private", owner)); err != nil {
				t.Fatal(err)
			}
			before, err := r.InspectMemory(ctx, memory.InspectionQuery{}, vault)
			if err != nil || len(before.Items) != 1 {
				t.Fatal("original authorized fact missing", before, err)
			}
			h := installRuntimePackage(t, s, r, "memory-inspector", rsi.Policy{ID: "memory-inspector-process-owner", AllowedContracts: []string{contract.Presentation}, Cases: cases[:1]}, source)
			status, err := h.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := h.Store.Graph(ctx, status.Revision)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := filepath.EvalSymlinks(filepath.Join(h.Catalog.Path(graph.Graph.Bindings[0].PackageDigest), "plugin"))
			if err != nil {
				t.Fatal(err)
			}
			// Fixture store/package setup is outside the unchanged producer race alarm.
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			type outcome struct {
				page         memory.InspectionPage
				presentation *memory.InspectionPresentation
				err          error
			}
			done := make(chan outcome, 1)
			go func() {
				p, v, e := r.InspectMemoryPresented(ctx, memory.InspectionQuery{}, vault, "")
				done <- outcome{p, v, e}
			}()
			// The external process is launched inside the projection callback, after
			// authoritative page/snapshot loading. Observe its exact installed entry,
			// rather than assuming that a sleep means the read has reached the producer.
			observed := false
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				select {
				case result := <-done:
					t.Fatal("projection returned before concurrent change", result.err)
				default:
				}
				processes, e := exec.Command("ps", "-axo", "command=").Output()
				if e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(processes), entry) {
					observed = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !observed {
				t.Fatal("actual isolated producer was not observed")
			}
			switch mode {
			case "forget":
				err = vault.Forget(ctx, recall.Scope{UserID: artifact.LocalUser, AgentID: owner}, "late-inspector-private", time.Now())
			case "replacement":
				next := evidenceFact("late-inspector-new", owner)
				next.Supersedes = "late-inspector-private"
				err = vault.Apply(ctx, next)
			case "privacy-epoch":
				err = s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) })
			case "stop":
				err = h.Store.Stop(ctx, true)
			}
			if err != nil {
				t.Fatal("concurrent control was blocked", err)
			}
			select {
			case result := <-done:
				if mode == "unchanged" {
					if result.err != nil || len(result.page.Items) != 1 || result.page.Items[0].Content != "Current exact evidence" || result.presentation == nil || result.presentation.ReaderRevision != status.Revision {
						t.Fatal("unmodified producer failed valid original view", result.page, result.err)
					}
				} else if result.err == nil || len(result.page.Items) != 0 || result.presentation != nil {
					t.Fatal("late producer released stale private view", result.page, result.err)
				}
			case <-ctx.Done():
				t.Fatal("producer did not terminate", ctx.Err())
			}
		})
	}
}
