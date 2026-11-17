package connectedapps

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialOrderCompleteWindowsFreezeOriginalFactsAndLabels(t *testing.T) {
	labels := labelPolicyLegacyV1()
	original := *labels
	rows := make([]composio.Toolkit, contract.OrderPageSize+3)
	for i := range rows {
		rows[i] = composio.Toolkit{Slug: fmt.Sprint(i), Name: "Original"}
	}
	offsets := []int{}
	selection := DirectorySelection{mode: directoryManaged, Catalogue: contract.Catalogue{Labels: labels}, methods: &DirectoryMethods{Order: func(_ context.Context, request contract.OrderRequest, data []byte) (contract.OrderPage, error) {
		offsets = append(offsets, request.Offset)
		if request.Count != len(rows) || request.Labels != original {
			t.Fatal("logical input mixed label policies", request.Count, request.Labels)
		}
		facts, err := contract.DecodeOrderFacts(data, len(rows))
		if err != nil || facts[0].Name != "Original" || facts[len(facts)-1].Slug != fmt.Sprint(len(rows)-1) {
			t.Fatal("complete original source changed", err)
		}
		labels.Named.UpperInitial = false
		result := make([]int, min(contract.OrderPageSize, len(rows)-request.Offset))
		for i := range result {
			result[i] = len(rows) - 1 - request.Offset - i
		}
		return contract.OrderPage{Indices: result}, nil
	}}}
	indices, err := selection.Order(context.Background(), rows)
	if err != nil || len(indices) != len(rows) || !reflect.DeepEqual(offsets, []int{0, contract.OrderPageSize}) {
		t.Fatal("output windows lost data", len(indices), offsets, err)
	}
	for i, index := range indices {
		if index != len(rows)-1-i {
			t.Fatal("source permutation changed", i, index)
		}
	}
}
func TestEditorialOrderInvalidOrPartialPermutationNeverFallsBack(t *testing.T) {
	rows := []composio.Toolkit{{Slug: "a", Name: "B"}, {Slug: "b", Name: "A"}, {Slug: "c", Name: "C"}}
	for name, result := range map[string][]int{"duplicate": {0, 0, 2}, "negative": {-1, 1, 2}, "outside": {0, 1, 3}, "short": {0, 1}, "extra": {0, 1, 2, 0}} {
		t.Run(name, func(t *testing.T) {
			selection := DirectorySelection{mode: directoryManaged, Catalogue: contract.Catalogue{Labels: labelPolicyLegacyV1()}, methods: &DirectoryMethods{Order: func(context.Context, contract.OrderRequest, []byte) (contract.OrderPage, error) {
				return contract.OrderPage{Indices: result}, nil
			}}}
			if indices, err := selection.Order(context.Background(), rows); !errors.Is(err, ErrUnavailable) || indices != nil {
				t.Fatal("bad plan released partial/native order", indices, err)
			}
		})
	}
	for _, selection := range []DirectorySelection{{}, {mode: directoryManaged, Catalogue: contract.Catalogue{Labels: labelPolicyLegacyV1()}}} {
		if indices, err := selection.Order(context.Background(), nil); !errors.Is(err, ErrUnavailable) || indices != nil {
			t.Fatal("empty/invalid selection inferred native", indices, err)
		}
	}
	calls := 0
	large := make([]composio.Toolkit, contract.OrderPageSize+1)
	selection := DirectorySelection{mode: directoryManaged, Catalogue: contract.Catalogue{Labels: labelPolicyLegacyV1()}, methods: &DirectoryMethods{Order: func(_ context.Context, request contract.OrderRequest, _ []byte) (contract.OrderPage, error) {
		calls++
		size := min(contract.OrderPageSize, len(large)-request.Offset)
		indices := make([]int, size)
		for i := range indices {
			indices[i] = request.Offset + i
		}
		if request.Offset > 0 {
			indices[0] = 0
		}
		return contract.OrderPage{Indices: indices}, nil
	}}}
	if indices, err := selection.Order(context.Background(), large); !errors.Is(err, ErrUnavailable) || indices != nil || calls != 2 {
		t.Fatal("cross-window duplicate released partial result", len(indices), err, calls)
	}
	selection.methods.Order = func(context.Context, contract.OrderRequest, []byte) (contract.OrderPage, error) {
		return contract.OrderPage{}, errors.New("private cause")
	}
	if indices, err := selection.Order(context.Background(), rows); !errors.Is(err, ErrUnavailable) || indices != nil {
		t.Fatal("error restored native order", indices, err)
	}
}
