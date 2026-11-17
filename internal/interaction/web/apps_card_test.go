package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
)

func TestAppsCardViewOriginal512BodiesAndActualSealedResource(t *testing.T) {
	type scene struct {
		Language                  string
		Flags, TextMode, IconMode int
	}
	type expected struct {
		SHA256 string
		Bytes  int
	}
	type example struct {
		Scene    scene
		Expected expected
	}
	var cases []example
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-card-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 512 {
		t.Fatal(len(cases))
	}
	var resource contract.CardView
	if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.CardViewInterface, json.RawMessage(`{}`)), &resource); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile("../../../plugins/apps-interface/" + contract.CardViewFile)
	if err != nil || resource.Digest != rsiplugin.FileDigest(sealed) || resource.Resource != contract.CardViewResource || !validAppCardProgram(resource.Program) {
		t.Fatal("actual resource identity/program", resource, err)
	}
	policies := actualLocalizedAppPolicies(t)
	index := 0
	for _, language := range []string{"en", "zh-CN"} {
		for flags := 0; flags < 32; flags++ {
			for textMode := 0; textMode < 4; textMode++ {
				for iconMode := 0; iconMode < 2; iconMode++ {
					name, description, url := "Original app", "Original description", "/settings/apps?app=sample_app"
					switch textMode {
					case 1:
						name = `Original <App> & 工作`
						description = `用途 & <script>private script</script>`
					case 2:
						name = ""
						description = ""
					case 3:
						name = `Name "quoted"`
						description = "Workspace"
						url = "javascript:alert(1)"
					}
					card := appCard{Toolkit: composio.Toolkit{Slug: "sample_app", Name: name, NoAuth: flags&2 != 0}, Connected: flags&4 != 0, PublicActive: flags&8 != 0, Managed: flags&16 != 0, Description: description, URL: url, Initial: "O"}
					if iconMode != 0 {
						card.Icon = "/apps/logo/sample_app?revision=original&icon=1"
					}
					view := &appsView{View: connectedapps.View{Suggested: flags&1 != 0}, Groups: []appGroup{{Title: "Original group", Items: []appCard{card}}}}
					for _, mode := range []string{"native", "managed"} {
						if mode == "managed" {
							p, err := ManagedAppCardView(policies[language], resource)
							if err != nil {
								t.Fatal(err)
							}
							view.Interface = &p
						}
						var out bytes.Buffer
						if err := requestLayout(languageRequest("GET", "/settings/apps", language, "")).ExecuteTemplate(&out, "app-directory-groups", page{Apps: view}); err != nil {
							t.Fatal(err)
						}
						raw := out.String()
						begin := strings.Index(raw, "<article ")
						end := strings.Index(raw, "</article>")
						if begin < 0 || end < begin || strings.Count(raw, "<article ") != 1 {
							t.Fatal("original native card unavailable", raw)
						}
						body := raw[begin : end+len("</article>")]
						body = regexp.MustCompile(`<input type="hidden" name="interface_revision" value="[^"]*">`).ReplaceAllString(body, "")
						sum := sha256.Sum256([]byte(body))
						want := cases[index]
						if want.Scene != (scene{language, flags, textMode, iconMode}) || want.Expected != (expected{hex.EncodeToString(sum[:]), len(body)}) {
							t.Fatalf("original card changed case=%d mode=%s got=%s", index, mode, body)
						}
					}
					index++
				}
			}
		}
	}
}

func TestAppsCardViewRejectsInvalidScopesAndMissingControls(t *testing.T) {
	native := appCardProgramLegacyV1()
	if !validAppCardProgram(native) {
		t.Fatal("original sequence rejected")
	}
	variants := [][]string{nil, append(append([]string{}, native...), "icon")}
	for i := range native {
		p := append([]string{}, native...)
		p[i] = "unknown"
		variants = append(variants, p)
		p = append([]string{}, native...)
		p[i] = native[(i+1)%len(native)]
		variants = append(variants, p)
	}
	badScope := append([]string{}, native...)
	badScope[2], badScope[8] = badScope[8], badScope[2]
	variants = append(variants, badScope)
	for _, p := range variants {
		if validAppCardProgram(p) {
			t.Fatal("incomplete/unsafe program accepted", p)
		}
	}
	valid := []string{"begin-card", "connect", "manage", "begin-summary", "begin-copy", "description", "name", "end-copy", "icon", "end-summary", "end-card"}
	if !validAppCardProgram(valid) {
		t.Fatal("independent valid component order rejected")
	}
}
