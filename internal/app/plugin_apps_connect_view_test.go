package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func connectViewVariant(source *contract.ConnectMarkupSource, version string) {
	if version == "1.14.0" {
		source.Connect.English.AccountLabel = "Account name"
		source.Connect.Chinese.AccountLabel = "账户名称"
	} else if version == "4.5.0" {
		source.Connect.Program = strings.Replace(source.Connect.Program, `name="alias"`, `name="key_ref"`, 1)
	}
}

func connectViewInterfacePackage(t *testing.T, version string) string {
	t.Helper()

	dir := t.TempDir()
	profile, err := filepath.Abs("../plugin/appsinterface/profile.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-settings-v9/profile.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.13.0"`, `Version: "`+version+`"`, 1)
	if code == string(raw) && version != "1.13.0" {
		t.Fatal("connect producer version variation missing")
	}
	replacement := filepath.Join(t.TempDir(), "profile.go")
	if err := os.WriteFile(replacement, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	// This old profile declares /2. Its original compiler is captured source,
	// independent of the current /3 producer and its removed old entry point.
	localization, err := filepath.Abs("../plugin/appsinterface/localization.go")
	if err != nil {
		t.Fatal(err)
	}
	originalLocalization, err := filepath.Abs("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/localization.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{"Replace": map[string]string{profile: replacement, localization: originalLocalization}})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-interface").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-settings-v9/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	copyInterfaceViews(t, dir, manifest)
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-settings-v9/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.13.0" {
		var source contract.ConnectMarkupSource
		if err := json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		connectViewVariant(&source, version)
		raw, _ = json.MarshalIndent(source, "", "  ")
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, contract.MarkupFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAppsConnectViewActualVersionsOriginalEffectAndColdReferences(t *testing.T) {
	testAppsAccountPresentationLifecycle(t, "connect")
}
