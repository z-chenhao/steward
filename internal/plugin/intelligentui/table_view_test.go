package intelligentui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
)

func TestUITableProducerOriginalMarkupAndFrozenSortDescriptions(t *testing.T) {
	raw, err := os.ReadFile("testdata/original-table.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Props  json.RawMessage `json:"props"`
		Stages []struct {
			HTML string `json:"html"`
		} `json:"stages"`
		LocaleCases []struct {
			Locale string          `json:"locale"`
			ID     string          `json:"id"`
			Props  json.RawMessage `json:"props"`
		} `json:"locale_cases"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	view, err := renderTableView(original.Props)
	if err != nil {
		t.Fatal(err)
	}
	// The original JS Number classification is painted by the owning worker.
	// This static assertion excludes only those transient attributes; the real
	// browser assertion compares the complete post-paint DOM, including them.
	node, err := html.Parse(strings.NewReader(original.Stages[0].HTML))
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		attrs := n.Attr[:0]
		for _, a := range n.Attr {
			if a.Key == "data-numeric" {
				removed++
				continue
			}
			attrs = append(attrs, a)
		}
		n.Attr = attrs
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	var expected strings.Builder
	if err = html.Render(&expected, node); err != nil {
		t.Fatal(err)
	}
	actualNode, e := html.Parse(strings.NewReader(view.HTML))
	if e != nil {
		t.Fatal(e)
	}
	transport := 0
	icons := 0
	var strip func(*html.Node)
	strip = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "template" {
			for _, a := range n.Attr {
				if a.Key == "data-table-sort-icon" {
					icons++
					n.Parent.RemoveChild(n)
					return
				}
			}
		}
		attrs := n.Attr[:0]
		for _, a := range n.Attr {
			if a.Key == "data-table-facts" {
				transport++
				raw, e := base64.StdEncoding.DecodeString(a.Val)
				if e != nil {
					t.Fatal(e)
				}
				var actual, expected any
				if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(original.Props, &expected) != nil {
					t.Fatal("invalid original facts transport")
				}
				x, _ := json.Marshal(actual)
				y, _ := json.Marshal(expected)
				if string(x) != string(y) {
					t.Fatal("original text facts changed")
				}
				continue
			}
			attrs = append(attrs, a)
		}
		n.Attr = attrs
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			strip(c)
			c = next
		}
	}
	strip(actualNode)
	var actualMarkup strings.Builder
	if e = html.Render(&actualMarkup, actualNode); e != nil {
		t.Fatal(e)
	}
	if icons != 1 || transport != 1 || removed != 18 || canonicalCardsDOM(t, actualMarkup.String()) != canonicalCardsDOM(t, expected.String()) {
		t.Fatal("original static Table markup changed", view.HTML)
	}
	output, _ := json.Marshal(contract.TableViewsResult{Views: []contract.TableView{view}})
	if !tool.SchemaSupported(contract.TableViewsInputSchema) || !tool.SchemaSupported(contract.TableViewsOutputSchema) || !tool.Validate(contract.TableViewsOutputSchema, string(output)) {
		t.Fatal("pure result violates declared private output shape")
	}
	if len(view.Contexts) != 8 {
		t.Fatal("sort descriptions incomplete", view.Contexts)
	}
	for _, c := range view.Contexts {
		if c.Sort < -1 || c.Sort > 2 || (c.Direction != 1 && c.Direction != -1) || c.Description.Direction != c.Direction || c.Description.Title != "Original table evidence" {
			t.Fatal("invalid frozen context", c)
		}
		if c.Sort == -1 {
			if c.Description.Sort != nil {
				t.Fatal("unsorted context must stay null")
			}
		} else {
			names := []string{"Name", "Amount", "Note"}
			if c.Description.Sort == nil || *c.Description.Sort != names[c.Sort] {
				t.Fatal("original sort name changed", c)
			}
		}
	}
	if path := os.Getenv("STEWARD_TEST_TABLE_RESULT"); path != "" {
		// Explicit local producer/browser evidence only, never a production fallback.
		views := map[string]contract.TableView{"en-US": view}
		for _, c := range original.LocaleCases {
			v, e := renderTableView(c.Props)
			if e != nil {
				t.Fatal(e)
			}
			id := c.ID
			if id == "" {
				id = c.Locale
			}
			views[id] = v
		}
		data, e := json.Marshal(views)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func TestUITablePureProducerRejectsInvalidSlotsWithoutPartialOutput(t *testing.T) {
	props := json.RawMessage(`{"title":"T","columns":["N"],"rows":[["A"]],"source":"User"}`)
	for _, facts := range []contract.TableFacts{{}, {Tables: []contract.TableFact{{Component: -1, Props: props}}}, {Tables: []contract.TableFact{{Component: 16, Props: props}}}, {Tables: []contract.TableFact{{Component: 1, Props: props}, {Component: 1, Props: props}}}, {Tables: []contract.TableFact{{Component: 0, Props: props}, {Component: 1, Props: json.RawMessage(`{}`)}}}} {
		r := renderTableViews(facts)
		if r.ErrorCode != "INVALID_ARGUMENT" || r.Views == nil || len(r.Views) != 0 {
			t.Fatal("invalid facts leaked partial output", r)
		}
	}
	for _, inputs := range [][]rsiplugin.Input{nil, {{ID: "ui-document", Size: 10}}, {{ID: contract.TableInput, Size: -1}}, {{ID: contract.TableInput, Size: (768 << 10) + 1}}} {
		if _, e := tableViews(context.Background(), rsiplugin.Call{Input: json.RawMessage(`{}`), Inputs: inputs}); e == nil {
			t.Fatal("accepted wrong Table input scope", inputs)
		}
	}
	for _, props := range []string{`{}`, `{"title":"T","columns":["N"],"rows":[["A","B"]],"source":"User"}`, `{"title":"T","columns":["N"],"rows":[["A"]],"source":"User","onclick":"run()"}`} {
		if v, e := renderTableView(json.RawMessage(props)); e == nil || v.HTML != "" {
			t.Fatal("invalid Table produced output", v)
		}
	}
	view, e := renderTableView(json.RawMessage(`{"title":"<script>run()</script>","columns":["N"],"rows":[["<img src=x onerror=run()>"]],"source":"User"}`))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(view.HTML, "<script>") || strings.Contains(view.HTML, "<img src=") {
		t.Fatal("active user text in Table markup")
	}
}

func TestUITableProducerRetainsValidLiteralCellBudget(t *testing.T) {
	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{strings.Repeat("'", 2000)}
	}
	props, _ := json.Marshal(tableProps{Title: "Literal cells", Columns: []string{"N"}, Rows: rows, Source: "Original valid data"})
	input := `{"title":"Literal cells","fallback":"Original values","components":[{"name":"Table","props":` + string(props) + `}]}`
	if len(input) > 128<<10 || Normalize(input).ErrorCode != "" {
		t.Fatal("fixture is not an existing valid document", len(input))
	}
	view, err := renderTableView(props)
	if err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(contract.TableViewsResult{Views: []contract.TableView{view}})
	if !tool.ValidateBounded(contract.TableViewsOutputSchema, string(output), rsiplugin.MaxResultBytes) {
		t.Fatalf("existing valid literal cells exceed new view boundary: input=%d HTML=%d result=%d", len(input), len(view.HTML), len(output))
	}
}
