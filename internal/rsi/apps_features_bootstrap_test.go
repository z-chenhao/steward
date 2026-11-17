package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsBootstrapIndependentOwnerResourcesAndColdSelection(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	dirs := builtin.Directories{DOCX: docxPackageSource(t),
		AppsDirectory: bootstrapPackage(t, "apps-directory", nil),
		AppsAccounts:  bootstrapPackage(t, "apps-accounts", nil),
		AppsLogos:     bootstrapPackage(t, "apps-logos", nil),
		AppsInterface: bootstrapPackage(t, "apps-interface", nil)}
	bad := bootstrapPackage(t, "apps-interface", nil)
	// This valid alternative program is sealed honestly. The independent owner
	// must reject its changed content, rather than learn expected output from it.
	program := []byte(`{"program":["begin-entry","name","icon","pending","end-entry"]}`)
	if err := os.WriteFile(filepath.Join(bad, "views/sidebar.json"), program, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(bad, "steward.plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(bad, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"good", "changed-resource"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			choice := dirs
			if mode == "changed-resource" {
				choice.AppsInterface = bad
			}
			err := builtin.EnsureDefault(ctx, f.host, choice)
			status, e := f.host.Store.Status(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "changed-resource" {
				if !errors.Is(err, rsi.ErrVerification) || status.Epoch != 0 || status.Revision != "" {
					t.Fatal("candidate resource supplied its own acceptance or restored native", status, err)
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
			if err != nil || len(policy.Cases) != 110 || len(policy.AllowedContracts) != 21 {
				t.Fatal("missing independent service examples", len(policy.Cases), policy.AllowedContracts, err)
			}
			if err := builtin.EnsureDefault(ctx, f.host, builtin.Directories{DOCX: "/missing"}); err != nil {
				t.Fatal("cold startup repaired existing selection", err)
			}
			if again, e := f.host.Store.Status(ctx); e != nil || again != status {
				t.Fatal("cold startup changed original graph", again, e)
			}
			if _, err := f.host.Store.Deactivate(ctx, "owner-disable-apps", status.Revision); err != nil {
				t.Fatal(err)
			}
			disabled, err := f.host.Store.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := builtin.EnsureDefault(ctx, f.host, dirs); err != nil {
				t.Fatal(err)
			}
			if after, e := f.host.Store.Status(ctx); e != nil || after != disabled {
				t.Fatal("cold startup reenabled owner-disabled Apps", after, e)
			}
		})
	}
}
