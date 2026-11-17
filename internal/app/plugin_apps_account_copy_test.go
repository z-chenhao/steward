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

func accountCopyInterfacePackage(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	replacements := map[string]string{}
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
	replacements[localization] = originalLocalization
	for _, name := range []string{"profile.go", "account_copy.go"} {
		path, err := filepath.Abs("../plugin/appsinterface/" + name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if name == "profile.go" {
			frozen, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-connect-v8/profile.go.txt")
			if err != nil {
				t.Fatal(err)
			}
			text = strings.Replace(string(frozen), `Version: "1.11.0"`, `Version: "`+version+`"`, 1)
		} else if version == "1.12.0" {
			text = strings.Replace(text, `localizedText(locale, "Disconnect")`, `accountCopyVariantDisconnect(locale)`, 1)
			text += "\nfunc accountCopyVariantDisconnect(locale string) string {if locale == \"zh-CN\" {return \"关闭账户\"}; return \"Close account\"}\n"
		}
		if name == "account_copy.go" && version != "1.12.0" {
			continue
		}
		if text == string(raw) {
			t.Fatal("independent copy fixture did not change", name)
		}
		replacement := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(replacement, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = replacement
	}
	encoded, _ := json.Marshal(map[string]any{"Replace": replacements})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-interface").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-connect-v8/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	copyInterfaceViews(t, dir, manifest)
	original, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-connect-v8/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, contract.MarkupFile), original, 0600); err != nil {
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

func accountCopyOwnerFixture(t *testing.T, locale, version string) contract.AccountCopy {
	t.Helper()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-account-copy-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var copies map[string]contract.AccountCopy
	if err := json.Unmarshal(raw, &copies); err != nil {
		t.Fatal(err)
	}
	c := copies[locale]
	if version == "1.12.0" {
		c.Disconnect = "Close account"
		if locale == "zh-CN" {
			c.Disconnect = "关闭账户"
		}
	}
	return c
}
