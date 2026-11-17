package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Distinguish checked absence from load/runtime/schema errors. A declared
// higher version never retries an older one; original /1 and /2 graphs remain
// explicit supported rollback/reference consumers.
func appsMarkupContract(ctx context.Context, h rsi.Host, revision string) (string, error) {
	g, err := h.Store.Graph(ctx, revision)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	for _, b := range g.Graph.Bindings {
		p, err := h.Catalog.Load(b.PackageDigest)
		if err != nil {
			return "", err
		}
		for _, s := range p.Manifest.Provides {
			if s.Contract == contract.PagesMarkupInterface || s.Contract == contract.DirectoryControlsMarkupInterface || s.Contract == contract.SettingsMarkupInterface || s.Contract == contract.ConnectMarkupInterface || s.Contract == contract.AccountMarkupInterface || s.Contract == contract.GraphicMarkupInterface || s.Contract == contract.MarkupInterface {
				counts[s.Contract]++
			}
		}
	}
	for _, name := range []string{contract.PagesMarkupInterface, contract.DirectoryControlsMarkupInterface, contract.SettingsMarkupInterface, contract.ConnectMarkupInterface, contract.AccountMarkupInterface, contract.GraphicMarkupInterface, contract.MarkupInterface} {
		if counts[name] > 1 {
			return "", rsi.ErrConflict
		}
		if counts[name] == 1 {
			return name, nil
		}
	}
	return "", nil
}
func attachAppsMarkup(ctx context.Context, h rsi.Host, revision string, p web.AppInterfacePolicy, history int, explicit bool) web.AppInterfacePolicy {
	unavailable := web.UnavailableAppMarkup(p)
	if history >= 7 {
		unavailable = web.UnavailableAppPagesMarkup(p)
	} else if history >= 6 {
		unavailable = web.UnavailableAppDirectoryMarkup(p)
	} else if history >= 5 {
		unavailable = web.UnavailableAppSettingsMarkup(p)
	} else if history >= 4 {
		unavailable = web.UnavailableAppConnectMarkup(p)
	} else if history >= 3 {
		unavailable = web.UnavailableAppAccountMarkup(p)
	} else if history >= 2 {
		unavailable = web.UnavailableAppGraphicMarkup(p)
	}
	name, err := appsMarkupContract(ctx, h, revision)
	if err != nil {
		return unavailable
	}
	if name == "" {
		if history == 0 || explicit {
			return p
		}
		return unavailable
	}
	if name == contract.PagesMarkupInterface {
		unavailable = web.UnavailableAppPagesMarkup(p)
	} else if name == contract.DirectoryControlsMarkupInterface {
		unavailable = web.UnavailableAppDirectoryMarkup(p)
	} else if name == contract.SettingsMarkupInterface {
		unavailable = web.UnavailableAppSettingsMarkup(p)
	} else if name == contract.ConnectMarkupInterface {
		unavailable = web.UnavailableAppConnectMarkup(p)
	} else if name == contract.AccountMarkupInterface {
		unavailable = web.UnavailableAppAccountMarkup(p)
	} else if name == contract.GraphicMarkupInterface {
		unavailable = web.UnavailableAppGraphicMarkup(p)
	} else {
		unavailable = web.UnavailableAppMarkup(p)
	}
	c, err := readAppsViewContribution(ctx, h, revision, name, contract.MarkupResource)
	if err != nil || !c.Provided {
		return unavailable
	}
	var m contract.Markup
	if rsiplugin.DecodeResult(c.Output, &m) != nil || m.Resource != contract.MarkupResource || m.Digest != c.Digest || rsiplugin.FileDigest([]byte(m.Source)) != c.Digest {
		return unavailable
	}
	var result web.AppInterfacePolicy
	if name == contract.PagesMarkupInterface {
		result, err = web.ManagedAppPagesMarkup(p, m)
	} else if name == contract.DirectoryControlsMarkupInterface {
		result, err = web.ManagedAppDirectoryMarkup(p, m)
	} else if name == contract.SettingsMarkupInterface {
		result, err = web.ManagedAppSettingsMarkup(p, m)
	} else if name == contract.ConnectMarkupInterface {
		result, err = web.ManagedAppConnectMarkup(p, m)
	} else if name == contract.AccountMarkupInterface {
		result, err = web.ManagedAppAccountMarkup(p, m)
	} else if name == contract.GraphicMarkupInterface {
		result, err = web.ManagedAppGraphicMarkup(p, m)
	} else {
		result, err = web.ManagedAppMarkup(p, m)
	}
	if err != nil {
		return unavailable
	}
	return result
}
