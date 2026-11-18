package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/z-chenhao/assistant/internal/rsi/builtin"
)

func TestShippedPluginDistributionRejectsPartialNativeFallback(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "steward")
	if d, err := builtin.ShippedDirectories(executable); err != nil || d.DOCX != "" {
		t.Fatal("explicit unbundled embedder boundary changed", d, err)
	}
	plugins := filepath.Join(root, "plugins")
	names := readDefaultOwnerInventory(t).Instances
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(plugins, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plugins, name, "steward.plugin.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := builtin.ShippedDirectories(executable); err != nil {
		t.Fatal("complete distribution not found", err)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(plugins, name, "steward.plugin.json")
			if err := os.Rename(manifest, manifest+".missing"); err != nil {
				t.Fatal(err)
			}
			defer os.Rename(manifest+".missing", manifest)
			if d, err := builtin.ShippedDirectories(executable); err == nil || d.DOCX != "" {
				t.Fatal("damaged distribution admitted a partial native composition", d, err)
			}
		})
	}
}
