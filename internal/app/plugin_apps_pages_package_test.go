package app

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func pagesVariant(t *testing.T, source *contract.PageMarkupSource, version string) {
	t.Helper()
	if version == "1.20.0" {
		re := regexp.MustCompile(`(<h2>{{.Name}}</h2>)(<p>{{.Description}}</p>)`)
		if !re.MatchString(source.Pages) {
			t.Fatal("original detail heading source missing")
		}
		source.Pages = re.ReplaceAllString(source.Pages, "$2$1")
	}
	if version == "4.11.0" {
		source.Pages = strings.Replace(source.Pages, `method="get"`, `method="post"`, 1)
	}
}
func pagesInterfacePackage(t *testing.T, version string) string {
	t.Helper()

	dir := t.TempDir()
	profile, err := filepath.Abs("../plugin/appsinterface/profile.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/profile.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.19.0"`, `Version: "`+version+`"`, 1)
	if code == string(raw) && version != "1.19.0" {
		t.Fatal("directory producer version variation missing")
	}
	replacement := filepath.Join(t.TempDir(), "profile.go")
	if err := os.WriteFile(replacement, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{profile: replacement}
	for _, name := range []string{"localization.go", "markup.go"} {
		path, err := filepath.Abs("../plugin/appsinterface/" + name)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := filepath.Abs("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/" + name + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		replacements[path] = saved
	}
	encoded, _ := json.Marshal(map[string]any{"Replace": replacements})
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/apps-interface").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-interface-pre-browser-v12/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	for _, resource := range manifest.Resources {
		if resource.Kind != "view" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("../../tests/fixtures/plugins/apps-interface-pre-browser-v12", resource.Path))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, resource.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = os.ReadFile(filepath.Join(dir, contract.MarkupFile))
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.19.0" {
		var source contract.PageMarkupSource
		if err := json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		pagesVariant(t, &source, version)
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
