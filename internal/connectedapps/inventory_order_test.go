package connectedapps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialInventoryOrderCompletePrivateWindowsAndOriginalRevision(t *testing.T) {
	facts := make([]contract.InventoryOrderFact, contract.OrderPageSize+3)
	for i := range facts {
		facts[i] = contract.InventoryOrderFact{Name: "private original", Slug: "original"}
	}
	original := contract.EncodeInventoryOrderFacts(facts)
	var offsets []int
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{InventoryOrder: func(_ context.Context, revision int64, request contract.InventoryOrderRequest, data []byte) (contract.OrderPage, error) {
		if revision != 37 || request.Count != len(facts) || !request.Connected || !reflect.DeepEqual(data, original) {
			t.Fatal("private input/revision/view changed", revision, request)
		}
		offsets = append(offsets, request.Offset)
		if request.Offset == 0 {
			facts[0].Name = "caller changed after first window"
		}
		page := contract.OrderPage{Indices: make([]int, min(contract.OrderPageSize, request.Count-request.Offset))}
		for i := range page.Indices {
			page.Indices[i] = request.Count - 1 - request.Offset - i
		}
		return page, nil
	}}}
	indices, err := selection.InventoryOrder(context.Background(), 37, facts, true)
	if err != nil || len(indices) != len(facts) || indices[0] != len(facts)-1 || indices[len(indices)-1] != 0 || !reflect.DeepEqual(offsets, []int{0, 16384}) {
		t.Fatal("whole private permutation failed", offsets, err)
	}
}
func TestEditorialInventoryOrderInvalidPermutationNeverReleasesPartialOrNative(t *testing.T) {
	for _, mode := range []string{"duplicate", "outside", "negative", "short", "late-replay", "late-error"} {
		t.Run(mode, func(t *testing.T) {
			facts := make([]contract.InventoryOrderFact, contract.OrderPageSize+3)
			selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{InventoryOrder: func(_ context.Context, _ int64, request contract.InventoryOrderRequest, _ []byte) (contract.OrderPage, error) {
				page := contract.OrderPage{Indices: make([]int, min(contract.OrderPageSize, request.Count-request.Offset))}
				for i := range page.Indices {
					page.Indices[i] = request.Offset + i
				}
				if request.Offset == 0 {
					switch mode {
					case "duplicate":
						page.Indices[1] = 0
					case "outside":
						page.Indices[0] = request.Count
					case "negative":
						page.Indices[0] = -1
					case "short":
						page.Indices = page.Indices[:1]
					}
				} else {
					switch mode {
					case "late-replay":
						page.Indices[0] = 0
					case "late-error":
						return contract.OrderPage{}, errors.New("private diagnostics")
					}
				}
				return page, nil
			}}}
			if indices, err := selection.InventoryOrder(context.Background(), 37, facts, false); !errors.Is(err, ErrUnavailable) || indices != nil {
				t.Fatal("invalid/private partial/native result released", len(indices), err)
			}
		})
	}
	for _, selection := range []DirectorySelection{{}, {mode: directoryManaged}} {
		if indices, err := selection.InventoryOrder(context.Background(), 37, nil, false); !errors.Is(err, ErrUnavailable) || indices != nil {
			t.Fatal("empty/missing selection inferred native", indices, err)
		}
	}
	calls := 0
	selection := DirectorySelection{mode: directoryManaged, methods: &DirectoryMethods{InventoryOrder: func(context.Context, int64, contract.InventoryOrderRequest, []byte) (contract.OrderPage, error) {
		calls++
		return contract.OrderPage{Indices: []int{}}, nil
	}}}
	if indices, err := selection.InventoryOrder(context.Background(), 37, nil, false); err != nil || len(indices) != 0 || calls != 1 {
		t.Fatal("empty source skipped managed service", indices, calls, err)
	}
}
