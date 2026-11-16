package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
)

type AppConnections interface {
	AccountInventory(context.Context) (connectedapps.AccountInventoryPolicy, error)
	Settings(context.Context) (connectedapps.SettingsSelection, error)
	Directory(context.Context, contract.Query) (connectedapps.DirectorySelection, error)
	State(context.Context) (connectedapps.State, error)
	Detail(context.Context, string) (composio.Toolkit, error)
	View(context.Context, string, string, string) (connectedapps.View, error)
	ConfigureProject(context.Context, string, int64, map[string]string) error
	Connect(context.Context, string, string, string) (composio.Link, error)
	Continue(context.Context, string) (composio.Link, error)
	Reconnect(context.Context, string) (composio.Link, error)
	Check(context.Context, string) error
	Disconnect(context.Context, string) error
}
type appCard struct {
	composio.Toolkit
	Connected                       bool
	PublicActive                    bool
	Managed                         bool
	Pending                         bool
	Icon, Initial, Description, URL string
}
type appGroup struct {
	Title string
	URL   string
	Items []appCard
}
type appAuthConfigRow = connectedapps.SettingsRow

type appsView struct {
	Interface *AppInterfacePolicy
	settings  connectedapps.SettingsSelection
	connectedapps.View
	Items                    []appCard
	ConnectedOnly            bool
	Collection               string
	Directory                bool
	ConnectURL, ConnectionID string
	AuthConfigRows           []appAuthConfigRow
	ExpiresAt                int64
	Groups                   []appGroup
	Managed                  []appCard
	Selected                 *appCard
}

// Inventory includes every account even on a native app detail page, so opening
// another app in the dialog never loses its current connection controls.
func (v *appsView) Inventory() page {
	all := *v
	all.Selected = nil
	return page{Apps: &all}
}

func (s *Server) cardFor(item composio.Toolkit, selection connectedapps.DirectorySelection) appCard {
	card := appCard{Toolkit: item, URL: "/settings/apps?app=" + item.Slug}
	// A failed directory contributes no label policy. Independent account views
	// retain original facts without restoring the old display default.
	if label, err := selection.Label(item.Name, item.Slug); err == nil {
		card.Name = label.Name
		card.Initial = label.Initial
	}
	card.Icon = "/apps/logo/" + item.Slug
	return card
}
func groupApps(items []appCard, title string) []appGroup {
	if len(items) == 0 {
		return nil
	}
	return []appGroup{{Title: title, Items: items}}
}

func (s *Server) loadAppService(ctx context.Context, r *http.Request, p *page) error {
	if s.Apps == nil {
		return connectedapps.ErrUnavailable
	}
	ui := s.appInterface(ctx, "", requestLanguage(r))
	if !ui.available() || ui.pagesManaged && ui.pagesMarkup == nil || ui.settingsManaged && ui.settingsMarkup == nil {
		return connectedapps.ErrUnavailable
	}
	selection, err := s.Apps.Settings(ctx)
	if err != nil {
		return err
	}
	state := selection.State
	var original []appAuthConfigRow
	for app, config := range state.AuthConfigs {
		original = append(original, appAuthConfigRow{App: app, Config: config})
	}
	rows, err := selection.Rows(ctx, "saved", original, -1)
	if err != nil {
		return err
	}

	p.Settings = &settingsView{Section: "apps"}
	p.Apps = &appsView{View: connectedapps.View{State: state}, AuthConfigRows: rows, settings: selection, Interface: &ui}
	return nil
}
func (s *Server) loadApps(ctx context.Context, r *http.Request, p *page) (err error) {
	if s.Apps == nil {
		return connectedapps.ErrUnavailable
	}
	logoPolicy := s.appLogoPolicy(ctx)
	ui := s.appInterface(ctx, r.URL.Query().Get("interface_revision"), requestLanguage(r))
	if !ui.available() || ui.pagesManaged && ui.pagesMarkup == nil || ui.directoryManaged && ui.directoryMarkup == nil {
		return connectedapps.ErrUnavailable
	}
	inventory, inventoryErr := s.Apps.AccountInventory(ctx)
	state, err := s.Apps.State(ctx)
	if err != nil {
		return err
	}
	query := contract.Query{Search: r.URL.Query().Get("search"), Category: r.URL.Query().Get("category"), LiveConfigured: state.KeyRef != "", HasCursor: r.URL.Query().Get("cursor") != ""}
	selection, editorialErr := s.Apps.Directory(ctx, query)
	ctx = connectedapps.WithEditorial(ctx, query, selection, editorialErr)
	defer func() {
		defer func() {
			if err == nil && p.Apps != nil {
				p.Apps.Interface = &ui
				if !appCardsAvailable(p.Apps) || p.View == "apps" && !appSidebarAvailable(p.Apps) || p.Apps.Selected != nil && !appGraphicsAvailable(p.Apps) {
					err = connectedapps.ErrUnavailable
					return
				}
				s.projectAppLogos(p.Apps, logoPolicy)
			}
		}()
		if err == nil && p.Apps != nil {
			if editorialErr != nil {
				if inventoryErr != nil {
					err = inventoryErr
					return
				}
				err = s.projectAccountInventory(ctx, inventory, selection, p.Apps)
				return
			}
			err = s.projectAppInventory(ctx, selection, p.Apps)
			if err == nil {
				err = projectAppCaptions(ctx, selection, p.Apps)
			}
		}
	}()
	p.Settings = &settingsView{Section: "apps"}
	if slug := r.URL.Query().Get("app"); slug != "" {
		item, err := s.Apps.Detail(ctx, slug)
		if err != nil {
			return err
		}
		card := s.cardFor(item, selection)
		p.Apps = &appsView{View: connectedapps.View{State: state, Suggested: state.KeyRef == ""}, Selected: &card}
		return nil
	}
	if r.URL.Query().Get("view") == "connected" {
		p.Apps = &appsView{View: connectedapps.View{State: state, Suggested: state.KeyRef == ""}, ConnectedOnly: true}
		return nil
	}
	if r.URL.Query().Get("search") == "" && r.URL.Query().Get("category") == "" && r.URL.Query().Get("cursor") == "" && r.URL.Query().Get("view") != "all" {
		if editorialErr != nil {
			return editorialErr
		}
		p.Apps, err = s.loadAppDiscovery(state, r.URL.Query().Get("collection"), selection)
		return err
	}
	v, err := s.Apps.View(ctx, r.URL.Query().Get("search"), r.URL.Query().Get("category"), r.URL.Query().Get("cursor"))
	p.Apps = &appsView{View: v, Directory: true}

	if err != nil {
		return err
	}
	order, err := selection.Order(ctx, v.Catalog.Items)
	if err != nil {
		return err
	}
	for _, index := range order {
		item := v.Catalog.Items[index]
		card := s.cardFor(item, selection)
		p.Apps.Items = append(p.Apps.Items, card)
	}
	p.Apps.Groups = groupApps(p.Apps.Items, "All apps")
	return err
}

func (s *Server) appDetail(w http.ResponseWriter, r *http.Request) {
	p := page{}
	if r.URL.Query().Get("app") == "" {
		fail(w, 400)
		return
	}
	if err := s.loadApps(r.Context(), r, &p); err != nil {
		s.pageError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = requestLayout(r).ExecuteTemplate(w, "app-detail", p)
}

func (s *Server) appServiceAction(w http.ResponseWriter, r *http.Request) error {
	ui := s.appInterface(r.Context(), r.FormValue("interface_revision"), requestLanguage(r))
	r = r.WithContext(context.WithValue(r.Context(), appInterfaceKey{}, ui))
	if s.Apps == nil {
		return connectedapps.ErrUnavailable
	}
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	configs := map[string]string{}
	var rows []appAuthConfigRow
	if r.FormValue("editor") == "fields" {
		apps, ids := r.PostForm["app_slug"], r.PostForm["auth_config"]
		if len(apps) != len(ids) || len(apps) == 0 || len(apps) > 128 {
			err = connectedapps.ErrInvalid
		} else {
			for i, app := range apps {
				rows = append(rows, appAuthConfigRow{App: app, Config: ids[i]})
				if app == "" && ids[i] == "" {
					continue
				}
				if _, exists := configs[app]; exists || !composio.ValidID(app) || !composio.ValidID(ids[i]) {
					err = connectedapps.ErrInvalid
				}
				configs[app] = ids[i]
			}
		}
	} else {
		// Existing native clients retain the private serialized POST format.
		text := r.FormValue("auth_configs")
		if len(text) > 16384 || text != "" && (json.Unmarshal([]byte(text), &configs) != nil || configs == nil) {
			err = connectedapps.ErrInvalid
		}
	}
	p := page{Tab: "/settings", View: "app-service", Title: "App connection service", Path: "/settings/apps/service"}
	if e := s.loadAppService(r.Context(), r, &p); e != nil {
		return e
	}
	if len(rows) > 0 {
		p.Apps.AuthConfigRows = rows
		p.Apps.State.KeyRef = r.FormValue("key_ref")
		p.Apps.State.Revision = revision
		mode, remove, draft := appSettingsCommand(r.FormValue("action"), len(rows))
		projected, e := p.Apps.settings.Rows(r.Context(), mode, rows, remove)
		if e != nil {
			return e
		}
		p.Apps.AuthConfigRows = projected
		if draft {
			p.Notice = p.Apps.Interface.notices().Unsaved
			s.render(w, r, p)
			return nil
		}

	}
	var acceptedRows []appAuthConfigRow
	if err == nil {
		var original []appAuthConfigRow
		for app, id := range configs {
			original = append(original, appAuthConfigRow{App: app, Config: id})
		}
		acceptedRows, err = p.Apps.settings.Rows(r.Context(), "saved", original, -1)
	}
	if err == nil {
		err = s.Apps.ConfigureProject(p.Apps.settings.ConfigureContext(r.Context()), r.FormValue("key_ref"), revision, configs)
	}
	if err == nil {
		accepted, e := s.Apps.State(r.Context())
		if e != nil {
			return e
		}
		if !maps.Equal(accepted.AuthConfigs, configs) || accepted.KeyRef != r.FormValue("key_ref") {
			return connectedapps.ErrConflict
		}
		// Projection was accepted before the write. A later graph switch must not
		// reinterpret or hide this already accepted configuration and its revision.
		p.Apps.State = accepted
		p.Apps.AuthConfigRows = acceptedRows
	}

	if err != nil {
		p.Error = p.Apps.Interface.errorLabel(err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
	} else {
		p.Notice = p.Apps.Interface.notices().ServiceSaved
	}
	s.render(w, r, p)
	return nil
}
func (s *Server) appAction(w http.ResponseWriter, r *http.Request) error {
	if s.Apps == nil {
		return connectedapps.ErrUnavailable
	}
	inventory, inventoryErr := s.Apps.AccountInventory(r.Context())
	logoPolicy := s.appLogoPolicy(r.Context())
	ui := s.appInterface(r.Context(), r.FormValue("interface_revision"), requestLanguage(r))
	r = r.WithContext(context.WithValue(r.Context(), appInterfaceKey{}, ui))
	if r.FormValue("automatic") == "1" && !ui.available() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		jsonResult(w, map[string]string{"code": "presentation_unavailable", "error": "App presentation is unavailable. Reload after the owner restores the plugin."})
		return nil
	}
	r = r.WithContext(context.WithValue(r.Context(), appLogoPolicyKey{}, logoPolicy))
	var err error
	var link composio.Link
	switch r.FormValue("action") {
	case "connect":
		link, err = s.Apps.Connect(r.Context(), r.FormValue("toolkit"), r.FormValue("alias"), "")
	case "continue":
		link, err = s.Apps.Continue(r.Context(), r.FormValue("connection"))
	case "reconnect":
		link, err = s.Apps.Reconnect(r.Context(), r.FormValue("connection"))
	case "check":
		err = s.Apps.Check(r.Context(), r.FormValue("connection"))
	case "disconnect":
		err = s.Apps.Disconnect(r.Context(), r.FormValue("connection"))
	default:
		err = connectedapps.ErrInvalid
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err == nil && !ui.available() {
		return s.acceptedAppReceipt(w, r, link)
	}
	if r.Header.Get("Accept") == "application/json" {
		if err != nil {
			code := "unavailable"
			if errors.Is(err, connectedapps.ErrAliasRequired) {
				code = "alias_required"
			}
			if errors.Is(err, connectedapps.ErrLinkExpired) {
				code = "authorization_expired"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			jsonResult(w, map[string]string{"error": ui.profileText(requestLanguage(r), ui.errorLabel(err)), "code": code})
			return nil
		}
		state, e := s.Apps.State(r.Context())
		if e != nil {
			return e
		}
		selection, captionErr := s.Apps.Directory(r.Context(), contract.Query{})
		view := page{Apps: &appsView{View: connectedapps.View{State: state}, ConnectURL: link.URL, ConnectionID: link.ConnectionID, ExpiresAt: link.ExpiresAt.UnixMilli()}}
		// Optional presentation must not turn an already accepted account action
		// into a failed/retried effect. Missing captions remain unavailable.
		if captionErr == nil {
			if e := s.projectAppInventory(r.Context(), selection, view.Apps); e == nil {
				_ = projectAppCaptions(r.Context(), selection, view.Apps)
			}
		} else {
			if inventoryErr != nil {
				return s.acceptedAppReceipt(w, r, link)
			}
			if e := s.projectAccountInventory(r.Context(), inventory, selection, view.Apps); e != nil {
				return s.acceptedAppReceipt(w, r, link)
			}
		}
		view.Apps.Interface = &ui
		s.projectAppLogos(view.Apps, logoPolicy)
		var html bytes.Buffer
		if e = requestLayout(r).ExecuteTemplate(&html, "app-accounts", view); e != nil {
			// Account markup is optional presentation after the original effect
			// succeeded. Preserve its receipt/link instead of inviting a replay.
			return s.acceptedAppReceipt(w, r, link)
		}
		if !appSidebarAvailable(view.Apps) {
			return s.acceptedAppReceipt(w, r, link)
		}
		var sidebar bytes.Buffer
		if e = requestLayout(r).ExecuteTemplate(&sidebar, "app-sidebar", view); e != nil {
			return e
		}
		jsonResult(w, map[string]string{"accounts_html": html.String(), "sidebar_html": sidebar.String(), "authorization_url": link.URL, "connection_id": link.ConnectionID, "authorization_expires": strconv.FormatInt(link.ExpiresAt.UnixMilli(), 10), "revision": strconv.FormatInt(state.Revision, 10)})
		return nil
	}
	p := page{Tab: "/settings", View: "apps", Title: "Apps", Path: "/settings/apps"}
	next := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/settings/apps"
	q := r.URL.Query()
	q.Del("app")
	if slug := r.FormValue("toolkit"); slug != "" {
		q.Set("app", slug)
	} else {
		state, _ := s.Apps.State(r.Context())
		for _, c := range state.Connections {
			if c.ID == r.FormValue("connection") {
				q.Set("app", c.Toolkit)
				break
			}
		}
	}
	u.RawQuery = q.Encode()
	next.URL = &u
	if e := s.loadApps(r.Context(), next, &p); e != nil {
		if err == nil {
			return s.acceptedAppReceipt(w, r, link)
		}
		return e
	}
	p.Apps.ConnectURL, p.Apps.ConnectionID = link.URL, link.ConnectionID
	p.Apps.ExpiresAt = link.ExpiresAt.UnixMilli()
	if err != nil {
		p.Error = ui.errorLabel(err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
	} else {
		p.Notice = ui.notices().AccountSaved
	}
	s.render(w, r, p)
	return nil
}
