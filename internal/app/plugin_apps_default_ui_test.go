package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	accounts "github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	directory "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The normal Server consumes the same initial standard graph and exact carrier
// as account effects and Actor work. Older owner compositions keep native UI.
func assertDefaultAppsPresentation(t *testing.T, ctx context.Context, a *App, secrets llm.SecretStore, host rsi.Host, graph string, managed bool) {
	t.Helper()
	const token = "synthetic-default-apps-page-owner"
	if err := secrets.Put(ctx, "credential:default-apps-page", []byte(token)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	server := &web.Server{Config: web.Config{TokenRef: "credential:default-apps-page"}, Core: a, Secrets: secrets, Apps: a.connectedApps,
		Interface: func(c context.Context, reference, locale string) (web.AppInterfacePolicy, error) {
			return a.appsInterfaceWithHost(c, host, reference, locale)
		},
		Logos: func(c context.Context, reference string) (web.AppLogoPolicy, error) {
			return a.appsLogosWithHost(c, host, reference)
		}}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	page := get("/settings/apps?view=connected")
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `data-app-tile="gmail"`) || !strings.Contains(body, `data-app-open="gmail"`) {
		t.Fatalf("actual default page did not render original connected app: HTTP%d", page.Code)
	}
	if strings.Contains(body, `name="interface_revision" value="`+graph+`"`) != managed {
		t.Fatal("default page changed its managed/native interface boundary")
	}
	// Exercise actual independent account business dispatch in the full bundle,
	// not only startup Owner examples. Check uses the newly accepted state revision.
	state, err := a.connectedApps.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := a.connectedApps.AccountInventory(ctx)
	if err != nil {
		t.Fatal("actual bundle inventory selection", err)
	}
	projection, err := policy.Project(ctx, state, true)
	if err != nil || len(projection.Rows) != 1 || projection.Rows[0] != 0 {
		t.Fatal("actual bundle did not dispatch account inventory", projection.Rows, err)
	}
	flags := projection.States([]string{"gmail"})
	if !flags[0].Connected || flags[0].Pending {
		t.Fatal("actual inventory lost accepted active account", flags)
	}
	if _, e := host.ServiceInstance(ctx, graph, accounts.Inventory); (e == nil) != managed {
		t.Fatal("actual bundle inventory protocol boundary changed", e)
	}
	if managed {
		// Exercise the real directory service's schema refusal, without swapping a
		// reader, graph or owner facts. The independent account view still succeeds.
		rejected := strings.Repeat("x", 201)
		if _, e := a.connectedApps.Directory(ctx, directory.Query{Search: rejected}); e == nil {
			t.Fatal("directory rejection fixture did not reach its real contract boundary")
		}
		isolated := get("/settings/apps?view=connected&search=" + url.QueryEscape(rejected))
		if isolated.Code != http.StatusOK || !strings.Contains(isolated.Body.String(), `data-app-tile="gmail"`) || !strings.Contains(isolated.Body.String(), `data-app-state="active"`) {
			t.Fatal("actual bundle lost independent account page after directory rejection", isolated.Code)
		}
	}

	if managed {
		var profile contract.BrowserProfile
		profileRaw, profileErr := host.ReadService(ctx, graph, contract.BrowserProfileInterface, []byte(`{"locale":"en"}`), nil)
		if profileErr != nil || rsiplugin.DecodeResult(profileRaw, &profile) != nil || profile.View.Worker == "" || profile.View.CSS == "" {
			t.Fatal("default did not dispatch actual client source", profileErr)
		}
		if !strings.Contains(body, "browser_view") || !strings.Contains(body, "interface_revision="+url.QueryEscape(graph)+"&amp;format=css") {
			t.Fatal("default page did not load frozen client/style")
		}
		style := get("/api/apps/browser?interface_revision=" + url.QueryEscape(graph) + "&format=css")
		if style.Code != 200 || style.Body.String() != profile.View.CSS {
			t.Fatal("default stylesheet differs from selected package", style.Code)
		}
		program := get("/api/apps/browser?interface_revision=" + url.QueryEscape(graph))
		if program.Code != 200 || !strings.Contains(program.Body.String(), "new Worker(url)") {
			t.Fatal("default did not load isolated client", program.Code)
		}
		var markup contract.Markup
		raw, err := host.ReadService(ctx, graph, contract.PagesMarkupInterface, []byte(`{}`), nil)
		if err != nil || rsiplugin.DecodeResult(raw, &markup) != nil {
			t.Fatal("complete default did not dispatch connect/account markup", err)
		}
		if !strings.Contains(markup.Source, `app-page-view-page-heading`) || !strings.Contains(markup.Source, `app-directory-view-tabs`) || !strings.Contains(markup.Source, `app-settings-view-project`) || !strings.Contains(markup.Source, `app-connect-view-additional`) {
			t.Fatal("complete default did not provide actual connect source")
		}
		directory := get("/settings/apps?view=all&search=original+query&category=communication")
		if directory.Code != http.StatusOK || !strings.Contains(directory.Body.String(), `data-app-category-form`) || !strings.Contains(directory.Body.String(), `name="search" value="original query"`) || !strings.Contains(directory.Body.String(), `<option value="communication" selected>Communication</option>`) || !strings.Contains(directory.Body.String(), `cursor=default_next`) {
			t.Fatal("complete default did not dispatch actual filter/continuation", directory.Code)
		}
		continued := get("/settings/apps?view=all&search=original+query&category=communication&cursor=default_next")
		if continued.Code != http.StatusOK || strings.Contains(continued.Body.String(), `cursor=default_next`) {
			t.Fatal("complete default lost exact-page continuation end", continued.Code)
		}
		settings := get("/settings/apps/service")
		if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `<form method="post" action="/apps/service/configure">`) || !strings.Contains(settings.Body.String(), `name="interface_revision" value="`+graph+`"`) || !strings.Contains(settings.Body.String(), `>Verify and save</button>`) || !strings.Contains(settings.Body.String(), `name="key_ref"`) {
			t.Fatal("complete default did not render managed settings form", settings.Code)
		}
		if !strings.Contains(markup.Source, `app-account-view-actions`) || !strings.Contains(body, `name="connection"`) {
			t.Fatal("complete default only declared account markup without rendering original controls")
		}
	}
	logoPath := "/apps/logo/gmail"
	if managed {
		logoPath += "?revision=" + url.QueryEscape(graph)
		if !strings.Contains(body, logoPath) {
			t.Fatal("default page lost original logo graph reference")
		}
	}
	logo := get(logoPath)
	if logo.Code != http.StatusOK || !strings.HasPrefix(logo.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(logo.Body.String(), "<svg") {
		t.Fatalf("actual default logo unavailable: HTTP%d", logo.Code)
	}
}
