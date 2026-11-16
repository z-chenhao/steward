package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
)

func originalGraphicSource(t *testing.T) contract.GraphicMarkupSource {
	t.Helper()
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-graphics-source-native-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.GraphicMarkupSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestAppsGraphicsOriginal28BodiesAndActualOwningResource(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-graphics-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Scene    struct{ Language, Name, Initial, Icon string }
		Expected struct {
			SHA256 string
			Bytes  int
		}
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) != 28 {
		t.Fatal(err, len(cases))
	}
	policies := actualLocalizedAppPolicies(t)
	for _, c := range cases {
		t.Run(c.Scene.Language+"/"+c.Scene.Name+"/"+c.Scene.Initial+"/"+c.Scene.Icon, func(t *testing.T) {
			templates := newLayout(c.Scene.Language)
			card := appCard{Initial: c.Scene.Initial, Icon: c.Scene.Icon}
			for _, managed := range []bool{false, true} {
				v := &appsView{}
				if managed {
					p := policies[c.Scene.Language]
					v.Interface = &p
				}
				body, err := appPrimitive(templates, c.Scene.Name, card, v)
				if err != nil {
					t.Fatal(err)
				}
				hash := sha256.Sum256([]byte(body))
				if len(body) != c.Expected.Bytes || hex.EncodeToString(hash[:]) != c.Expected.SHA256 {
					t.Fatalf("managed=%v changed independent original body: %s", managed, body)
				}
			}
		})
	}
}

func TestAppsGraphicsPassiveVariantAndSharedModelsIndependence(t *testing.T) {
	source := originalGraphicSource(t)
	source.Graphics = strings.Replace(source.Graphics, `class="app-logo"`, `class="app-logo" data-plugin-icon="v1.8"`, 1)
	source.Graphics = strings.Replace(source.Graphics, `cx="5"`, `cx="4"`, 1)
	compiled, err := compileAppGraphics(source.Graphics)
	if err != nil {
		t.Fatal(err)
	}
	more, err := renderAppGraphic(compiled, "app-more", appGraphicData{})
	if err != nil || !strings.Contains(string(more), `cx="4"`) {
		t.Fatal(more, err)
	}
	icon, err := renderAppGraphic(compiled, "app-icon", appGraphicData{Initial: "G"})
	if err != nil || !strings.Contains(string(icon), `data-plugin-icon="v1.8"`) {
		t.Fatal(icon, err)
	}
	native := newLayout("en")
	var out bytes.Buffer
	if err := native.ExecuteTemplate(&out, "icon-more-v1", nil); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(out.Bytes())
	if hex.EncodeToString(sum[:]) != "dca09f053c6b9b8096831d9024c9ab3f33c4efba0c63bb08291852bfad73cfdc" {
		t.Fatal("Models shared glyph changed", out.String())
	}
	p := UnavailableAppGraphicMarkup(AppInterfacePolicy{})
	if _, err := appPrimitive(native, "app-more", appCard{}, &appsView{Interface: &p}); err == nil {
		t.Fatal("failed Apps graphic restored native")
	}
	out.Reset()
	if err := native.ExecuteTemplate(&out, "icon-more-v1", nil); err != nil {
		t.Fatal("Apps failure affected Models", err)
	}
}

func TestAppsGraphicsRejectsActiveSourcesReflectionAndChangedBindings(t *testing.T) {
	original := originalGraphicSource(t).Graphics
	cases := []struct{ name, old, new string }{
		{"state", `{{.Initial}}`, `{{.State.KeyRef}}`},
		{"method", `{{.Initial}}`, `{{.Initial.String}}`},
		{"function", `{{.Initial}}`, `{{printf "%s" .Initial}}`},
		{"range", `{{.Initial}}`, `{{range .Initial}}{{.}}{{end}}`},
		{"missing-initial", `{{.Initial}}`, `G`},
		{"wrong-logo", `src="{{.IconURL}}"`, `src="/apps/logo/other"`},
		{"missing-image", `{{if .HasIcon}}`, `{{if .HasIcon}}<!--`},
		{"event", `<svg `, `<svg onload="alert(1)" `},
		{"script", `<circle `, `<script>alert(1)</script><circle `},
		{"foreign-object", `<circle `, `<foreignObject/><circle `},
		{"external-image", `<circle `, `<image href="https://example.invalid/a"/><circle `},
		{"paint-url", `fill="currentColor"`, `fill="url(https://example.invalid/a)"`},
		{"style", `<circle `, `<circle style="display:none" `},
		{"dom-hook", `<circle `, `<circle data-app-action="connect" `},
		{"id", `<circle `, `<circle id="composio-key-ref" `},
		{"namespace", `<circle `, `<circle xlink:href="/apps" `},
		{"nan-coordinate", `cx="5"`, `cx="NaN"`},
		{"infinite-coordinate", `cx="5"`, `cx="+Inf"`},
		{"nan-stroke", `stroke-width="1.6"`, `stroke-width="NaN"`},
		{"infinite-stroke", `stroke-width="1.6"`, `stroke-width="+Inf"`},
		{"outside-frame", `cx="5"`, `cx="25"`},
		{"zero-radius", `r="1.5"`, `r="0"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(original, c.old) {
				t.Fatal("negative marker absent", c.old)
			}
			source := strings.Replace(original, c.old, c.new, 1)
			if _, err := compileAppGraphics(source); err == nil {
				t.Fatal("unsafe graphic accepted")
			}
		})
	}
}
