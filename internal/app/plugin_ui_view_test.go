package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestUIResourceReadRequiresDeclaredRoleAndOriginalBytes(t *testing.T) {
	catalog := rsi.Catalog{Root: t.TempDir()}
	digest := "sha256:" + strings.Repeat("a", 64)
	folder := catalog.Path(digest)
	if err := os.MkdirAll(filepath.Join(folder, "view"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("body{color:var(--ogui-text)}")
	path := filepath.Join(folder, "view/style.css")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	file := rsiplugin.File{Path: "view/style.css", Digest: rsiplugin.FileDigest(raw)}
	pkg := rsi.Package{Digest: digest, Manifest: rsiplugin.Manifest{Files: []rsiplugin.File{file}, Resources: []rsiplugin.Resource{{Kind: "view", Name: contract.StylesResource, Path: file.Path}}}}
	resource := contract.ViewResource{Name: contract.StylesResource, Digest: file.Digest}
	got, err := readUIAsset(catalog, pkg, resource, contract.StylesResource)
	if err != nil || string(got) != string(raw) {
		t.Fatal(string(got), err)
	}
	wrong := resource
	wrong.Name = contract.RuntimeResource
	if _, err = readUIAsset(catalog, pkg, wrong, contract.StylesResource); err == nil {
		t.Fatal("wrong role accepted")
	}
	wrong = resource
	wrong.Digest = "sha256:" + strings.Repeat("b", 64)
	if _, err = readUIAsset(catalog, pkg, wrong, contract.StylesResource); err == nil {
		t.Fatal("candidate digest replaced sealed file digest")
	}
	duplicate := pkg
	duplicate.Manifest.Resources = append(append([]rsiplugin.Resource{}, pkg.Manifest.Resources...), pkg.Manifest.Resources[0])
	if _, err = readUIAsset(catalog, duplicate, resource, contract.StylesResource); err == nil {
		t.Fatal("ambiguous role accepted")
	}
	if err = os.WriteFile(path, []byte("body{display:none}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readUIAsset(catalog, pkg, resource, contract.StylesResource); err == nil {
		t.Fatal("changed file bytes accepted")
	}
}
