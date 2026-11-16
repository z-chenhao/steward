package web

import (
	"net/url"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

func (s *Server) loadAppDiscovery(state connectedapps.State, collection string, selection connectedapps.DirectorySelection) (*appsView, error) {
	editorial := selection.Catalogue
	if editorial.Discovery == nil {
		return nil, connectedapps.ErrUnavailable
	}
	v := &appsView{View: connectedapps.View{State: state, Suggested: state.KeyRef == ""}, Collection: collection}
	bySlug := map[string]appCard{}
	for _, item := range connectedapps.EditorialToolkits(editorial) {
		bySlug[item.Slug] = s.cardFor(item, selection)
	}
	groups := editorial.Discovery.Home
	if collection != "" {
		groups = nil
		for _, group := range editorial.Discovery.Collections {
			if group.ID == collection {
				groups = []contract.Group{group}
				break
			}
		}
		if groups == nil {
			return nil, connectedapps.ErrInvalid
		}
	}
	for _, group := range groups {
		row := appGroup{Title: group.Title}
		if collection == "" && group.ID != "" {
			row.URL = "/settings/apps?collection=" + url.QueryEscape(group.ID)
		}
		for _, slug := range group.Slugs {
			card, exists := bySlug[slug]
			if !exists {
				return nil, connectedapps.ErrUnavailable
			}
			row.Items = append(row.Items, card)
		}
		v.Groups = append(v.Groups, row)
		v.Items = append(v.Items, row.Items...)
	}
	return v, nil
}
