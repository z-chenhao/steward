package web

import "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"

// These flags come from the Host's actual selected contract, never a candidate
// boolean. A version2 string is final display text, including intentional brand
// words; feeding it through the Host dictionary would change its meaning.
func ManagedLocalizedAppInterface(revision string, profile contract.Profile) (AppInterfacePolicy, error) {
	p, err := ManagedAppInterface(revision, profile)
	if err != nil {
		return p, err
	}
	p.profileLocalized = true
	return p, nil
}
func ManagedLocalizedAppComposition(p AppInterfacePolicy, c contract.Composition) (AppInterfacePolicy, error) {
	p, err := ManagedAppComposition(p, c)
	if err != nil {
		return p, err
	}
	p.copyLocalized = true
	return p, nil
}
func (p AppInterfacePolicy) profileText(language, text string) string {
	if p.profileLocalized {
		return text
	}
	return translate(language, text)
}
func appProfileText(language string) func(*appsView, string) string {
	return func(view *appsView, text string) string {
		if view != nil && view.Interface != nil {
			return view.Interface.profileText(language, text)
		}
		return translate(language, text)
	}
}
func appCopyText(language string) func(*appsView, string) string {
	return func(view *appsView, text string) string {
		if view != nil && view.Interface != nil && view.Interface.copyLocalized {
			return text
		}
		return translate(language, text)
	}
}
func appPageText(language string) func(page, string) string {
	return func(p page, text string) string {
		if p.View == "apps" || p.View == "app-service" {
			return appProfileText(language)(p.Apps, text)
		}
		return translate(language, text)
	}
}
func appPageTitle(language string) func(page) string {
	return func(p page) string {
		if p.Apps != nil && p.Apps.Interface != nil && p.Apps.Interface.copyLocalized {
			if p.View == "apps" {
				return p.Apps.Copy().AppsTitle
			}
			if p.View == "app-service" {
				return p.Apps.Copy().ServiceTitle
			}
		}
		return translate(language, p.Title)
	}
}
func (view *appsView) InterfaceLocalized() bool {
	return view != nil && view.Interface != nil && view.Interface.available() && view.Interface.profileLocalized
}
