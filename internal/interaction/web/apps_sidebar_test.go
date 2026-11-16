package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"testing"
)

func TestAppsSidebarViewOriginal34BodiesAndActualResource(t *testing.T) {
	type scene struct {
		Language           string
		Pending            bool
		IconMode, TextMode int
		Empty              bool
	}
	type result struct {
		SHA256 string
		Bytes  int
	}
	type example struct {
		Scene    scene
		Expected result
	}
	var cases []example
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-sidebar-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 34 {
		t.Fatal(len(cases))
	}
	var resource contract.SidebarView
	if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.SidebarViewInterface, json.RawMessage(`{}`)), &resource); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile("../../../plugins/apps-interface/" + contract.SidebarViewFile)
	if err != nil || resource.Digest != rsiplugin.FileDigest(sealed) || resource.Resource != contract.SidebarViewResource || !validAppSidebarProgram(resource.Program) {
		t.Fatal("actual sidebar resource identity", resource, err)
	}
	policies := actualLocalizedAppPolicies(t)
	index := 0
	capture := func(f scene) {
		t.Helper()
		view := &appsView{}
		if !f.Empty {
			name, url := "Original app", "/settings/apps?app=sample_app"
			switch f.TextMode {
			case 1:
				name = `Original <App> & 工作`
			case 2:
				name = ""
			case 3:
				name = `Name "quoted"`
				url = "javascript:alert(1)"
			}
			card := appCard{Toolkit: composio.Toolkit{Slug: "sample_app", Name: name}, Pending: f.Pending, URL: url, Initial: "O"}
			if f.IconMode != 0 {
				card.Icon = "/apps/logo/sample_app?revision=original&icon=1"
			}
			view.Managed = []appCard{card}
		}
		for _, mode := range []string{"native", "managed"} {
			if mode == "managed" {
				p, err := ManagedAppSidebarView(policies[f.Language], resource)
				if err != nil {
					t.Fatal(err)
				}
				view.Interface = &p
			}
			var out bytes.Buffer
			if err := requestLayout(languageRequest("GET", "/settings/apps", f.Language, "")).ExecuteTemplate(&out, "app-sidebar", page{Apps: view}); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(out.Bytes())
			want := cases[index]
			if want.Scene != f || want.Expected != (result{hex.EncodeToString(sum[:]), out.Len()}) {
				t.Fatalf("original sidebar changed case=%d mode=%s got=%s", index, mode, out.String())
			}
		}
		index++
	}
	for _, locale := range []string{"en", "zh-CN"} {
		for _, pending := range []bool{false, true} {
			for icon := 0; icon < 2; icon++ {
				for text := 0; text < 4; text++ {
					capture(scene{Language: locale, Pending: pending, IconMode: icon, TextMode: text})
				}
			}
		}
		capture(scene{Language: locale, Empty: true})
	}
}

func TestAppsSidebarViewRejectsMissingOrDuplicateAtoms(t *testing.T) {
	valid := appSidebarProgramLegacyV1()
	if !validAppSidebarProgram(valid) {
		t.Fatal("native entry rejected")
	}
	variants := [][]string{nil, valid[:4], {"begin-entry", "name", "icon", "pending", "end-entry", "pending"}, {"icon", "begin-entry", "name", "pending", "end-entry"}}
	for i := range valid {
		p := append([]string{}, valid...)
		p[i] = valid[(i+1)%len(valid)]
		variants = append(variants, p)
		p = append([]string{}, valid...)
		p[i] = "unknown"
		variants = append(variants, p)
	}
	for _, p := range variants {
		if validAppSidebarProgram(p) {
			t.Fatal("incomplete/unknown entry accepted", p)
		}
	}
	if !validAppSidebarProgram([]string{"begin-entry", "name", "pending", "icon", "end-entry"}) {
		t.Fatal("independent valid order rejected")
	}
}
