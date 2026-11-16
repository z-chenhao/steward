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

func settingsViewVariant(source *contract.SettingsMarkupSource, version string) {
	if version == "1.16.0" {
		source.Settings.English.SaveLabel = "Save connection service"
		source.Settings.Chinese.SaveLabel = "保存连接服务"
	}
	if version == "4.7.0" {
		source.Settings.Program = strings.Replace(source.Settings.Program, `name="key_ref"`, `name="api_key"`, 1)
	}
}

func settingsViewInterfacePackage(t *testing.T, version string) string {
	t.Helper()

	dir := t.TempDir()
	profile, err := filepath.Abs("../plugin/appsinterface/profile.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-directory-controls-v10/profile.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.15.0"`, `Version: "`+version+`"`, 1)
	if code == string(raw) && version != "1.15.0" {
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
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-directory-controls-v10/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	copyInterfaceViews(t, dir, manifest)
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-directory-controls-v10/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.15.0" {
		var source contract.SettingsMarkupSource
		if err := json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		settingsViewVariant(&source, version)
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
