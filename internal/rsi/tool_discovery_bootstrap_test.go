package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The variant runs the real producer/input reader/ranker, then corrupts its
// declared-valid result. Owner expectations remain independent literals.
func corruptDiscoveryBootstrapPackage(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs("../plugin/tooldiscovery/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(original), `Version: "1.0.0"`, `Version: "1.1.0"`, 1)
	needle := `return rsiplugin.JSONResult(ctx, Rank(f, req.Limit))`
	replacement := `plan := Rank(f, req.Limit)
 if len(plan.Entries)>0 {plan.Entries[0].Index=req.Count}
 return rsiplugin.JSONResult(ctx, plan)`
	if !strings.Contains(code, needle) || code == string(original) {
		t.Fatal("bad producer overlay did not match")
	}
	directory := t.TempDir()
	override := filepath.Join(directory, "override.go")
	if err := os.WriteFile(override, []byte(strings.Replace(code, needle, replacement, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{source: override}})
	overlay := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bundle := t.TempDir()
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(bundle, "plugin"), "../../cmd/plugins/tool-discovery").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../plugins/tool-discovery/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = "1.1.0"
	m, err = rsi.Seal(bundle, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(filepath.Join(bundle, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestToolDiscoveryBootstrapRejectsCorruptPlanAndRetainsOwnerDisable(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	docx := docxPackageSource(t)
	good := bootstrapPackage(t, "tool-discovery", nil)
	bad := corruptDiscoveryBootstrapPackage(t)
	for _, mode := range []string{"good", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			dirs := builtin.Directories{DOCX: docx, Discovery: good}
			if mode == "corrupt" {
				dirs.Discovery = bad
			}
			err := builtin.EnsureDefault(ctx, f.host, dirs)
			status, readErr := f.host.Store.Status(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "corrupt" {
				if !errors.Is(err, rsi.ErrVerification) || status.Epoch != 0 || status.Revision != "" {
					t.Fatal("corrupt producer selected or silently fell back", status, err)
				}
				return
			}
			if err != nil || status.Epoch != 1 || status.Revision == "" {
				t.Fatal(status, err)
			}
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing/current"}); err != nil {
				t.Fatal(err)
			}
			if again, _ := f.host.Store.Status(ctx); again != status {
				t.Fatal("cold startup replaced selected ranker", again, status)
			}
			if _, err := f.host.Store.Deactivate(ctx, "owner-disable-discovery", status.Revision); err != nil {
				t.Fatal(err)
			}
			disabled, _ := f.host.Store.Status(ctx)
			if err := builtin.EnsureDefault(ctx, f.host, dirs); err != nil {
				t.Fatal(err)
			}
			if again, _ := f.host.Store.Status(ctx); again != disabled {
				t.Fatal("startup reenabled discovery", again, disabled)
			}
		})
	}
}
