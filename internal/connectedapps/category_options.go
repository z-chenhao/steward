package connectedapps

import (
	"context"
	"encoding/binary"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only source identity equality is assigned here. Which occurrence survives,
// and whether the requested category is appended, are plugin policy.
func projectCategoryOptions(ctx context.Context, read CategoryProjector, items []composio.Toolkit, selected string) ([]composio.Category, error) {
	if read == nil {
		return nil, ErrUnavailable
	}
	var facts []composio.Category
	identities := map[string]uint32{}
	raw := []byte{}
	for _, item := range items {
		for _, category := range item.Meta.Categories {
			// The empty value is the existing All categories sentinel, never an option.
			if category.ID == "" {
				continue
			}
			if len(raw) > rsiplugin.MaxInputBytes-4 {
				return nil, ErrUnavailable
			}
			key := identities[category.ID]
			if key == 0 {
				key = uint32(len(identities) + 1)
				identities[category.ID] = key
			}
			raw = binary.LittleEndian.AppendUint32(raw, key)
			facts = append(facts, category)
		}
	}
	request := contract.CategoryRequest{Count: len(facts), HasRequested: selected != "", RequestedKey: identities[selected]}
	plan, err := read(ctx, request, raw)
	if err != nil {
		return nil, err
	}
	if len(plan.Keep) != (len(facts)+7)/8 {
		return nil, ErrUnavailable
	}
	if len(facts)%8 != 0 && plan.Keep[len(plan.Keep)-1]>>uint(len(facts)%8) != 0 {
		return nil, ErrUnavailable
	}
	var result []composio.Category
	seen := map[string]bool{}
	for i, fact := range facts {
		if plan.Keep[i/8]&(1<<uint(i%8)) != 0 {
			if seen[fact.ID] {
				return nil, ErrUnavailable
			}
			seen[fact.ID] = true
			result = append(result, fact)
		}
	}
	if plan.AppendRequested {
		if selected == "" || seen[selected] {
			return nil, ErrUnavailable
		}
		result = append(result, composio.Category{ID: selected, Name: selected})
	}
	return result, nil
}

func (s DirectorySelection) categoryProjector() CategoryProjector {
	if s.methods == nil {
		return nil
	}
	return s.methods.Categories
}
