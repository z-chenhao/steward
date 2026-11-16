package web

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// This is the original ordering remainder, not a claim that native sorting has
// migrated. The captured cases protect label projection's real sort dependency.
func TestAppsLabelsProjectBeforeCatalogueOrder(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-card-order-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Items    []struct{ Slug, Name string }
		Expected []int
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	selection, err := (&connectedapps.Service{}).Directory(context.Background(), contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var cards []appCard
			for i, item := range c.Items {
				card := s.cardFor(composio.Toolkit{Slug: item.Slug, Name: item.Name}, selection)
				card.URL = strconv.Itoa(i)
				cards = append(cards, card)
			}

			source := make([]composio.Toolkit, len(c.Items))
			for i, item := range c.Items {
				source[i] = composio.Toolkit{Slug: item.Slug, Name: item.Name}
			}
			order, err := selection.Order(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			sorted := make([]appCard, len(cards))
			for i, index := range order {
				sorted[i] = cards[index]
			}
			cards = sorted
			var indices []int
			for _, card := range cards {
				index, _ := strconv.Atoi(card.URL)
				indices = append(indices, index)
			}
			if !reflect.DeepEqual(indices, c.Expected) {
				t.Fatal("label projection changed original ordering", indices, c.Expected)
			}
		})
	}
}
