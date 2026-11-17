package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The owner sees only its authorized display fields, never page, Server,
// credential references, provider methods or unrelated feature state.
type modelViewData struct {
	Settings        *modelViewSettings
	Accounts        []modelViewAccount
	Auth            modelViewAuth
	AuthError       string
	AuthOnResources bool
}
type modelViewSettings struct {
	DirectModels                                              []modelViewRow
	Model, Endpoint, Name, Service, WireAPI, ConnectionMethod string
}
type modelViewRow struct {
	ProfileID, Name, ModelID, Endpoint, ConnectionStatus string
	Current, Verified, Open, KeyAuth                     bool
	Version, AuthVersion, ConfigVersion                  int64
}
type modelViewAccount struct{ ConnectionID, AccountLabel, Status string }
type modelViewAuth struct {
	Status string
	Events []modelViewEvent
}
type modelViewEvent struct{ URL, DeviceCode string }

func modelViewModel(p page) modelViewData {
	d := modelViewData{Settings: &modelViewSettings{}, AuthError: p.AuthError, AuthOnResources: p.AuthOnResources, Auth: modelViewAuth{Status: p.Auth.Status}}
	for _, a := range p.Accounts {
		d.Accounts = append(d.Accounts, modelViewAccount{a.ConnectionID, a.AccountLabel, a.Status})
	}
	for _, e := range p.Auth.Events {
		d.Auth.Events = append(d.Auth.Events, modelViewEvent{URL: e.URL, DeviceCode: e.DeviceCode})
	}
	if v := p.Settings; v != nil {
		d.Settings.Model, d.Settings.Endpoint, d.Settings.Name, d.Settings.Service, d.Settings.WireAPI, d.Settings.ConnectionMethod = v.Model, v.Endpoint, v.Name, v.Service, v.WireAPI, v.ConnectionMethod
		for _, r := range v.DirectModels {
			d.Settings.DirectModels = append(d.Settings.DirectModels, modelViewRow{ProfileID: r.ProfileID, Name: r.Name, ModelID: r.ModelID, Endpoint: r.Endpoint, ConnectionStatus: r.ConnectionStatus, Current: r.Current, Verified: r.Verified, Open: r.Open, KeyAuth: r.KeyAuth, Version: r.Version, AuthVersion: r.AuthVersion, ConfigVersion: r.ConfigVersion})
		}
	}
	return d
}

var modelViewNames = map[string]bool{"model-list": true, "model-new": true, "signin": true}
var modelViewShared = map[string]bool{"help": true, "icon-more-v1": true}

const modelViewFields = "Settings Accounts Auth AuthError AuthOnResources DirectModels Model Endpoint Name Service WireAPI ConnectionMethod ProfileID ModelID ConnectionStatus Current Verified Open KeyAuth Version AuthVersion ConfigVersion ConnectionID AccountLabel Status Events URL DeviceCode"

func compileModelView(source string, r *http.Request, copy map[string]string) (*template.Template, error) {
	functions := template.FuncMap{"tr": func(s string) string { return translate(requestLanguage(r), s) }, "modelTr": func(s string) string {
		if requestLanguage(r) == "zh-CN" {
			if value, ok := copy[s]; ok {
				return value
			}
		}
		return s
	}, "help": help, "authURL": AuthURL}
	return compileInspectionView(source, functions, requestLayout(r), inspectionTemplateSpec{root: "model-connections-view", names: modelViewNames, privateName: func(name string) bool { return strings.HasPrefix(name, "model-") }, shared: modelViewShared, fields: modelViewFields, functions: " modelTr help authURL eq ne and or not len ", unavailable: llm.ErrConflict})
}

type modelViewBuffer struct {
	bytes.Buffer
	remaining int
}

func (w *modelViewBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, llm.ErrConflict
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}
func (w *modelViewBuffer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func renderModelView(p page, r *http.Request, name string) (template.HTML, error) {
	if p.Settings == nil || p.Settings.View == nil || p.Settings.Copy == nil || !modelViewNames[name] {
		return "", llm.ErrConflict
	}
	var entries []contract.Translation
	if e := json.Unmarshal([]byte(p.Settings.Copy.Source), &entries); e != nil || len(entries) > 512 {
		return "", llm.ErrConflict
	}
	copy := map[string]string{}
	for _, entry := range entries {
		if entry.Key == "" || entry.Value == "" {
			return "", llm.ErrConflict
		}
		if _, exists := copy[entry.Key]; exists {
			return "", llm.ErrConflict
		}
		copy[entry.Key] = entry.Value
	}
	t, e := compileModelView(p.Settings.View.Source, r, copy)
	if e != nil {
		return "", e
	}
	d := modelViewModel(p)
	raw, e := json.Marshal(d)
	if e != nil {
		return "", e
	}
	out := modelViewBuffer{remaining: 65536 + 16*len(raw)}
	if e = t.ExecuteTemplate(&out, name, d); e != nil {
		return "", e
	}
	if !safeModelViewDOM(out.String(), d) {
		return "", llm.ErrConflict
	}
	return template.HTML(out.String()), nil
}

// Keep effects behind the existing same-origin endpoints and original source
// identities. Scripts, outbound resources, alternate form targets and bypasses
// of the shared destructive-action confirmation cannot enter the host DOM.
func safeModelViewDOM(source string, d modelViewData) bool {
	nodes, e := html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if e != nil {
		return false
	}
	accounts := map[string]bool{}
	for _, a := range d.Accounts {
		accounts[a.ConnectionID] = true
	}
	allowedAuth := map[string]bool{}
	for _, event := range d.Auth.Events {
		if u := AuthURL(event.URL); u != "" {
			allowedAuth[u] = true
		}
	}
	tags := " a button code dd details div dl dt form h1 h2 input label option p section select span strong summary svg path circle line polyline rect "
	attrs := func(n *html.Node) map[string]string {
		m := map[string]string{}
		for _, a := range n.Attr {
			m[a.Key] = a.Val
		}
		return m
	}
	forms := map[string]bool{"/model/add": true, "/model/reconnect": true, "/model/disconnect": true, "/auth/login": true, "/auth/logout": true, "/auth/cancel": true}
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if !strings.Contains(tags, " "+n.Data+" ") {
				return false
			}
			a := attrs(n)
			dataAttrs := " data-status data-account-connection data-form-menu data-open-signin data-settings-confirm data-confirm-description data-model-verify data-api-connection data-context-help data-help-fallback data-help-trigger data-help-tooltip data-signin-pending data-signin-fallback data-account-reconnected "
			for _, attr := range n.Attr {
				key := strings.ToLower(attr.Key)
				allowedAttrs := " class id role title placeholder tabindex type name value maxlength autocomplete spellcheck autocapitalize required for hidden open selected disabled inert method action href target rel width height viewbox fill stroke stroke-width stroke-linecap stroke-linejoin d cx cy r x y x1 x2 y1 y2 points rx ry "
				if !strings.Contains(allowedAttrs, " "+key+" ") && !strings.HasPrefix(key, "aria-") && !strings.HasPrefix(key, "data-") {
					return false
				}
				if key == "href" && n.Data != "a" {
					return false
				}
				if strings.HasPrefix(key, "data-") && !strings.Contains(dataAttrs, " "+key+" ") && !strings.HasPrefix(key, "data-model-owner-") {
					return false
				}
				if strings.Contains(strings.ToLower(attr.Val), "url(") {
					return false
				}
				if attr.Namespace != "" || strings.HasPrefix(key, "on") || key == "style" || key == "src" || key == "srcset" || key == "form" || strings.HasPrefix(key, "formaction") || key == "formtarget" || key == "formenctype" || key == "formmethod" || key == "autofocus" {
					return false
				}
				if key == "action" && n.Data != "form" {
					return false
				}
			}
			if n.Data == "a" {
				href := a["href"]
				u, e := url.Parse(href)
				if e != nil {
					return false
				}
				local := u.Scheme == "" && u.Host == "" && !strings.HasPrefix(href, "//") && (u.Path == "/settings/models" || strings.HasPrefix(u.Path, "/settings/models/"))
				if !local && (!allowedAuth[href] || a["target"] != "_blank" || !strings.Contains(" "+a["rel"]+" ", " noreferrer ")) {
					return false
				}
			}
			if n.Data == "form" {
				action := a["action"]
				if action == "/model/add" {
					if _, ok := a["data-model-owner-connection"]; !ok {
						return false
					}
					if !modelProviderOptions(n) {
						return false
					}
				}
				if !forms[action] || strings.ToLower(a["method"]) != "post" {
					return false
				}
				if (action == "/auth/logout" || action == "/model/disconnect") && (a["data-settings-confirm"] == "" || a["data-confirm-description"] == "") {
					return false
				}
				allowedControls := map[string]string{"/model/add": " service api_key model endpoint wire_api name ", "/model/reconnect": " profile profile_version config_version consent ", "/model/disconnect": " profile profile_version auth_version confirm ", "/auth/login": " account provider return_to ", "/auth/logout": " account return_to ", "/auth/cancel": " return_to "}
				values := map[string]string{}
				var controls func(*html.Node) bool
				controls = func(c *html.Node) bool {
					if c != n && c.Type == html.ElementNode && c.Data == "form" {
						return false
					}
					if c.Type == html.ElementNode && (c.Data == "input" || c.Data == "select" || c.Data == "button") {
						ca := attrs(c)
						name := ca["name"]
						if name != "" {
							if !strings.Contains(allowedControls[action], " "+name+" ") {
								return false
							}
							if _, exists := values[name]; exists {
								return false
							}
							values[name] = ca["value"]
						}
						if name == "api_key" && (action != "/model/add" || ca["type"] != "password" || ca["value"] != "") {
							return false
						}
					}
					for child := c.FirstChild; child != nil; child = child.NextSibling {
						if !controls(child) {
							return false
						}
					}
					return true
				}
				if !controls(n) {
					return false
				}
				switch action {
				case "/model/reconnect", "/model/disconnect":
					var row *modelViewRow
					for i := range d.Settings.DirectModels {
						r := &d.Settings.DirectModels[i]
						if r.ProfileID == values["profile"] {
							row = r
							break
						}
					}
					if row == nil {
						return false
					}
					if !modelFormVersions(values, *row, action) {
						return false
					}
				case "/auth/login", "/auth/logout":
					if id := values["account"]; id != "" && !accounts[id] {
						return false
					}
					if action == "/auth/logout" && values["account"] == "" {
						return false
					}
					if provider := values["provider"]; provider != "" && provider != "chatgpt" {
						return false
					}
				}
				if target := values["return_to"]; target != "" && target != "/settings/models" {
					return false
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if !walk(child) {
				return false
			}
		}
		return true
	}
	for _, n := range nodes {
		if !walk(n) {
			return false
		}
	}
	return true
}
func modelFormVersions(values map[string]string, row modelViewRow, action string) bool {
	if values["profile_version"] != modelVersion(row.Version) {
		return false
	}
	if action == "/model/reconnect" {
		return values["config_version"] == modelVersion(row.ConfigVersion) && values["consent"] == "yes"
	}
	return values["auth_version"] == modelVersion(row.AuthVersion) && values["confirm"] == "yes"
}

func modelVersion(v int64) string { return strconv.FormatInt(v, 10) }

// Validate only the actual field-binding operations, not provider business values.
func modelProviderOptions(form *html.Node) bool {
	valid := true
	count := 0
	var visit func(*html.Node, bool)
	visit = func(n *html.Node, service bool) {
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if n.Type == html.ElementNode && n.Data == "select" {
			service = attrs["name"] == "service"
		}
		if n.Type == html.ElementNode && n.Data == "option" && service {
			count++
			for _, name := range []string{"key-required", "endpoint-readonly", "expand-advanced", "endpoint-force"} {
				v := attrs["data-model-owner-"+name]
				if v != "true" && v != "false" {
					valid = false
				}
			}
			for _, name := range []string{"endpoint-default", "endpoint-replace"} {
				value, ok := attrs["data-model-owner-"+name]
				if !ok || name == "endpoint-default" && value == "" {
					valid = false
					continue
				}
				if value != "" {
					u, e := url.Parse(value)
					if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
						valid = false
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child, service)
		}
	}
	visit(form, false)
	return valid && count > 0
}
