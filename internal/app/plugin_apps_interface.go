package app

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readAppsInterface(ctx context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
	return a.appsInterfaceWithHost(ctx, a.Plugins(), reference, locale)
}

// Select the declared versions in one checked graph/catalog pass. Catalog
// errors (including missing bytes) are never mistaken for an absent version.
func appsPresentationServices(ctx context.Context, host rsi.Host, revision string) (string, string, error) {
	graph, err := host.Store.Graph(ctx, revision)
	if err != nil {
		return "", "", err
	}
	var profile3, profile2, profile1, composition3, composition2, composition1 int
	for _, binding := range graph.Graph.Bindings {
		pkg, err := host.Catalog.Load(binding.PackageDigest)
		if err != nil {
			return "", "", err
		}
		for _, service := range pkg.Manifest.Provides {
			switch service.Contract {
			case contract.BrowserProfileInterface:
				profile3++
			case contract.LocalizedInterface:
				profile2++
			case contract.Interface:
				profile1++
			case contract.AccountCompositionInterface:
				composition3++
			case contract.LocalizedComposition:
				composition2++
			case contract.CompositionInterface:
				composition1++
			}
		}
	}
	choose := func(current, legacy string, currentCount, legacyCount int) (string, error) {
		if currentCount > 1 || currentCount == 0 && legacyCount > 1 {
			return "", rsi.ErrConflict
		}
		if currentCount == 1 {
			return current, nil
		}
		if legacyCount == 1 {
			return legacy, nil
		}
		return "", nil
	}
	var profile string
	if profile3 > 1 {
		return "", "", rsi.ErrConflict
	}
	if profile3 == 1 {
		profile = contract.BrowserProfileInterface
	} else {
		profile, err = choose(contract.LocalizedInterface, contract.Interface, profile2, profile1)
	}
	if err != nil {
		return "", "", err
	}
	if profile == "" {
		return "", "", rsi.ErrNotFound
	}
	if composition3 > 1 {
		return "", "", rsi.ErrConflict
	}
	if composition3 == 1 {
		return profile, contract.AccountCompositionInterface, nil
	}
	composition, err := choose(contract.LocalizedComposition, contract.CompositionInterface, composition2, composition1)
	return profile, composition, err
}
func (a *App) appsInterfaceWithHost(ctx context.Context, host rsi.Host, reference, locale string) (web.AppInterfacePolicy, error) {
	if locale != "en" && locale != "zh-CN" {
		return web.AppInterfacePolicy{}, rsi.ErrInvalid
	}
	revision, managed, compositionManaged, cardManaged, sidebarManaged, markupManaged, err := a.store.AppsInterfaceSelection(ctx)
	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	if reference != "" {
		revision = reference
		managed = true
	}
	if !managed {
		if compositionManaged || cardManaged || sidebarManaged || markupManaged > 0 {
			return web.AppInterfacePolicy{}, rsi.ErrNotFound
		}
		return web.NativeAppInterface(), nil
	}
	profileContract, compositionContract, err := appsPresentationServices(ctx, host, revision)
	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	localeInput, _ := json.Marshal(contract.LocaleRequest{Locale: locale})
	input := json.RawMessage(`{}`)
	if profileContract == contract.LocalizedInterface || profileContract == contract.BrowserProfileInterface {
		input = localeInput
	}
	raw, err := host.ReadService(ctx, revision, profileContract, input, nil)
	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	var p web.AppInterfacePolicy
	if profileContract == contract.BrowserProfileInterface {
		var profile contract.BrowserProfile
		if err := rsiplugin.DecodeResult(raw, &profile); err != nil {
			return web.AppInterfacePolicy{}, err
		}
		p, err = web.ManagedLocalizedAppInterface(revision, profile.Profile)
		if err == nil {
			p, err = web.ManagedAppBrowser(p, profile.View)
		}
	} else {
		var profile contract.Profile
		if err := rsiplugin.DecodeResult(raw, &profile); err != nil {
			return web.AppInterfacePolicy{}, err
		}
		if profileContract == contract.LocalizedInterface {
			p, err = web.ManagedLocalizedAppInterface(revision, profile)
		} else {
			p, err = web.ManagedAppInterface(revision, profile)
		}
	}

	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	if compositionContract == "" {
		// Only actual pre-composition consumers and explicit original references
		// retain the historical sequencing. A current opted-in gap stays unavailable.
		if !compositionManaged || reference != "" {
			p = attachAppsCardView(ctx, host, revision, p, cardManaged, reference != "")
			p = attachAppsSidebarView(ctx, host, revision, p, sidebarManaged, reference != "")
			return attachAppsMarkup(ctx, host, revision, p, markupManaged, reference != ""), nil
		}
		return web.AppInterfacePolicy{}, rsi.ErrNotFound
	}
	input = json.RawMessage(`{}`)
	if compositionContract == contract.LocalizedComposition || compositionContract == contract.AccountCompositionInterface {
		input = localeInput
	}
	raw, err = host.ReadService(ctx, revision, compositionContract, input, nil)
	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	if compositionContract == contract.AccountCompositionInterface {
		var composition contract.AccountComposition
		if err := rsiplugin.DecodeResult(raw, &composition); err != nil {
			return web.AppInterfacePolicy{}, err
		}
		p, err = web.ManagedAppAccountComposition(p, composition)
	} else {
		var composition contract.Composition
		if err := rsiplugin.DecodeResult(raw, &composition); err != nil {
			return web.AppInterfacePolicy{}, err
		}
		if compositionContract == contract.LocalizedComposition {
			p, err = web.ManagedLocalizedAppComposition(p, composition)
		} else {
			p, err = web.ManagedAppComposition(p, composition)
		}
	}
	if err != nil {
		return web.AppInterfacePolicy{}, err
	}
	p = attachAppsCardView(ctx, host, revision, p, cardManaged, reference != "")
	p = attachAppsSidebarView(ctx, host, revision, p, sidebarManaged, reference != "")
	return attachAppsMarkup(ctx, host, revision, p, markupManaged, reference != ""), nil

}
