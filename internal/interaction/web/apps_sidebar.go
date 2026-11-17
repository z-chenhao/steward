package web

import (
	"bytes"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"html/template"
)

func UnavailableAppSidebarView(p AppInterfacePolicy) AppInterfacePolicy {
	p.sidebarManaged = true
	p.sidebarView = nil
	return p
}
func ManagedAppSidebarView(p AppInterfacePolicy, v contract.SidebarView) (AppInterfacePolicy, error) {
	if !p.available() || p.native || !validAppSidebarProgram(v.Program) {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	v.Program = append([]string(nil), v.Program...)
	p.sidebarManaged = true
	p.sidebarView = &v
	return p, nil
}
func validAppSidebarProgram(p []string) bool {
	return len(p) == 5 && p[0] == "begin-entry" && p[4] == "end-entry" && appSectionSet(p[1:4], "icon", "name", "pending")
}
func appSidebarAvailable(v *appsView) bool {
	return v == nil || len(v.Managed) == 0 || v.Interface == nil || appGraphicsAvailable(v) && (!v.Interface.markupManaged || v.Interface.markup != nil) && (!v.Interface.sidebarManaged || v.Interface.sidebarView != nil)
}

// Candidate order only; original scoped row and escaped native atoms stay local.
func renderAppSidebarEntry(t *template.Template, v *appsView, c appCard, language string) (template.HTML, error) {
	p := v.Interface
	var program []string
	if p == nil || !p.sidebarManaged {
		program = appSidebarProgramLegacyV1()
	} else if p.sidebarView != nil {
		program = p.sidebarView.Program
	} else {
		return "", connectedapps.ErrUnavailable
	}
	if p != nil && p.markupManaged {
		if p.markup == nil {
			return "", connectedapps.ErrUnavailable
		}
		data, err := appSidebarDisplay(t, v, c, language)
		if err != nil {
			return "", err
		}
		return renderAppMarkupAtoms(p.markup.sidebar, "app-sidebar-atom-", program, data)
	}
	var out bytes.Buffer
	for _, op := range program {
		if err := t.ExecuteTemplate(&out, "app-sidebar-atom-"+op, c); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil
}
