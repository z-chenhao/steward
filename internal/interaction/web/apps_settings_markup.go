package web

import (
	"bytes"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"html/template"
	"strings"
	"unicode/utf8"
)

// Private flat display data. No State object, SecretStore, methods or effect
// executor is reachable from a source contribution.
type appSettingsMarkupData struct {
	contract.SettingsCopy
	ProjectTitle, AuthenticationTitle, KeyRef, App, Config string
	Index                                                  int
	AdvancedOpen                                           bool
	Identity, ProjectHelp, KeyHelp, Rows                   template.HTML
}

func parseAppSettingsMarkup(source string) (*template.Template, error) {
	return parsePassiveAppAtoms(source, "app-settings-view-", map[string]string{
		"project": "ProjectTitle ProjectHelp KeyRefLabel KeyHelp Identity KeyRef AdvancedOpen AuthenticationTitle Rows AddLabel SaveLabel",
		"row":     "AuthenticationTitle AppLabel App ConfigLabel Config Index RemoveLabel",
	})
}

// Repeat the existing bounded typed projection, not a candidate program loop.
// Callers compile and validate the passive source before rendering it.
func renderAppSettingsMarkup(t *template.Template, d appSettingsMarkupData, rows []appAuthConfigRow) (template.HTML, error) {
	var body bytes.Buffer
	for i, row := range rows {
		entry := d
		entry.Index = i
		entry.App = row.App
		entry.Config = row.Config
		if err := t.ExecuteTemplate(&body, "app-settings-view-row", entry); err != nil {
			return "", err
		}
	}
	d.Rows = template.HTML(body.String())
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, "app-settings-view-project", d); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

type appSettingsMarkup struct {
	template         *template.Template
	english, chinese contract.SettingsCopy
}

func UnavailableAppSettingsMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppConnectMarkup(p)
	p.settingsManaged = true
	p.settingsMarkup = nil
	return p
}
func ManagedAppSettingsMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if len(m.Source) > 16384 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.SettingsMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	for _, c := range []contract.SettingsCopy{source.Settings.English, source.Settings.Chinese} {
		for _, s := range []string{c.KeyRefLabel, c.AppLabel, c.ConfigLabel, c.RemoveLabel, c.AddLabel, c.SaveLabel} {
			if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 512 {
				return AppInterfacePolicy{}, connectedapps.ErrUnavailable
			}
		}
	}
	t, err := compileAppSettingsMarkup(source.Settings.Program)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	raw, err := json.Marshal(source.ConnectMarkupSource)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	m.Source = string(raw)
	p, err = ManagedAppConnectMarkup(p, m)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	p.settingsManaged = true
	p.settingsMarkup = &appSettingsMarkup{template: t, english: source.Settings.English, chinese: source.Settings.Chinese}
	return p, nil
}
func appSettingsProject(host *template.Template, p page, language string) (template.HTML, error) {
	v := p.Apps
	if v == nil {
		return "", connectedapps.ErrUnavailable
	}
	policy := v.Interface
	if policy == nil || !policy.settingsManaged {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, "app-service-project-legacy-v4", p)
		return template.HTML(out.String()), err
	}
	if !policy.available() || policy.settingsMarkup == nil || len(v.AuthConfigRows) > 128 {
		return "", connectedapps.ErrUnavailable
	}
	copy := policy.settingsMarkup.english
	if language == "zh-CN" {
		copy = policy.settingsMarkup.chinese
	}
	title := appCopyText(language)(v, v.Copy().ProjectTitle)
	d := appSettingsMarkupData{SettingsCopy: copy, ProjectTitle: title, AuthenticationTitle: appCopyText(language)(v, v.Copy().AuthenticationTitle), KeyRef: v.State.KeyRef, AdvancedOpen: appAdvanced(v, p.Notice, p.Error)}
	slot := func(name string, data any) (template.HTML, error) {
		var out bytes.Buffer
		err := host.ExecuteTemplate(&out, name, data)
		return template.HTML(out.String()), err
	}
	var err error
	if d.Identity, err = slot("app-settings-identity", v); err != nil {
		return "", err
	}
	if d.ProjectHelp, err = slot("help", help("composio-project-help", title, appCopyText(language)(v, v.Copy().ProjectHelp))); err != nil {
		return "", err
	}
	if d.KeyHelp, err = slot("help", help("composio-key-help", translate(language, "API key reference"), translate(language, "Use a SecretStore or environment reference. Keep the project API key out of this form."))); err != nil {
		return "", err
	}
	return renderAppSettingsMarkup(policy.settingsMarkup.template, d, v.AuthConfigRows)
}
