package connectedapps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialCardStatesUseOnlyScopedFactsAndOriginalPublicValue(t *testing.T) {
	state := State{Revision: 17, Connections: []Connection{{Toolkit: "example", ID: "private id", Name: "private name", Alias: "private alias", AccountID: "private account", Status: "active", Public: true}, {Toolkit: "other", Status: "pending"}, {Toolkit: "example", Status: "active"}}}
	calls := 0
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{CardStates: func(_ context.Context, revision int64, request contract.CardStateRequest, data []byte) (contract.CardStatePlan, error) {
		calls++
		if revision != 17 || request.Offset != 0 {
			t.Fatal("private revision or window changed", revision, request)
		}
		index := 0
		if request.Count == 1 && reflect.DeepEqual(data, []byte{contract.FactPending}) {
			return contract.CardStatePlan{Managed: &index, Pending: &index}, nil
		}
		if request.Count != 2 || !reflect.DeepEqual(data, []byte{contract.FactActive | contract.FactPublic, contract.FactActive}) {
			t.Fatal("private data scope or facts changed", revision, request, data)
		}
		return contract.CardStatePlan{Managed: &index, Active: &index}, nil
	}}}
	projection, err := selection.Inventory(context.Background(), state)
	values := projection.States([]string{"example", "example"})
	if err != nil || calls != 2 || len(values) != 2 || values[0] != (CardState{Managed: true, Connected: true, PublicActive: true}) || values[1] != values[0] {
		t.Fatal("original proof projection failed", values, calls, err)
	}
	if !reflect.DeepEqual(projection.Rows, []int{0, 1}) || projection.States([]string{"other", "unconnected"})[0] != (CardState{Managed: true, Pending: true}) || calls != 2 {
		t.Fatal("inventory/status projection reran selector or lost original references", projection.Rows, calls)
	}
}
func TestEditorialCardStatesRejectForgedReferencesAndPartialGroups(t *testing.T) {
	ref := func(i int) *int { return &i }
	state := State{Connections: []Connection{{Toolkit: "example", Status: "pending"}, {Toolkit: "other", Status: "active", Public: true}}}
	for name, plan := range map[string]contract.CardStatePlan{"outside": {Managed: ref(1)}, "negative": {Managed: ref(-1)}, "fake-active": {Active: ref(0)}, "empty-toolkit-ref": {Managed: ref(0)}} {
		t.Run(name, func(t *testing.T) {
			sample := state
			if name == "empty-toolkit-ref" {
				sample = State{}
			}
			selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{CardStates: func(context.Context, int64, contract.CardStateRequest, []byte) (contract.CardStatePlan, error) {
				return plan, nil
			}}}
			if result, err := readCardStates(context.Background(), sample, []string{"example"}, selection); !errors.Is(err, ErrUnavailable) || result != nil {
				t.Fatal("invented private status or row escaped", result, err)
			}
		})
	}
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{CardStates: func(_ context.Context, _ int64, _ contract.CardStateRequest, data []byte) (contract.CardStatePlan, error) {
		if reflect.DeepEqual(data, []byte{contract.FactPending}) {
			return contract.CardStatePlan{Pending: ref(0)}, nil
		}
		return contract.CardStatePlan{}, errors.New("private diagnostic")
	}}}
	if result, err := readCardStates(context.Background(), state, []string{"example", "unconnected"}, selection); !errors.Is(err, ErrUnavailable) || result != nil {
		t.Fatal("partial groups or native fallback escaped", result, err)
	}
	for _, selection := range []DirectorySelection{{}, {mode: directoryManaged}} {
		if result, err := readCardStates(context.Background(), State{}, nil, selection); !errors.Is(err, ErrUnavailable) || result != nil {
			t.Fatal("missing selection/method invented native", result, err)
		}
	}
}
