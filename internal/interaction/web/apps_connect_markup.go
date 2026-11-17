package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type appConnectMarkup struct {
	template         *template.Template
	english, chinese contract.ConnectCopy
}

// Host-local flat render facts; the candidate process receives only its own
// resource identity. Original effect fields remain in the opaque Identity slot.
type appConnectMarkupData struct {
	Slug, FirstLabel, AddAccountLabel, ConnectLabel, AccountLabel, ConnectAccountLabel string
	FirstHidden, AdditionalHidden                                                      bool
	Identity, Help                                                                     template.HTML
}

func UnavailableAppConnectMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppAccountMarkup(p)
	p.connectManaged = true
	p.connectMarkup = nil
	return p
}

func ManagedAppConnectMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if len(m.Source) > 16384 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.ConnectMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	for _, c := range []contract.ConnectCopy{source.Connect.English, source.Connect.Chinese} {
		for _, text := range []string{c.ConnectLabel, c.AccountLabel, c.ConnectAccountLabel} {
			if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 512 {
				return AppInterfacePolicy{}, connectedapps.ErrUnavailable
			}
		}
	}
	t, err := compileAppConnectMarkup(source.Connect.Program)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	raw, err := json.Marshal(source.AccountMarkupSource)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	m.Source = string(raw)
	p, err = ManagedAppAccountMarkup(p, m)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.connectManaged = true
	p.connectMarkup = &appConnectMarkup{template: t, english: source.Connect.English, chinese: source.Connect.Chinese}
	return p, nil
}

func appConnectPart(host *template.Template, v *appsView, part, language string) (template.HTML, error) {
	if part != "first" && part != "additional" {
		return "", connectedapps.ErrUnavailable
	}
	if v == nil || v.Selected == nil {
		return "", nil
	}
	p := v.Interface
	if p == nil || !p.connectManaged {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, "app-detail-connect-"+part+"-legacy-v3", page{Apps: v})
		return template.HTML(out.String()), err
	}
	if !p.available() || p.connectMarkup == nil {
		return "", connectedapps.ErrUnavailable
	}
	ui := appCardUI(v, *v.Selected)
	if part == "additional" && !ui.Additional {
		return "", nil
	}
	locale := p.connectMarkup.english
	if language == "zh-CN" {
		locale = p.connectMarkup.chinese
	}
	d := appConnectMarkupData{Slug: v.Selected.Slug, FirstLabel: p.profileText(language, ui.FirstLabel), AddAccountLabel: appCopyText(language)(v, v.Copy().AddAccount), FirstHidden: ui.FirstHidden, AdditionalHidden: ui.AdditionalHidden, ConnectLabel: locale.ConnectLabel, AccountLabel: locale.AccountLabel, ConnectAccountLabel: locale.ConnectAccountLabel}
	var slot bytes.Buffer
	if part == "first" && ui.Unavailable {
		part = "unavailable"
		if err := host.ExecuteTemplate(&slot, "help", help("app-"+d.Slug+"-connection-help", d.ConnectLabel, appCopyText(language)(v, v.Copy().ConnectionsUnavailable))); err != nil {
			return "", err
		}
		d.Help = template.HTML(slot.String())
	} else {
		if err := host.ExecuteTemplate(&slot, "app-connect-identity", v); err != nil {
			return "", err
		}
		d.Identity = template.HTML(slot.String())
	}
	return renderAppConnectMarkup(p.connectMarkup.template, part, d)
}

func parseAppConnectMarkup(source string) (*template.Template, error) {
	return parsePassiveAppAtoms(source, "app-connect-view-", map[string]string{
		"unavailable": "ConnectLabel Help",
		"first":       "Slug FirstHidden FirstLabel Identity",
		"additional":  "Slug AdditionalHidden AddAccountLabel AccountLabel ConnectAccountLabel Identity",
	})
}

func renderAppConnectMarkup(t *template.Template, part string, data appConnectMarkupData) (template.HTML, error) {
	if t == nil || (part != "first" && part != "unavailable" && part != "additional") {
		return "", connectedapps.ErrUnavailable
	}
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, "app-connect-view-"+part, data); err != nil {
		return "", connectedapps.ErrUnavailable
	}
	return template.HTML(out.String()), nil
}
