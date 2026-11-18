package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// A real independently compiled fixture version changes owned Chinese copy and
// spacing and compact-layout decision. Expectations derive from the pre-candidate capture, not its output.
func memoryInspectorIteration(t *testing.T, original contract.Result) (string, contract.Result) {
	t.Helper()
	next := original
	var copy []contract.Translation
	if err := json.Unmarshal([]byte(original.Localization.Source), &copy); err != nil {
		t.Fatal(err)
	}
	changed := false
	for i := range copy {
		if copy[i].English == "Memory details" {
			copy[i].Chinese = "独立版本详情"
			changed = true
		}
	}
	if !changed {
		t.Fatal("original copy target missing")
	}
	copy = append(copy, contract.Translation{English: "Independent memory details", Chinese: "独立版本详情"})
	raw, err := json.MarshalIndent(copy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	next.Localization.Source = string(raw)
	next.Localization.Digest = rsiplugin.FileDigest(raw)
	next.Stylesheet.Source = strings.Replace(original.Stylesheet.Source, ".memory-content-section{margin-bottom:24px}", ".memory-content-section{margin-bottom:32px}", 1)
	if next.Stylesheet.Source == original.Stylesheet.Source {
		t.Fatal("spacing variant unchanged")
	}
	next.Stylesheet.Digest = rsiplugin.FileDigest([]byte(next.Stylesheet.Source))
	next.Program.Source = strings.Replace(original.Program.Source, "compact_width:760", "compact_width:800", 1)
	if next.Program.Source == original.Program.Source {
		t.Fatal("client decision variant unchanged")
	}
	next.Program.Source = strings.Replace(next.Program.Source, "details:'Memory details'", "details:'Independent memory details'", 1)
	if !strings.Contains(next.Program.Source, "details:'Independent memory details'") {
		t.Fatal("independent English label unchanged")
	}
	next.Program.Digest = rsiplugin.FileDigest([]byte(next.Program.Source))
	dir := t.TempDir()
	replacements := map[string]string{}
	for _, file := range []string{"plugin.go", "presentation.go"} {
		path, err := filepath.Abs(filepath.Join("../plugin/memoryinspector", file))
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join("../../tests/fixtures/plugins/memory-inspector-original-v1", file+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		code := string(source)
		if file == "plugin.go" {
			needle := `Version: "1.0.0"`
			if strings.Count(code, needle) != 1 {
				t.Fatal("original version declaration missing")
			}
			code = strings.Replace(code, needle, `Version: "1.1.0"`, 1)
		} else {
			for _, part := range []struct{ directive, name, value string }{{"view/memory.html", "memoryViewSource", next.View.Source}, {"view/memory.css", "memoryStylesheetSource", next.Stylesheet.Source}, {"view/copy.json", "memoryLocalizationSource", next.Localization.Source}, {"view/memory-worker.js", "memoryWorkerSource", next.Program.Source}} {
				needle := "//go:embed " + part.directive + "\nvar " + part.name + " string"
				if strings.Count(code, needle) != 1 {
					t.Fatal("original embedded source missing", part.name)
				}
				code = strings.Replace(code, needle, "var "+part.name+" = "+strconv.Quote(part.value), 1)
			}
		}
		override := filepath.Join(t.TempDir(), file)
		if err = os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = override
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", overlayPath, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/memory-inspector").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	manifestBytes, err := os.ReadFile("../../plugins/memory-inspector/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	if err = os.MkdirAll(filepath.Join(dir, "views"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct{ name, value string }{{"memory.html", next.View.Source}, {"memory.css", next.Stylesheet.Source}, {"copy.json", next.Localization.Source}, {"memory-worker.js", next.Program.Source}} {
		if err = os.WriteFile(filepath.Join(dir, "views", part.name), []byte(part.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), manifestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, next
}
