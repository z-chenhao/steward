package intelligentui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Owning Table presentation and policy. Host freezes accepted output and binds
// identities; managed failures never fall back to the historical native view.
//
//go:embed view/table-worker.js
var tableWorker string

//go:embed view/table.css
var tableCSS string

type tableProps struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Source  string     `json:"source"`
}

func tableViews(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.TableInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 768<<10 {
		return rsiplugin.Result{}, errors.New("invalid table facts input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.TableFacts
	if rsiplugin.DecodeInput(data, &facts) != nil {
		return rsiplugin.JSONResult(ctx, contract.TableViewsResult{Views: []contract.TableView{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return rsiplugin.JSONResult(ctx, renderTableViews(facts))
}

func renderTableViews(facts contract.TableFacts) contract.TableViewsResult {
	invalid := contract.TableViewsResult{Views: []contract.TableView{}, ErrorCode: "INVALID_ARGUMENT"}
	if facts.Tables == nil || len(facts.Tables) > 16 {
		return invalid
	}
	result := contract.TableViewsResult{Views: []contract.TableView{}}
	seen := map[int]bool{}
	for _, fact := range facts.Tables {
		if fact.Component < 0 || fact.Component > 15 || seen[fact.Component] {
			return invalid
		}
		seen[fact.Component] = true
		view, err := renderTableView(fact.Props)
		if err != nil {
			return invalid
		}
		view.Component = fact.Component
		result.Views = append(result.Views, view)
	}
	return result
}

// Number conversion and locale ordering stay in JavaScript. Computing them in
// Go would alter hex/whitespace numeric columns and browser locale collation.
var tableTemplate = template.Must(template.New("table").Funcs(template.FuncMap{"text": func(value string) string { return strings.ReplaceAll(value, "\x00", "\uFFFD") }}).Parse(`<section class="answer-table" data-table-facts="{{.Facts}}" aria-label="{{text .Title}}"><div class="answer-table-scroll" role="region" aria-label="{{text .Title}} table" tabindex="0"><table><caption>{{text .Title}}</caption><thead><tr>{{range .Columns}}<th scope="col" aria-sort="none"><button type="button" class="answer-column-sort">{{text .}}</button></th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range $column, $cell := .}}<td>{{if eq $column 0}}<button type="button" class="answer-cell-select" aria-pressed="false">{{text $cell}}</button>{{else}}{{text $cell}}{{end}}</td>{{end}}</tr>{{end}}</tbody></table></div><p class="answer-table-source">{{text .Source}}</p><template data-table-sort-icon><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m8 5-4 4m4-4v14m8 0 4-4m-4 4V5"></path></svg></template></section>`))

func renderTableView(raw json.RawMessage) (contract.TableView, error) {
	if !validNative(Component{Name: "Table", Props: raw}) {
		return contract.TableView{}, errors.New("invalid table presentation")
	}
	var props tableProps
	if err := json.Unmarshal(raw, &props); err != nil {
		return contract.TableView{}, err
	}
	facts, err := json.Marshal(props)
	if err != nil {
		return contract.TableView{}, err
	}
	// HTML parsing normalizes CRLF and NUL; transport original strings without
	// giving the frame an identity, network capability or arbitrary Host input.
	model := struct {
		tableProps
		Facts string
	}{props, base64.StdEncoding.EncodeToString(facts)}
	var body bytes.Buffer
	if err := tableTemplate.Execute(&body, model); err != nil {
		return contract.TableView{}, err
	}
	contexts := make([]contract.TableSortContext, 0, 2*(len(props.Columns)+1))
	for column := -1; column < len(props.Columns); column++ {
		var name *string
		if column >= 0 {
			value := props.Columns[column]
			name = &value
		}
		for _, direction := range []int{1, -1} {
			contexts = append(contexts, contract.TableSortContext{Sort: column, Direction: direction, Description: contract.TableDescription{Title: props.Title, Sort: name, Direction: direction}})
		}
	}
	return contract.TableView{HTML: body.String(), Worker: tableWorker, CSS: tableCSS, CompactMaxWidth: 760, Contexts: contexts}, nil
}
