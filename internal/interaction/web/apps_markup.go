package web

import (
	"bytes"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"html/template"
)

type appMarkup struct{ card, sidebar, graphics *template.Template }

// This local display record has no State,credentials,account identity or methods.
// HTML slots are rendered by Host primitives,never supplied by the candidate.
type appMarkupData struct {
	Slug, URL, Name, Description, ManageLabel, AboutLabel, ConnectLabel, PendingLabel string
	ManageHidden, AddHidden, Unavailable, Pending                                     bool
	Icon, More, Plus, InterfaceReference                                              template.HTML
}

func UnavailableAppMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p.markupManaged = true
	p.markup = nil
	return p
}
func ManagedAppMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if !p.available() || p.native || len(m.Source) > 4096 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.MarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	cardProgram, sideProgram := appMarkupPrograms(p)
	compiled, err := compileAppMarkup(source, cardProgram, sideProgram)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.markupManaged = true
	p.markup = compiled
	return p, nil
}
func appPrimitive(t *template.Template, name string, card appCard, view *appsView) (template.HTML, error) {
	if name != "app-interface-reference" && view != nil && view.Interface != nil && view.Interface.graphicsManaged {
		p := view.Interface
		if p.markup == nil || p.markup.graphics == nil {
			return "", connectedapps.ErrUnavailable
		}
		return renderAppGraphic(p.markup.graphics, name, appGraphicData{Initial: card.Initial, IconURL: card.Icon, HasIcon: card.Icon != ""})
	}
	var out bytes.Buffer
	switch name {
	case "app-interface-reference":
		if err := t.ExecuteTemplate(&out, name, view); err != nil {
			return "", err
		}
	default:
		if err := t.ExecuteTemplate(&out, name, card); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil
}
func appCardDisplay(t *template.Template, v *appsView, c appCard, language string) (appMarkupData, error) {
	ui := appCardUI(v, c)
	d := appMarkupData{Slug: c.Slug, URL: c.URL, Name: c.Name, Description: translate(language, c.Description), ManageHidden: ui.ManageHidden, AddHidden: ui.AddHidden, Unavailable: ui.Unavailable, ManageLabel: appCopyText(language)(v, v.Copy().ManageApp), AboutLabel: appCopyText(language)(v, v.Copy().AboutApp), ConnectLabel: translate(language, "Connect")}
	slots := []struct {
		name   string
		target *template.HTML
	}{{"app-icon", &d.Icon}, {"app-more", &d.More}, {"app-plus", &d.Plus}, {"app-interface-reference", &d.InterfaceReference}}
	for _, s := range slots {
		value, err := appPrimitive(t, s.name, c, v)
		if err != nil {
			return d, err
		}
		*s.target = value
	}
	return d, nil
}
func appSidebarDisplay(t *template.Template, v *appsView, c appCard, language string) (appMarkupData, error) {
	d := appMarkupData{Slug: c.Slug, URL: c.URL, Name: c.Name, Pending: c.Pending}
	// This semantic binding belongs only to the new selected markup contract.
	// Old pre-markup atoms retain their exact native pending-word interpretation.
	d.PendingLabel = v.Interface.profileText(language, v.Interface.profile.Accounts[32].StatusLabel)
	icon, err := appPrimitive(t, "app-icon", c, v)
	d.Icon = icon
	return d, err
}
func renderAppMarkupAtoms(t *template.Template, prefix string, program []string, d appMarkupData) (template.HTML, error) {
	var out bytes.Buffer
	for _, op := range program {
		if err := t.ExecuteTemplate(&out, prefix+op, d); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil
}

func appMarkupPrograms(p AppInterfacePolicy) ([]string, []string) {
	cardProgram := appCardProgramLegacyV1()
	if p.cardView != nil {
		cardProgram = p.cardView.Program
	}
	sideProgram := appSidebarProgramLegacyV1()
	if p.sidebarView != nil {
		sideProgram = p.sidebarView.Program
	}
	return cardProgram, sideProgram
}
