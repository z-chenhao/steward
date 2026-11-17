package computer

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

func browserTestUnits(units ...uint16) []byte {
	b := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}
func browserTestText(s string) []byte { return browserTestUnits(utf16.Encode([]rune(s))...) }
func TestComputerBrowserProjectionOriginalUTF16AndImmutableReferences(t *testing.T) {
	value := browserTestUnits('a', 0xd800, 'b', 0xd83e, 0xdded, '\n')
	s := BrowserSource{Version: 1, Body: browserTestText("原文🧭"), Rows: []BrowserSourceRow{{Role: browserTestText("INPUT"), Aria: browserTestText("<名>\"\n"), Inner: browserTestText("alternative"), Value: &value}, {Role: browserTestText("BUTTON"), Inner: browserTestText("提交")}}}
	f, err := s.Facts()
	if err != nil || f.BodyUnits != 4 || f.Rows[0].ValueUnits == nil || *f.Rows[0].ValueUnits != 6 {
		t.Fatal(f, err)
	}
	n := 6
	p := BrowserProjection{BodyUnits: 4, Rows: []BrowserRowView{{Index: 0, Label: "aria", LabelUnits: 5, ValueUnits: &n}, {Index: 1, Label: "inner", LabelUnits: 2}}}
	got, err := s.Render(p)
	want := "原文🧭\n[0] INPUT \"<名>\\\"\\n\" = \"a\\ud800b🧭\\n\"\n[1] BUTTON \"提交\""
	if err != nil || got != want {
		t.Fatalf("original JSON.stringify code units changed\ngot=%q\nwant=%q error=%v", got, want, err)
	}
	cut := 4
	p.Rows[0].ValueUnits = &cut
	p.Rows[0].Label = "inner"
	p.Rows[0].LabelUnits = 3
	p.Rows[0], p.Rows[1] = p.Rows[1], p.Rows[0]
	p.BodyUnits = 3
	got, err = s.Render(p)
	if err != nil || !strings.HasPrefix(got, "原文�\n[1] BUTTON") || !strings.Contains(got, `[0] INPUT "alt" = "a\ud800b\ud83e"`) {
		t.Fatal("bounded independent presentation lost exact indices/source", got, err)
	}
	if string(value) != string(browserTestUnits('a', 0xd800, 'b', 0xd83e, 0xdded, '\n')) {
		t.Fatal("source mutated")
	}
}
func TestComputerBrowserProjectionRejectsForgedOrIncompleteProof(t *testing.T) {
	v := browserTestText("secret")
	source := BrowserSource{Version: 1, Body: browserTestText("body"), Rows: []BrowserSourceRow{{Role: browserTestText("INPUT"), Aria: browserTestText("name"), Value: &v}, {Role: browserTestText("BUTTON")}}}
	n := 6
	base := func() BrowserProjection {
		return BrowserProjection{BodyUnits: 4, Rows: []BrowserRowView{{Index: 0, Label: "aria", LabelUnits: 4, ValueUnits: &n}, {Index: 1, Label: "role", LabelUnits: 6}}}
	}
	for name, mutate := range map[string]func(*BrowserProjection){
		"missing-row":               func(p *BrowserProjection) { p.Rows = p.Rows[:1] },
		"duplicate-index":           func(p *BrowserProjection) { p.Rows[1].Index = 0 },
		"invented-index":            func(p *BrowserProjection) { p.Rows[0].Index = 3 },
		"invented-text":             func(p *BrowserProjection) { p.Rows[0].Label = "private-injected-value" },
		"unproved-body":             func(p *BrowserProjection) { p.BodyUnits = 5 },
		"unproved-label":            func(p *BrowserProjection) { p.Rows[0].LabelUnits = 5 },
		"unproved-value":            func(p *BrowserProjection) { x := 7; p.Rows[0].ValueUnits = &x },
		"dropped-value-presence":    func(p *BrowserProjection) { p.Rows[0].ValueUnits = nil },
		"fabricated-value-presence": func(p *BrowserProjection) { p.Rows[1].ValueUnits = &n },
	} {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(&p)
			if _, err := source.Render(p); err == nil {
				t.Fatal("unproved source accepted")
			}
		})
	}
	malformed := source
	malformed.Body = []byte{1}
	if _, err := malformed.Facts(); err == nil {
		t.Fatal("odd UTF16 transport admitted")
	}
}

func TestComputerBrowserProjectionMatchesObservedChromiumFixture(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/computer-browser-projection.json")
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Source     BrowserSource `json:"source"`
		Expected   string        `json:"expected"`
		Playwright string        `json:"playwright"`
		Chromium   string        `json:"chromium"`
		Program    string        `json:"program_sha256"`
	}
	if json.Unmarshal(raw, &observed) != nil || observed.Playwright != "1.64.1" || observed.Chromium == "" || len(observed.Program) != 64 {
		t.Fatal("observed official-artifact provenance missing")
	}
	five, zero := 5, 0
	plan := BrowserProjection{BodyUnits: 6, Rows: []BrowserRowView{{Index: 0, Label: "aria", LabelUnits: 256, ValueUnits: &five}, {Index: 1, Label: "inner", LabelUnits: 2, ValueUnits: &zero}, {Index: 2, Label: "placeholder", LabelUnits: 5, ValueUnits: &zero}}}
	got, err := observed.Source.Render(plan)
	if err != nil || got != observed.Expected {
		t.Fatalf("observed browser spelling lost: %q %q %v", got, observed.Expected, err)
	}
}
