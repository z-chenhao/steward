package intelligentui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
)

func TestUICardsViewMatchesOriginalBrowserDOM(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Props        json.RawMessage `json:"props"`
		InitialHTML  string          `json:"initial_html"`
		SelectedHTML string          `json:"selected_html"`
		RestoredHTML string          `json:"restored_html"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if original.SelectedHTML == "" || original.SelectedHTML != original.RestoredHTML {
		t.Fatal("original browser restore evidence changed")
	}

	actual, err := renderCardsView(original.Props)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalCardsDOM(t, actual.HTML) != canonicalCardsDOM(t, original.InitialHTML) {
		t.Fatalf("original Cards DOM changed: %s", actual)
	}
	if !slices.Equal(actual.Contexts, []contract.CardsContext{{Title: "Card choices", VisibleItem: "Alpha"}, {Title: "Card choices", VisibleItem: "Beta"}}) {
		t.Fatal("original description policy changed", actual.Contexts)
	}
}

func TestUICardsPureServiceRejectsInvalidSlotsWithoutPartialOutput(t *testing.T) {
	props := json.RawMessage(`{"items":[{"title":"A"}]}`)
	for _, facts := range []contract.CardsFacts{
		{},
		{Cards: []contract.CardsFact{{Component: -1, Props: props}}},
		{Cards: []contract.CardsFact{{Component: 16, Props: props}}},
		{Cards: []contract.CardsFact{{Component: 1, Props: props}, {Component: 1, Props: props}}},
		{Cards: []contract.CardsFact{{Component: 1, Props: props}, {Component: 2, Props: json.RawMessage(`{}`)}}},
	} {
		result := renderCardsViews(facts)
		if result.ErrorCode != "INVALID_ARGUMENT" || result.Views == nil || len(result.Views) != 0 {
			t.Fatal("invalid Cards facts leaked partial output", result)
		}
	}
	for _, inputs := range [][]rsiplugin.Input{nil, {{ID: "ui-document", Size: 10}}, {{ID: contract.CardsInput, Size: (768 << 10) + 1}}} {
		if _, err := cardsViews(context.Background(), rsiplugin.Call{Input: json.RawMessage(`{}`), Inputs: inputs}); err == nil {
			t.Fatal("Cards service accepted another input scope", inputs)
		}
	}
}

func TestUICardsViewRejectsUnsafeAndMalformedProps(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"items":[]}`, `{"items":[{"title":""}]}`,
		`{"items":[{"title":"A","url":"javascript:alert(1)"}]}`,
		`{"items":[{"title":"A","url":"https://user:secret@example.com"}]}`,
		`{"items":[{"title":"A","image_ref":"artifact:../private"}]}`,
		`{"items":[{"title":"A","onclick":"send()"}]}`,
	} {
		if markup, err := renderCardsView(json.RawMessage(raw)); err == nil || markup.HTML != "" || markup.Contexts != nil {
			t.Fatalf("invalid Cards props produced markup: %s", raw)
		}
	}
	markup, err := renderCardsView(json.RawMessage(`{"items":[{"title":"<script>run()</script>","text":"<img src=x onerror=run()>","image_ref":"artifact:local_image"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(markup.HTML, "<script>") || strings.Contains(markup.HTML, "<img src=x") || strings.Contains(markup.HTML, "artifact:local_image") || !strings.Contains(markup.HTML, `data-card-image="0"`) {
		t.Fatal("candidate markup exposed active text or raw Artifact access", markup)
	}
}

// Attribute ordering is not DOM behavior. Both expected bodies were captured
// independently in Chrome before the Cards producer existed.
func canonicalCardsDOM(t *testing.T, markup string) string {
	t.Helper()
	node, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		// Transport metadata is removed by the fixed driver before interaction.
		attrs := n.Attr[:0]
		for _, attr := range n.Attr {
			if attr.Key != "data-card-text" {
				attrs = append(attrs, attr)
			}
		}
		n.Attr = attrs
		sort.Slice(n.Attr, func(i, j int) bool {
			a, b := n.Attr[i], n.Attr[j]
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			if a.Key != b.Key {
				return a.Key < b.Key
			}
			return a.Val < b.Val
		})
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	var out bytes.Buffer
	if err = html.Render(&out, node); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestUICardsImageViewMatchesIndependentOriginalDOM(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-cards-image.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Props    json.RawMessage `json:"props"`
		Expected string          `json:"opaque_html"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	actual, err := renderCardsView(original.Props)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalCardsDOM(t, actual.HTML) != canonicalCardsDOM(t, original.Expected) {
		t.Fatal("original image Cards DOM changed", actual)
	}
	if strings.Contains(actual.HTML, "artifact:") || strings.Contains(actual.HTML, "/ui/image") {
		t.Fatal("producer exposed a raw resource address")
	}
	contexts, _ := json.Marshal(actual.Contexts)
	if strings.Contains(string(contexts), "artifact:") || !slices.Equal(actual.Contexts, []contract.CardsContext{{Title: "Card choices", VisibleItem: "Alpha"}, {Title: "Card choices", VisibleItem: "Beta"}}) {
		t.Fatal("image metadata escaped the original text description", string(contexts))
	}
}

func TestUICardsCurrentSchemaRequiresWorkerAndRejectsHistoricalRuntime(t *testing.T) {
	result := renderCardsViews(contract.CardsFacts{Cards: []contract.CardsFact{{Component: 0, Props: json.RawMessage(`{"items":[{"title":"A"}]}`)}}})
	encoded, err := json.Marshal(result)
	if err != nil || !tool.Validate(contract.CardsViewsOutputSchema, string(encoded)) {
		t.Fatal("owning Worker output rejected", err)
	}
	result.Views[0].Runtime = result.Views[0].Worker
	result.Views[0].Worker = ""
	encoded, err = json.Marshal(result)
	if err != nil || tool.Validate(contract.CardsViewsOutputSchema, string(encoded)) {
		t.Fatal("current package admitted historical DOM result", err)
	}
}

func TestUICardsLiteralDisplayTextSurvivesHTMLTransport(t *testing.T) {
	value := "Choice\r\nline\x00中文🌙<script>"
	raw, _ := json.Marshal(map[string]any{"title": value, "items": []map[string]string{{"title": value, "text": value, "label": value, "url": "https://example.com/private", "image_ref": "artifact:local_image"}}})
	view, err := renderCardsView(raw)
	if err != nil {
		t.Fatal(err)
	}
	root, err := html.Parse(strings.NewReader(view.HTML))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "data-card-text" {
				b, e := base64.StdEncoding.DecodeString(a.Val)
				if e != nil || string(b) != value {
					t.Fatalf("literal text changed: %q %v", b, e)
				}
				count++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if count != 4 {
		t.Fatalf("display fields lost: %d", count)
	}
	if strings.Contains(view.HTML, "artifact:local_image") {
		t.Fatal("raw image authority leaked")
	}
}
