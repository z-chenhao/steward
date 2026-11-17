package web

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

// These migration-before expectations preserve the named legacy interpreter
// and private order interpreter. Real managed dispatch has its own actual App test.
func TestAppsOriginalInventoryAndStateProjectionRemainExact(t *testing.T) {
	type card struct {
		Slug, Name                                string
		Managed, Connected, PublicActive, Pending bool
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-account-inventory-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                   string
		Rows                   []connectedapps.Connection
		ExpectedCard           card
		ExpectedInventory      []card
		ExpectedConnectedOrder []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	selection, err := (&connectedapps.Service{}).Directory(context.Background(), contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(c appCard) card { return card{c.Slug, c.Name, c.Managed, c.Connected, c.PublicActive, c.Pending} }
	s := &Server{}
	for _, fixture := range cases {
		t.Run(fixture.Name, func(t *testing.T) {
			state := connectedapps.State{Connections: fixture.Rows}
			selected := s.cardFor(composio.Toolkit{Slug: "example", Name: "Original toolkit"}, selection)
			view := &appsView{View: connectedapps.View{State: state}, Selected: &selected}
			if err := s.projectAppInventory(context.Background(), selection, view); err != nil {
				t.Fatal(err)
			}
			var actual []card
			for _, c := range view.Managed {
				actual = append(actual, project(c))
			}
			if !reflect.DeepEqual(actual, fixture.ExpectedInventory) || project(selected) != fixture.ExpectedCard {
				t.Fatal("original inventory/card projection changed", actual, project(selected))
			}
			view.Managed = projectInventoryOrder(view.Managed, connectedapps.AccountInventoryOrderFacts(inventoryOrderFacts(view.Managed), true))
			var order []string
			for _, c := range view.Managed {
				order = append(order, c.Slug)
			}
			if !reflect.DeepEqual(order, fixture.ExpectedConnectedOrder) {
				t.Fatal("original private account order changed", order, fixture.ExpectedConnectedOrder)
			}
		})
	}
}
