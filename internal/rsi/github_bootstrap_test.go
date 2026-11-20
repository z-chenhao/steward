package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// An honestly sealed, schema-valid alternative must not teach the owner its
// own answer. Only the candidate's default changes; owner cases stay literals.
func changedGitHubBootstrapPackage(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs("../plugin/githubfollowup/plugin.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../plugins/github-pr-followup/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &m); err != nil {
		t.Fatal(err)
	}
	const candidateVersion = "99.0.0"
	raw, err = os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), "Version: "+strconv.Quote(m.Version), "Version: "+strconv.Quote(candidateVersion), 1)
	needle := "plan.IntervalSeconds = 60"
	if !strings.Contains(code, needle) || code == string(raw) {
		t.Fatal("candidate overlay did not match")
	}
	tmp := t.TempDir()
	override := filepath.Join(tmp, "plugin.go")
	if err := os.WriteFile(override, []byte(strings.Replace(code, needle, "plan.IntervalSeconds = 30", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(struct{ Replace map[string]string }{map[string]string{source: override}})
	overlay := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bundle := t.TempDir()
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(bundle, "plugin"), "../../cmd/plugins/github-pr-followup").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	m.Version = candidateVersion
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

func TestGitHubBootstrapIndependentOracleAndOriginalSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	dirs := builtin.Directories{DOCX: docxPackageSource(t), GitHub: bootstrapPackage(t, "github-pr-followup", nil)}
	bad := changedGitHubBootstrapPackage(t)
	for _, mode := range []string{"good", "changed-default", "prior-selection"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode == "prior-selection" {
				if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: dirs.DOCX}); err != nil {
					t.Fatal(err)
				}
				before, err := f.host.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := builtin.EnsureDefault(ctx, f.host, dirs); err != nil {
					t.Fatal(err)
				}
				if after, err := f.host.Store.Status(ctx); err != nil || after != before {
					t.Fatal("new distribution replaced original owner choice", after, err)
				}
				return
			}
			choice := dirs
			if mode == "changed-default" {
				choice.GitHub = bad
			}
			err := builtin.EnsureDefault(ctx, f.host, choice)
			status, readErr := f.host.Store.Status(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "changed-default" {
				if !errors.Is(err, rsi.ErrVerification) || status.Epoch != 0 || status.Revision != "" {
					t.Fatal("candidate supplied its own oracle or restored native", status, err)
				}
				return
			}
			if err != nil || status.Epoch != 1 || status.Revision == "" {
				t.Fatal(status, err)
			}
			var policyID string
			if err := f.db.QueryRow(`SELECT policy_id FROM rsi_candidate WHERE target=?`, status.Revision).Scan(&policyID); err != nil {
				t.Fatal(err)
			}
			policy, err := f.host.Store.Policy(ctx, policyID)
			if err != nil || len(policy.Cases) != 12 || len(policy.AllowedContracts) != 2 {
				t.Fatal("independent GitHub cases missing", len(policy.Cases), err)
			}
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing"}); err != nil {
				t.Fatal(err)
			}
			if after, err := f.host.Store.Status(ctx); err != nil || after != status {
				t.Fatal("cold bootstrap repaired original graph", after, err)
			}
			if _, err := f.host.Store.Deactivate(ctx, "owner-disable-github", status.Revision); err != nil {
				t.Fatal(err)
			}
			disabled, err := f.host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := builtin.EnsureDefault(ctx, f.host, dirs); err != nil {
				t.Fatal(err)
			}
			if after, err := f.host.Store.Status(ctx); err != nil || after != disabled {
				t.Fatal("cold startup reenabled owner-disabled GitHub", after, err)
			}
		})
	}
}
