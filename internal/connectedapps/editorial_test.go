package connectedapps

import (
	"context"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func TestEditorialManagedFailureNeverRestoresLegacyData(t *testing.T) {
	ctx := context.Background()
	original := contract.Entry{Slug: "custom_app", Name: "Custom app", Description: "Independent catalogue", CategoryID: "custom", CategoryName: "Custom"}
	for _, test := range []struct {
		name    string
		value   contract.Catalogue
		failure error
	}{
		{name: "reader-error", failure: errors.New("unavailable private diagnostic")},
		{name: "duplicate-slug", value: contract.Catalogue{Items: []contract.Entry{original, original}}},
		{name: "invalid-slug", value: contract.Catalogue{Items: []contract.Entry{{Slug: "../gmail", CategoryID: "custom"}}}},
		{name: "invalid-category", value: contract.Catalogue{Items: []contract.Entry{{Slug: "gmail", CategoryID: "../custom"}}}},
		{name: "missing-labels", value: contract.Catalogue{}},
		{name: "invalid-labels", value: contract.Catalogue{Labels: &contract.LabelPolicy{Named: contract.LabelPlan{NameSource: "account"}, Unnamed: contract.LabelPlan{NameSource: "slug"}}}},
		{name: "missing-lookup", value: contract.Catalogue{}},
		{name: "missing-route", value: contract.Catalogue{}},
		{name: "missing-filter", value: contract.Catalogue{Items: []contract.Entry{original}, Discovery: &contract.Discovery{}}},
		{name: "unknown-filter-slug", value: contract.Catalogue{Items: []contract.Entry{original}, Filter: &contract.Filter{Slugs: []string{"unknown"}}}},
		{name: "duplicate-filter-slug", value: contract.Catalogue{Items: []contract.Entry{original}, Filter: &contract.Filter{Slugs: []string{"custom_app", "custom_app"}}}},
		{name: "duplicate-filter-category", value: contract.Catalogue{Items: []contract.Entry{original}, Filter: &contract.Filter{Categories: []contract.Category{{ID: "custom"}, {ID: "custom"}}}}},
		{name: "invalid-filter-category", value: contract.Catalogue{Items: []contract.Entry{original}, Filter: &contract.Filter{Categories: []contract.Category{{ID: "../custom"}}}}},
		{name: "missing-discovery", value: contract.Catalogue{Items: []contract.Entry{original}}},
		{name: "unknown-discovery-slug", value: contract.Catalogue{Items: []contract.Entry{original}, Discovery: &contract.Discovery{Home: []contract.Group{{Slugs: []string{"unknown"}}}}}},
		{name: "unknown-discovery-collection", value: contract.Catalogue{Items: []contract.Entry{original}, Discovery: &contract.Discovery{Home: []contract.Group{{ID: "unknown"}}}}},
		{name: "duplicate-collections", value: contract.Catalogue{Items: []contract.Entry{original}, Discovery: &contract.Discovery{Collections: []contract.Group{{ID: "custom"}, {ID: "custom"}}}}},
		{name: "invalid-text", value: contract.Catalogue{Items: []contract.Entry{{Slug: "gmail", CategoryID: "custom", Name: string([]byte{0xff})}}}},
	} {
		if test.name != "missing-labels" && test.value.Labels == nil {
			test.value.Labels = labelPolicyLegacyV1()
		}
		if test.name != "missing-discovery" && test.value.Discovery == nil {
			test.value.Discovery = &contract.Discovery{}
		}
		if test.name != "missing-filter" && test.value.Filter == nil {
			test.value.Filter = &contract.Filter{}
		}
		if test.name != "missing-route" && test.value.Route == nil {
			test.value.Route = &contract.Route{Source: "editorial"}
		}
		if test.name != "missing-lookup" && test.value.Lookup == nil {
			test.value.Lookup = &contract.Lookup{LocalFirst: "editorial", OnLiveError: "local"}
		}
		t.Run(test.name, func(t *testing.T) {
			service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
				return test.value, true, nil, test.failure
			}}
			value, err := service.Editorial(ctx)
			if !errors.Is(err, ErrUnavailable) || len(value.Items) != 0 {
				t.Fatal("invalid managed output restored legacy data", value, err)
			}
		})
	}
	service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
		return contract.Catalogue{Items: []contract.Entry{original}, Discovery: &contract.Discovery{}, Filter: &contract.Filter{}, Route: &contract.Route{Source: "editorial"}, Lookup: &contract.Lookup{LocalFirst: "editorial", OnLiveError: "local"}, Labels: labelPolicyLegacyV1()}, true, nil, nil
	}}
	value, err := service.Editorial(ctx)
	if err != nil || len(value.Items) != 1 || value.Items[0] != original {
		t.Fatal("valid independent catalogue changed", value, err)
	}
	service.ReadEditorial = func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
		return contract.Catalogue{Items: []contract.Entry{}, Discovery: &contract.Discovery{}, Filter: &contract.Filter{}, Route: &contract.Route{Source: "editorial"}, Lookup: &contract.Lookup{LocalFirst: "editorial", OnLiveError: "local"}, Labels: labelPolicyLegacyV1()}, true, nil, nil
	}
	value, err = service.Editorial(ctx)
	if err != nil || len(value.Items) != 0 {
		t.Fatal("empty managed catalogue restored legacy data", value, err)
	}
	service.ReadEditorial = func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
		return contract.Catalogue{}, false, nil, nil
	}
	value, err = service.Editorial(ctx)
	if err != nil || len(value.Items) != 65 || value.Items[0].Slug != "gmail" {
		t.Fatal("explicit pre-service composition lost original directory", value, err)
	}
}

func TestEditorialQuerySnapshotCannotBeReusedForAnotherSearch(t *testing.T) {
	ctx := context.Background()
	q := contract.Query{Search: "original"}
	original := contract.Catalogue{Items: []contract.Entry{{Slug: "original", CategoryID: "custom"}}, Discovery: &contract.Discovery{}, Filter: &contract.Filter{Slugs: []string{"original"}}}
	calls := 0
	service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
		calls++
		return contract.Catalogue{}, true, nil, ErrUnavailable
	}}
	frozen := WithEditorial(ctx, q, DirectorySelection{Catalogue: original, mode: directoryManaged}, nil)
	result, err := service.Directory(frozen, q)
	if err != nil || len(result.Filter.Slugs) != 1 || result.Filter.Slugs[0] != "original" || calls != 0 {
		t.Fatal("accepted selection was redispatched", result, err, calls)
	}
	if _, err := service.Directory(frozen, contract.Query{Search: "changed"}); !errors.Is(err, ErrInvalid) || calls != 0 {
		t.Fatal("another query reused the frozen selection", err, calls)
	}
	metadata, err := service.Editorial(frozen)
	if err != nil || len(metadata.Items) != 1 || metadata.Items[0].Slug != "original" || calls != 0 {
		t.Fatal("metadata lost the original request snapshot", metadata, err, calls)
	}
}

func TestEditorialRouteCannotInventLiveAuthorityOrRewriteCursorSize(t *testing.T) {
	for _, test := range []struct {
		name  string
		query contract.Query
		route contract.Route
	}{
		{name: "missing-source-authority", route: contract.Route{Source: "live", PageSize: 40, OnLiveError: "unavailable"}},
		{name: "new-page-zero", query: contract.Query{LiveConfigured: true}, route: contract.Route{Source: "live", OnLiveError: "unavailable"}},
		{name: "oversize", query: contract.Query{LiveConfigured: true}, route: contract.Route{Source: "live", PageSize: 1001, OnLiveError: "unavailable"}},
		{name: "cursor-override", query: contract.Query{LiveConfigured: true, HasCursor: true}, route: contract.Route{Source: "live", PageSize: 40, OnLiveError: "unavailable"}},
		{name: "invent-legacy-tag", query: contract.Query{LiveConfigured: true}, route: contract.Route{Source: "legacy"}},
		{name: "offline-extra-recipe", route: contract.Route{Source: "editorial", PageSize: 40}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
				return contract.Catalogue{Discovery: &contract.Discovery{}, Filter: &contract.Filter{}, Route: &test.route, Labels: labelPolicyLegacyV1()}, true, nil, nil
			}}
			if _, err := service.Directory(context.Background(), test.query); !errors.Is(err, ErrUnavailable) {
				t.Fatal("invalid source recipe accepted", test.name, err)
			}
		})
	}
}

func TestEditorialDetailSnapshotCannotChangeSourceAuthority(t *testing.T) {
	ctx := context.Background()
	q := contract.Query{Search: "original"}
	original := DirectorySelection{Catalogue: contract.Catalogue{Lookup: &contract.Lookup{LocalFirst: "editorial", OnLiveError: "local"}}, mode: directoryManaged}
	calls := 0
	service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
		calls++
		return contract.Catalogue{}, true, nil, ErrUnavailable
	}}
	frozen := WithEditorial(ctx, q, original, nil)
	if _, err := service.detailSelection(frozen, true); !errors.Is(err, ErrInvalid) || calls != 0 {
		t.Fatal("detail acquired a different source authority", err, calls)
	}
	got, err := service.detailSelection(frozen, false)
	if err != nil || got.Lookup != original.Lookup || calls != 0 {
		t.Fatal("detail redispatched an accepted request snapshot", got, err, calls)
	}
}

func TestEditorialLookupCannotInventAuthorityOrAnInterpreter(t *testing.T) {
	for _, lookup := range []contract.Lookup{{PreferLive: true, LocalFirst: "editorial", OnLiveError: "local"}, {LocalFirst: "legacy", OnLiveError: "local"}, {LocalFirst: "editorial", OnLiveError: "arbitrary"}} {
		service := &Service{ReadEditorial: func(context.Context, contract.Query) (contract.Catalogue, bool, *DirectoryMethods, error) {
			return contract.Catalogue{Route: &contract.Route{Source: "editorial"}, Lookup: &lookup, Labels: labelPolicyLegacyV1(), Discovery: &contract.Discovery{}, Filter: &contract.Filter{}}, true, nil, nil
		}}
		if _, err := service.Directory(context.Background(), contract.Query{}); !errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid detail source policy accepted", lookup, err)
		}
	}
}
