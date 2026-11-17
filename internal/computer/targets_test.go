package computer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/fake"
)

type targetDriver struct {
	fake.Driver
	values []computer.Target
}

func (d *targetDriver) Inventory(context.Context, computer.Instance, string) ([]computer.Target, error) {
	return d.values, nil
}
func TestComputerInventoryRejectsIncompleteOrInventedPermutation(t *testing.T) {
	for _, indices := range [][]int{{}, {0}, {0, 0}, {1, 2}, {-1, 0}} {
		t.Run(stringifyIndices(indices), func(t *testing.T) {
			d := &targetDriver{values: []computer.Target{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}}}
			r := computer.UseRuntime{Driver: d, Store: &store{}, OrderInventory: func(context.Context, string, []computer.Target) ([]int, error) { return indices, nil }}
			result, err := r.Call(context.Background(), "builtin__computer_discover", `{"method":"listApps"}`)
			if !errors.Is(err, computer.ErrInvalid) || result.Text != "" {
				t.Fatal("candidate changed inventory membership", result, err)
			}
		})
	}
}
func stringifyIndices(indices []int) string { raw, _ := json.Marshal(indices); return string(raw) }
func TestComputerTargetIdentityPinRejectsRedirectionAndDuplicateIdentity(t *testing.T) {
	for _, ids := range [][]string{{"exact", "other"}, {"exact", "exact"}} {
		t.Run(ids[1], func(t *testing.T) {
			s := &store{}
			r := computer.UseRuntime{Driver: &targetDriver{values: []computer.Target{{ID: ids[0], Name: "One"}, {ID: ids[1], Name: "Two"}}}, Store: s, MatchTargets: func(context.Context, string, string, []computer.Target) ([]int, error) { return []int{1}, nil }}
			result, err := r.Call(context.Background(), "builtin__computer_bind", `{"method":"getApp","args":["exact"]}`)
			if !errors.Is(err, computer.ErrInvalid) || result.Text != "" || len(s.state.Handles) != 0 {
				t.Fatal("candidate substituted exact identity", result, err, s.state)
			}
		})
	}
}
