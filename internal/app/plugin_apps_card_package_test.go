package app

import (
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"path/filepath"
	"testing"
)

func copyInterfaceViews(t *testing.T, directory string, m rsiplugin.Manifest) {
	t.Helper()
	for _, resource := range m.Resources {
		if resource.Kind != "view" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../plugins/apps-interface", resource.Path))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(directory, resource.Path)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
