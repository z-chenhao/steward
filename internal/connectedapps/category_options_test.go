package connectedapps

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialCategoryPlanUsesOnlyOriginalReferences(t *testing.T) {
	item := composio.Toolkit{}
	item.Meta.Categories = []composio.Category{{ID: "private-source-id", Name: "Private source label"}, {ID: "", Name: "All categories sentinel"}, {ID: "other", Name: "Other"}, {ID: "private-source-id", Name: "Later source label"}}
	calls := 0
	reader := func(_ context.Context, req contract.CategoryRequest, data []byte) (contract.CategoryPlan, error) {
		calls++
		if req.Count != 3 || !req.HasRequested || req.RequestedKey != 0 || len(data) != 12 {
			t.Fatal("identity-only facts changed", req, len(data))
		}
		got := []uint32{binary.LittleEndian.Uint32(data), binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:])}
		if !reflect.DeepEqual(got, []uint32{1, 2, 1}) {
			t.Fatal("original identity equality changed", got)
		}
		return contract.CategoryPlan{Keep: []byte{6}, AppendRequested: true}, nil
	}
	value, err := projectCategoryOptions(context.Background(), reader, []composio.Toolkit{item}, "original + 汉")
	expected := []composio.Category{{ID: "other", Name: "Other"}, {ID: "private-source-id", Name: "Later source label"}, {ID: "original + 汉", Name: "original + 汉"}}
	if err != nil || calls != 1 || !reflect.DeepEqual(value, expected) {
		t.Fatal("projection invented/lost/reordered source values", value, err, calls)
	}
}

func TestEditorialCategoryPlanRejectsInvalidMasksAndDuplicateReferences(t *testing.T) {
	item := composio.Toolkit{}
	item.Meta.Categories = []composio.Category{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "a", Name: "Late A"}}
	for _, test := range []struct {
		name     string
		plan     contract.CategoryPlan
		selected string
	}{
		{name: "missing-mask"},
		{name: "extra-byte", plan: contract.CategoryPlan{Keep: []byte{1, 0}}},
		{name: "outside-facts", plan: contract.CategoryPlan{Keep: []byte{128}}},
		{name: "duplicate-source-id", plan: contract.CategoryPlan{Keep: []byte{5}}},
		{name: "invented-selected", plan: contract.CategoryPlan{Keep: []byte{0}, AppendRequested: true}},
		{name: "duplicate-selected", selected: "a", plan: contract.CategoryPlan{Keep: []byte{1}, AppendRequested: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := func(context.Context, contract.CategoryRequest, []byte) (contract.CategoryPlan, error) {
				return test.plan, nil
			}
			value, err := projectCategoryOptions(context.Background(), reader, []composio.Toolkit{item}, test.selected)
			if !errors.Is(err, ErrUnavailable) || len(value) != 0 {
				t.Fatal("invalid category result became partial success", value, err)
			}
		})
	}
	if _, err := projectCategoryOptions(context.Background(), nil, []composio.Toolkit{item}, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing managed method became native fallback", err)
	}
}
