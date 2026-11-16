package appsaccounts

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"os"
	"reflect"
	"testing"
)

// Expectations were executed from the original independent account workflow
// before this producer existed, not calculated by the replacement algorithm.
func TestInventoryMatchesCapturedOriginalWorkflow(t *testing.T) {
	type state struct{ Managed, Connected, PublicActive, Pending bool }
	var fixture struct {
		Cases []struct {
			Name      string
			Connected bool
			Rows      []struct {
				Toolkit, Name, Status string
				Public                bool
				RevocationConfirmed   bool `json:"revocation_confirmed"`
			}
			Indices []int
			States  []state
		}
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/apps-account-inventory-original-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 12 {
		t.Fatal("missing original scenarios")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			rows := make([]contract.InventoryRow, len(c.Rows))
			for i, row := range c.Rows {
				bits := byte(0)
				switch row.Status {
				case "active":
					bits |= contract.Active
				case "pending":
					bits |= contract.Pending
				case "disconnected":
					bits |= contract.Disconnected
				}
				if row.Public {
					bits |= contract.Public
				}
				if row.RevocationConfirmed {
					bits |= contract.RevocationConfirmed
				}
				rows[i] = contract.InventoryRow{Slug: row.Toolkit, Name: row.Name, Facts: bits}
			}
			decoded, e := contract.DecodeInventoryRows(contract.EncodeInventoryRows(rows), len(rows))
			if e != nil || !reflect.DeepEqual(decoded, rows) {
				t.Fatal("original display facts changed", e)
			}
			got := chooseInventory(decoded, c.Connected)
			var indices []int
			states := make([]state, len(got))
			for i, item := range got {
				indices = append(indices, item.Row)
				states[i] = state{Managed: true, Connected: item.Active != nil, Pending: item.Pending != nil}
				if item.Active != nil {
					states[i].PublicActive = rows[*item.Active].Facts&contract.Public != 0
				}
			}
			if !reflect.DeepEqual(indices, c.Indices) || !reflect.DeepEqual(states, c.States) {
				t.Fatalf("original projection changed: indices=%v want=%v states=%+v want=%+v", indices, c.Indices, states, c.States)
			}
		})
	}
}
