package intelligentui

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Cards presentation belongs to the owning package. Reference identities,
// persistence, group ownership and Artifact access never come from this markup.
// The default package exposes this producer through the managed publication path.
type cardsProps struct {
	Title string `json:"title"`
	Items []struct {
		Title    string `json:"title"`
		Text     string `json:"text"`
		ImageRef string `json:"image_ref"`
		URL      string `json:"url"`
		Label    string `json:"label"`
	} `json:"items"`
}

//go:embed view/cards-worker.js
var cardsWorker string

//go:embed view/cards.css
var cardsCSS string

func cardsViews(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != contract.CardsInput || call.Inputs[0].Size < 0 || call.Inputs[0].Size > 768<<10 {
		return rsiplugin.Result{}, errors.New("invalid cards facts input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var facts contract.CardsFacts
	if rsiplugin.DecodeInput(data, &facts) != nil {
		return rsiplugin.JSONResult(ctx, contract.CardsViewsResult{Views: []contract.CardsView{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return rsiplugin.JSONResult(ctx, renderCardsViews(facts))
}

func renderCardsViews(facts contract.CardsFacts) contract.CardsViewsResult {
	invalid := contract.CardsViewsResult{Views: []contract.CardsView{}, ErrorCode: "INVALID_ARGUMENT"}
	if facts.Cards == nil || len(facts.Cards) > 16 {
		return invalid
	}
	result := contract.CardsViewsResult{Views: []contract.CardsView{}}
	seen := map[int]bool{}
	for _, fact := range facts.Cards {
		if fact.Component < 0 || fact.Component > 15 || seen[fact.Component] {
			return invalid
		}
		seen[fact.Component] = true
		view, err := renderCardsView(fact.Props)
		if err != nil {
			return invalid
		}
		result.Views = append(result.Views, contract.CardsView{Component: fact.Component, HTML: view.HTML, Contexts: view.Contexts, Worker: cardsWorker, CSS: cardsCSS, CompactMaxWidth: 760})
	}
	return result
}

var cardsTemplate = template.Must(template.New("cards").Funcs(template.FuncMap{"literal": func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }}).Parse(`<section class="native-carousel"><div class="native-carousel-heading">{{if .Title}}<h3 class="native-answer-title" data-card-text="{{literal .Title}}">{{.Title}}</h3>{{end}}<div class="native-carousel-controls"><button class="answer-icon-button" type="button" aria-label="Previous card" disabled=""><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 5-7 7 7 7"></path></svg></button><span>1 / {{len .Items}}</span><button class="answer-icon-button" type="button" aria-label="Next card"{{if eq (len .Items) 1}} disabled=""{{end}}><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 5 7 7-7 7"></path></svg></button></div></div><div class="native-carousel-list">{{range $index, $item := .Items}}<article data-slot="card" class="native-result-card" data-selected="false">{{if .ImageRef}}<img loading="lazy" data-card-image="{{$index}}" alt="">{{end}}<button type="button" class="native-card-title" aria-pressed="false" data-card-text="{{literal .Title}}">{{.Title}}</button>{{if .Text}}<p data-card-text="{{literal .Text}}">{{.Text}}</p>{{end}}{{if .URL}}<a data-card-text="{{if .Label}}{{literal .Label}}{{else}}T3Blbg=={{end}}" href="{{.URL}}" target="_blank" rel="noopener noreferrer">{{if .Label}}{{.Label}}{{else}}Open{{end}}</a>{{end}}</article>{{end}}</div></section>`))

type renderedCards struct {
	HTML     string
	Contexts []contract.CardsContext
}

func renderCardsView(raw json.RawMessage) (renderedCards, error) {
	if !validNative(Component{Name: "steward.cards", Props: raw}) {
		return renderedCards{}, errors.New("invalid cards presentation")
	}
	var props cardsProps
	if err := json.Unmarshal(raw, &props); err != nil {
		return renderedCards{}, err
	}
	var body bytes.Buffer
	if err := cardsTemplate.Execute(&body, props); err != nil {
		return renderedCards{}, err
	}
	contexts := make([]contract.CardsContext, len(props.Items))
	for i, item := range props.Items {
		contexts[i] = contract.CardsContext{Title: props.Title, VisibleItem: item.Title}
	}
	return renderedCards{HTML: body.String(), Contexts: contexts}, nil
}
