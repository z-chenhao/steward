package connectedapps

import (
	"context"
	"fmt"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"reflect"
	"testing"
)

func TestAccountInventoryCompleteWindowsAndRejectInvalidProofs(t *testing.T) {
	ctx := context.Background()
	state := State{Revision: 23}
	for i := 0; i < contract.InventoryPageSize+1; i++ {
		state.Connections = append(state.Connections, Connection{Toolkit: fmt.Sprintf("app-%04d", i), Name: "original", Status: "active", ID: "private-id", AccountID: "private-provider", Alias: "private-alias"})
	}
	var offsets []int
	read := func(_ context.Context, revision int64, r contract.InventoryRequest, data []byte) (contract.InventoryPlan, error) {
		if revision != 23 || r.Count != len(state.Connections) {
			t.Fatal("lost original state identity")
		}
		rows, e := contract.DecodeInventoryRows(data, r.Count)
		if e != nil || len(rows) != r.Count {
			t.Fatal(e)
		}
		offsets = append(offsets, r.Offset)
		p := contract.InventoryPlan{Total: len(rows), Items: []contract.InventoryItem{}}
		for i := r.Offset; i < min(r.Offset+contract.InventoryPageSize, len(rows)); i++ {
			p.Items = append(p.Items, contract.InventoryItem{Row: i})
		}
		return p, nil
	}
	got, e := ManagedAccountInventory(read).Project(ctx, state, true)
	if e != nil || len(got.Rows) != len(state.Connections) || !reflect.DeepEqual(offsets, []int{0, 1024}) {
		t.Fatal("lost page boundary", len(got.Rows), offsets, e)
	}
	for _, mode := range []string{"duplicate", "short", "changed-total", "cross-toolkit-active", "invented-pending", "confirmed-row"} {
		t.Run(mode, func(t *testing.T) {
			sample := state
			if mode == "confirmed-row" {
				sample.Connections = append([]Connection{}, state.Connections...)
				sample.Connections[0].Status = "disconnected"
				sample.Connections[0].RevocationConfirmed = true
			}
			policy := ManagedAccountInventory(func(c context.Context, rev int64, r contract.InventoryRequest, raw []byte) (contract.InventoryPlan, error) {
				p, err := read(c, rev, r, raw)
				if err != nil {
					return p, err
				}
				if r.Offset > 0 {
					switch mode {
					case "duplicate":
						p.Items[0].Row = 0
					case "short":
						p.Items = nil
					case "changed-total":
						p.Total--
					case "cross-toolkit-active":
						v := 0
						p.Items[0].Active = &v
					case "invented-pending":
						v := p.Items[0].Row
						p.Items[0].Pending = &v
					}
				}
				return p, nil
			})
			p, err := policy.Project(ctx, sample, true)
			if err == nil || len(p.Rows) != 0 {
				t.Fatal("invalid or partial inventory released", mode, p, err)
			}
		})
	}
	if _, err := (AccountInventoryPolicy{}).Project(ctx, state, true); err == nil {
		t.Fatal("unavailable policy used native interpretation")
	}
}

func TestHistoricalAccountInventoryRequiresBothOriginalReleaseFences(t *testing.T) {
	ctx := context.Background()
	state := State{Revision: 41, Connections: []Connection{{Toolkit: "original", Name: "Original", Status: "active"}}}
	calls := 0
	p := HistoricalAccountInventory(func(_ context.Context, revision int64) error {
		calls++
		if revision != 41 {
			t.Fatal("historical state identity changed")
		}
		if calls == 2 {
			return ErrConflict
		}
		return nil
	})
	got, err := p.Project(ctx, state, true)
	if err != ErrConflict || len(got.Rows) != 0 || calls != 2 {
		t.Fatal("historical facts released after fence changed", got, err, calls)
	}
	if _, err = HistoricalAccountInventory(nil).Project(ctx, state, true); err == nil {
		t.Fatal("historical interpreter accepted without authority fence")
	}
}
