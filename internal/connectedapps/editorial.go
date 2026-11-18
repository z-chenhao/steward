package connectedapps

import (
	"context"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/v1"
)

// EditorialReader is an owner control-plane binding, never a model tool.
// managed=false denotes the explicit pre-editorial/native composition. Once
// activated, missing/invalid contributions return an error, never legacy data.
type CategoryProjector func(context.Context, contract.CategoryRequest, []byte) (contract.CategoryPlan, error)

type CardStateReader func(context.Context, int64, contract.CardStateRequest, []byte) (contract.CardStatePlan, error)

type InventoryOrderer func(context.Context, int64, contract.InventoryOrderRequest, []byte) (contract.OrderPage, error)

type CatalogueOrderer func(context.Context, contract.OrderRequest, []byte) (contract.OrderPage, error)

type CaptionProjector func(context.Context, []contract.CaptionFacts) ([]contract.CaptionPlan, error)

// DirectoryMethods contains only concrete revision-bound Host callbacks, never JSON.
type DirectoryMethods struct {
	Categories     CategoryProjector
	Captions       CaptionProjector
	Order          CatalogueOrderer
	CardStates     CardStateReader
	InventoryOrder InventoryOrderer
}

type EditorialReader func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error)

type directoryMode uint8

const (
	directoryUnavailable directoryMode = iota
	directoryLegacy
	directoryManaged
)

// DirectorySelection binds the public result to a Host-owned compatibility
// fact. Candidates cannot opt into the original pre-service HTTP interpreter.
// This is an internal consumer value, not a plugin/JSON contract.
type DirectorySelection struct {
	contract.Catalogue
	mode    directoryMode
	methods *DirectoryMethods
}

type editorialSnapshot struct {
	query contract.Query
	value DirectorySelection
	err   error
}
type editorialKey struct{}

// WithEditorial freezes one already-read public contribution for one HTTP
// request's View/Detail/card consumers. It carries no account facts or rights.
func WithEditorial(ctx context.Context, query contract.Query, value DirectorySelection, err error) context.Context {
	return context.WithValue(ctx, editorialKey{}, editorialSnapshot{query: query, value: value, err: err})
}

// Editorial reads metadata from the same request snapshot regardless of the
// query; metadata consumers never interpret that query's selected result set.
func (s *Service) Editorial(ctx context.Context) (contract.Catalogue, error) {
	if snapshot, ok := ctx.Value(editorialKey{}).(editorialSnapshot); ok {
		if snapshot.value.mode == directoryUnavailable {
			return contract.Catalogue{}, ErrUnavailable
		}
		return snapshot.value.Catalogue, snapshot.err
	}
	value, err := s.Directory(ctx, contract.Query{})
	return value.Catalogue, err
}

// detailSelection reuses the request's metadata and lookup policy without
// interpreting its filter. Source availability remains the original fact.
func (s *Service) detailSelection(ctx context.Context, liveConfigured bool) (DirectorySelection, error) {
	if snapshot, ok := ctx.Value(editorialKey{}).(editorialSnapshot); ok {
		if snapshot.value.mode == directoryUnavailable {
			return DirectorySelection{}, ErrUnavailable
		}
		if snapshot.query.LiveConfigured != liveConfigured {
			return DirectorySelection{}, ErrInvalid
		}
		return snapshot.value, snapshot.err
	}
	return s.Directory(ctx, contract.Query{LiveConfigured: liveConfigured})
}

// Directory preserves the original query and its single accepted selection.
// Reusing a snapshot for another query is a caller error, never a new dispatch.
func (s *Service) Directory(ctx context.Context, query contract.Query) (DirectorySelection, error) {
	if len(query.Search) > 200 || len(query.Category) > 100 {
		return DirectorySelection{}, ErrInvalid
	}
	if snapshot, ok := ctx.Value(editorialKey{}).(editorialSnapshot); ok {
		if snapshot.value.mode == directoryUnavailable {
			return DirectorySelection{}, ErrUnavailable
		}
		if snapshot.query != query {
			return DirectorySelection{}, ErrInvalid
		}
		return snapshot.value, snapshot.err
	}
	if s.ReadEditorial == nil {
		return DirectorySelection{Catalogue: editorialLegacyV1(query), mode: directoryLegacy}, nil
	}
	value, managed, methods, err := s.ReadEditorial(ctx, query)
	if err != nil {
		return DirectorySelection{}, ErrUnavailable
	}
	if !managed {
		return DirectorySelection{Catalogue: editorialLegacyV1(query), mode: directoryLegacy}, nil
	}
	if !validLabelPolicy(value.Labels) {
		return DirectorySelection{}, ErrUnavailable
	}
	if value.Route == nil {
		return DirectorySelection{}, ErrUnavailable
	}
	switch value.Route.Source {
	case "editorial":
		if value.Route.PageSize != 0 || value.Route.OnLiveError != "" {
			return DirectorySelection{}, ErrUnavailable
		}
	case "live":
		if !query.LiveConfigured || query.HasCursor && value.Route.PageSize != 0 || !query.HasCursor && (value.Route.PageSize < 1 || value.Route.PageSize > 1000) || value.Route.OnLiveError != "unavailable" && value.Route.OnLiveError != "editorial" {
			return DirectorySelection{}, ErrUnavailable
		}
	default:
		return DirectorySelection{}, ErrUnavailable
	}
	if value.Lookup == nil || value.Lookup.PreferLive && !query.LiveConfigured || value.Lookup.LocalFirst != "editorial" && value.Lookup.LocalFirst != "connection" || value.Lookup.OnLiveError != "local" && value.Lookup.OnLiveError != "unavailable" {
		return DirectorySelection{}, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, item := range value.Items {
		if !composio.ValidID(item.Slug) || !composio.ValidID(item.CategoryID) || seen[item.Slug] || !utf8.ValidString(item.Name) || !utf8.ValidString(item.Description) || !utf8.ValidString(item.CategoryName) {
			return DirectorySelection{}, ErrUnavailable
		}
		seen[item.Slug] = true
	}
	if value.Discovery == nil {
		return DirectorySelection{}, ErrUnavailable
	}
	collections := map[string]bool{}
	for _, group := range value.Discovery.Collections {
		if !composio.ValidID(group.ID) || collections[group.ID] {
			return DirectorySelection{}, ErrUnavailable
		}
		collections[group.ID] = true
	}
	for _, groups := range [][]contract.Group{value.Discovery.Home, value.Discovery.Collections} {
		for _, group := range groups {
			if !utf8.ValidString(group.Title) || group.ID != "" && !collections[group.ID] {
				return DirectorySelection{}, ErrUnavailable
			}
			for _, slug := range group.Slugs {
				if !seen[slug] {
					return DirectorySelection{}, ErrUnavailable
				}
			}
		}
	}
	if value.Filter == nil {
		return DirectorySelection{}, ErrUnavailable
	}
	selected := map[string]bool{}
	for _, slug := range value.Filter.Slugs {
		if !seen[slug] || selected[slug] {
			return DirectorySelection{}, ErrUnavailable
		}
		selected[slug] = true
	}
	categories := map[string]bool{}
	for _, category := range value.Filter.Categories {
		if !composio.ValidID(category.ID) || categories[category.ID] || !utf8.ValidString(category.Name) {
			return DirectorySelection{}, ErrUnavailable
		}
		categories[category.ID] = true
	}
	return DirectorySelection{Catalogue: value, mode: directoryManaged, methods: methods}, nil
}

// EditorialToolkits projects validated public metadata without selecting,
// filtering or sorting it. Those decisions belong to the directory plugin.
func EditorialToolkits(value contract.Catalogue) []composio.Toolkit {
	items := []composio.Toolkit{}
	for _, entry := range value.Items {
		item := composio.Toolkit{Slug: entry.Slug, Name: entry.Name}
		item.Meta.Description = entry.Description
		item.Meta.Categories = []composio.Category{{ID: entry.CategoryID, Name: entry.CategoryName}}
		items = append(items, item)
	}
	return items
}

func EditorialDescription(value contract.Catalogue, slug string) string {
	for _, item := range value.Items {
		if item.Slug == slug {
			return item.Description
		}
	}
	return ""
}

// Exact baseline for installations predating the editorial service and explicit
// direct/unbundled Service compositions. Managed Web readers use Editorial;
// remove these data only after those composition/installation consumers migrate.
func editorialLegacyV1(query contract.Query) contract.Catalogue {
	value := contract.Catalogue{Labels: labelPolicyLegacyV1()}
	names := map[string]string{"communication": "Communication", "productivity": "Productivity", "storage": "Documents and storage", "development": "Development", "design": "Design", "business": "Business", "personal": "Personal life"}
	for _, item := range suggestions {
		value.Items = append(value.Items, contract.Entry{Slug: item.slug, Name: item.name, Description: descriptions[item.slug], CategoryID: item.category, CategoryName: names[item.category]})
	}
	value.Discovery = v1.Discovery(value.Items)
	value.Filter = v1.Filter(value.Items, query)
	value.Lookup = &contract.Lookup{PreferLive: query.LiveConfigured, LocalFirst: "editorial", OnLiveError: "local"}
	value.Route = &contract.Route{Source: "editorial"}
	if query.LiveConfigured {
		value.Route = &contract.Route{Source: "live", PageSize: 40, OnLiveError: "unavailable"}
	}
	return value
}
