package web

import (
	"bytes"
	"encoding/json"
	"html/template"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Account display values stay inside the authorized Host render. Candidate
// source sees only these flat fields, never Connection, State or their methods.
type appAccountMarkupData struct {
	Name, Alias, Status, StatusLabel string
	Confirm, Description             string
	Action, Label                    string
	Warning, Authorization           template.HTML
	Identity, Buttons                template.HTML
}

func UnavailableAppAccountMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppGraphicMarkup(p)
	p.accountManaged = true
	p.accountMarkup = nil
	return p
}

func ManagedAppAccountMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if len(m.Source) > 16384 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.AccountMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	account, err := compileAppAccountMarkup(source.Account)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	graphic, err := json.Marshal(contract.GraphicMarkupSource{Card: source.Card, Sidebar: source.Sidebar, Graphics: source.Graphics})
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	m.Source = string(graphic)
	p, err = ManagedAppGraphicMarkup(p, m)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.accountManaged = true
	p.accountMarkup = account
	return p, nil
}

func appAccountPart(host *template.Template, row appAccountContextView, part, language string) (template.HTML, error) {
	if part != "copy" && part != "actions" {
		return "", connectedapps.ErrUnavailable
	}
	p := row.Apps.Interface
	if p == nil || !p.accountManaged {
		var out bytes.Buffer
		if err := host.ExecuteTemplate(&out, "app-account-"+part+"-legacy-v1", row); err != nil {
			return "", err
		}
		return template.HTML(out.String()), nil
	}
	if !p.available() || p.accountMarkup == nil {
		return "", connectedapps.ErrUnavailable
	}
	copy := appAccountCopy(row.Apps, language)
	d := appAccountMarkupData{Name: row.Name, Alias: row.Alias, Status: row.Status, StatusLabel: p.profileText(language, row.UI.StatusLabel), Confirm: copy.ConfirmDisconnect, Description: copy.DisconnectDescription}
	slot := func(name string) (template.HTML, error) {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, name, row)
		return template.HTML(out.String()), err
	}
	var err error
	if part == "copy" {
		if d.Warning, err = slot("app-account-required-warning"); err != nil {
			return "", err
		}
		if d.Authorization, err = slot("app-account-authorization"); err != nil {
			return "", err
		}
	} else {
		if len(row.UI.Actions) == 0 {
			return "", nil
		}
		if d.Identity, err = slot("app-account-identity"); err != nil {
			return "", err
		}
		var buttons bytes.Buffer
		for _, action := range row.UI.Actions {
			label := accountActionLabel(copy, action)
			if label == "" {
				return "", connectedapps.ErrUnavailable
			}
			button, err := renderAppAccountMarkup(p.accountMarkup, "button", appAccountMarkupData{Action: action, Label: label})
			if err != nil {
				return "", err
			}
			buttons.WriteString(string(button))
		}
		d.Buttons = template.HTML(buttons.String())
	}
	return renderAppAccountMarkup(p.accountMarkup, part, d)
}

func parseAppAccountMarkup(source string) (*template.Template, error) {
	return parsePassiveAppAtoms(source, "app-account-view-", map[string]string{
		"copy":    "Name Alias Status StatusLabel Warning Authorization",
		"actions": "Confirm Description Identity Buttons",
		"button":  "Action Label",
	})
}

func renderAppAccountMarkup(t *template.Template, part string, data appAccountMarkupData) (template.HTML, error) {
	if t == nil || (part != "copy" && part != "actions" && part != "button") {
		return "", connectedapps.ErrUnavailable
	}
	var output bytes.Buffer
	if err := t.ExecuteTemplate(&output, "app-account-view-"+part, data); err != nil {
		return "", connectedapps.ErrUnavailable
	}
	return template.HTML(output.String()), nil
}
