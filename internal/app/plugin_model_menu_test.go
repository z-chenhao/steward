package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestModelMenuActualDefaultUpgradeRollbackDeactivationAndCold(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, e := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	facts := contract.Facts{Candidates: []contract.Candidate{{Key: "profile:one:1", Name: "Original", Connected: true, Verified: true, Current: true}}, Catalogues: []contract.Catalogue{}}
	if _, managed, e := a.readModelMenuWithHost(ctx, host, facts); e != nil || managed {
		t.Fatal("historical installation lost explicit boundary", managed, e)
	}
	if err = builtin.EnsureDefault(ctx, host, builtin.Directories{DOCX: defaultFeaturePackage(t, "docx-reader", nil), ModelConnections: defaultFeaturePackage(t, "model-connections", nil)}); err != nil {
		t.Fatal(err)
	}
	initial, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var policyID string
	if err = a.store.Plugins().DB.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, initial.Revision).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	originalPolicy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	settingsFacts := contract.SettingsFacts{WithView: true, Rows: []contract.SettingsRow{{Ref: "one", AuthStatus: "connected", Verified: true, Current: true}}, Requested: "one", Notice: "tested"}
	connectionFacts := contract.ConnectionFacts{Service: "custom", Login: &contract.LoginFacts{Models: []string{"Zulu", "Alpha"}}}
	check := func(want string) {
		t.Helper()
		out, managed, e := a.readModelMenuWithHost(ctx, host, facts)
		if e != nil || !managed || len(out.Choices) != 1 || out.Choices[0].Label != want || out.Current != facts.Candidates[0].Key {
			t.Fatal("actual frozen owner menu mismatch", out, managed, e)
		}
		settings, managed, e := a.readModelSettingsWithHost(ctx, host, settingsFacts)
		if e != nil || !managed {
			t.Fatal("actual Settings owner read failed", managed, e)
		}
		connection, managedConnection, connectionErr := a.readModelConnectionWithHost(ctx, host, connectionFacts)
		wantLogin := "Zulu"
		wantWindow := int64(128000)
		if strings.HasPrefix(want, "v2 ") {
			wantWindow = 96000
			wantLogin = "Alpha"
		}
		if connectionErr != nil || !managedConnection || connection.ContextWindow != wantWindow || connection.LoginModel != wantLogin {
			t.Fatal("actual connection policy mismatch", connection, managedConnection, connectionErr)
		}
		reconnect, managedReconnect, reconnectErr := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Reconnect: &contract.ReconnectFacts{}})
		wantReconnect := "activate"
		if strings.HasPrefix(want, "v2 ") {
			wantReconnect = "probe"
		}
		if reconnectErr != nil || !managedReconnect || reconnect.ReconnectAction != wantReconnect {
			t.Fatal("actual reconnect owner revision mismatch", reconnect, managedReconnect, reconnectErr)
		}
		added, managedAdded, addedErr := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Added: &contract.AddedFacts{}})
		wantAdded := "activate"
		if strings.HasPrefix(want, "v2 ") {
			wantAdded = "save"
		}
		if addedErr != nil || !managedAdded || added.AddedAction != wantAdded {
			t.Fatal("actual added-connection owner revision mismatch", added, managedAdded, addedErr)
		}
		browserFacts := contract.BrowserFacts{Reason: "ack", Previous: []contract.BrowserChoice{{Choice: contract.Choice{Key: "account:old", Label: "Old"}, SortKey: "old"}}, Received: []contract.BrowserChoice{{Choice: contract.Choice{Key: "profile:current:2", Label: "Current"}, SortKey: "current"}}}
		browser, managedBrowser, browserErr := a.readModelMenuWithHost(ctx, host, contract.Facts{Candidates: []contract.Candidate{}, Catalogues: []contract.Catalogue{}, Browser: &browserFacts})
		wantBrowser := 2
		if strings.HasPrefix(want, "v2 ") {
			wantBrowser = 1
		}
		if browserErr != nil || !managedBrowser || browser.Order == nil || len(*browser.Order) != wantBrowser {
			t.Fatal("actual browser menu policy revision mismatch", browser, managedBrowser, browserErr)
		}
		restore, managedRestore, restoreErr := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{AccountBacked: true, Login: &contract.LoginFacts{Models: []string{}, RestoreOwnedCurrent: true}})
		wantRestore := !strings.HasPrefix(want, "v2 ")
		if restoreErr != nil || !managedRestore || restore.RestoreOwnedCurrent == nil || *restore.RestoreOwnedCurrent != wantRestore {
			t.Fatal("actual login restore revision mismatch", restore, managedRestore, restoreErr)
		}
		terminal, managedTerminal, terminalErr := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Preset: contract.TerminalSetup})
		wantEndpoint, wantLimit := "http://127.0.0.1:8000/v1", int64(1048576)
		if strings.HasPrefix(want, "v2 ") {
			wantEndpoint, wantLimit = "http://127.0.0.1:9000/v1", 524288
		}
		if terminalErr != nil || !managedTerminal || terminal.ContextWindow != wantWindow || terminal.EndpointDefault != wantEndpoint || terminal.TokenLimit != wantLimit {
			t.Fatal("actual terminal recipe revision mismatch", terminal, managedTerminal, terminalErr)
		}
		shortcut, e := a.prepareModelShortcutWithHost(ctx, host, llm.ProfileInput{Name: "CLI", ProviderKind: "openai_compatible", Endpoint: "https://cli.example.test/v1", ModelID: "cli-model", AuthConnectionID: "original-auth"})
		wantCLI := int64(8192)
		if strings.HasPrefix(want, "v2 ") {
			wantCLI = 6144
		}
		if e != nil || shortcut.ContextWindow != wantCLI || shortcut.MaxOutputTokens != 1024 || shortcut.Capabilities.ImageInput || !shortcut.Capabilities.TextInput || shortcut.Name != "CLI" || shortcut.ModelID != "cli-model" || shortcut.AuthConnectionID != "original-auth" || shortcut.Endpoint != "https://cli.example.test/v1" {
			t.Fatal("actual shorthand policy or source binding lost", shortcut, e)
		}
		wantStatus := "connected"
		if strings.HasPrefix(want, "v2 ") {
			wantStatus = "v2 connected"
		}
		if settings.Copy == nil || strings.Contains(settings.Copy.Source, "独立版本模型") != strings.HasPrefix(want, "v2 ") {
			t.Fatal("actual owning dictionary version mismatch", settings.Copy)
		}
		if settings.View == nil || strings.Contains(settings.View.Source, "Independent models") != strings.HasPrefix(want, "v2 ") {
			t.Fatal("actual owning template version mismatch", settings.View)
		}
		if len(settings.Rows) != 1 || settings.Rows[0].Ref != "one" || settings.Rows[0].Status != wantStatus || !settings.Rows[0].Open || settings.SuppressNotice {
			t.Fatal("actual frozen owner Settings mismatch", settings, managed, e)
		}
	}
	check("Original")
	binding, e := host.ServiceInstance(ctx, initial.Revision, contract.Settings)
	if e != nil {
		t.Fatal(e)
	}
	for _, resourceFile := range []string{contract.ViewFile, contract.CopyFile} {
		resourcePath := filepath.Join(host.Catalog.Path(binding.PackageDigest), resourceFile)
		resourceBytes, e := os.ReadFile(resourcePath)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Remove(resourcePath); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = os.WriteFile(resourcePath, resourceBytes, 0600) })
		if _, managed, e := a.readModelSettingsWithHost(ctx, host, settingsFacts); e == nil || !managed {
			t.Fatal("missing sealed resource restored native interpretation", managed, e)
		}
		if e = os.WriteFile(resourcePath, resourceBytes, 0600); e != nil {
			t.Fatal(e)
		}
		check("Original")
		recovered, e := host.Store.Status(ctx)
		if e != nil || recovered.Revision != initial.Revision {
			t.Fatal("resource recovery changed selected version", recovered, e)
		}
	}
	// Change actual executable code, with a separately adjusted Owner expectation.
	dir := defaultFeaturePackage(t, "model-connections", nil)
	source, e := filepath.Abs("../plugin/modelconnections/menu.go")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	code := strings.Replace(string(raw), `Version: "1.1.0"`, `Version: "1.2.0"`, 1)
	code = strings.ReplaceAll(code, "Label: label", "Label: \"v2 \"+label")
	code = strings.ReplaceAll(code, "Label: m.Name", "Label: \"v2 \"+m.Name")
	replacement := filepath.Join(t.TempDir(), "menu.go")
	if e = os.WriteFile(replacement, []byte(code), 0600); e != nil {
		t.Fatal(e)
	}
	settingsSource, e := filepath.Abs("../plugin/modelconnections/settings.go")
	if e != nil {
		t.Fatal(e)
	}
	settingsRaw, e := os.ReadFile(settingsSource)
	if e != nil {
		t.Fatal(e)
	}
	viewFile := filepath.Join(dir, "views/models.html")
	viewRaw, e := os.ReadFile(viewFile)
	if e != nil {
		t.Fatal(e)
	}
	variantView := strings.Replace(string(viewRaw), `<h1>{{modelTr "Models"}}</h1>`, `<h1>{{modelTr "Independent models"}}</h1>`, 1)
	variantView = strings.Replace(variantView, `data-model-owner-endpoint-default="http://127.0.0.1:8000/v1"`, `data-model-owner-endpoint-default="http://127.0.0.1:9000/v1"`, 1)
	if variantView == string(viewRaw) {
		t.Fatal("view variant did not change actual resource")
	}
	if e = os.WriteFile(viewFile, []byte(variantView), 0600); e != nil {
		t.Fatal(e)
	}
	copyFile := filepath.Join(dir, contract.CopyFile)
	copyRaw, e := os.ReadFile(copyFile)
	if e != nil {
		t.Fatal(e)
	}
	var translations []contract.Translation
	if e = json.Unmarshal(copyRaw, &translations); e != nil {
		t.Fatal(e)
	}
	for i := range translations {
		if translations[i].Key == "Models" {
			translations[i].Value = "独立版本模型"
		}
	}
	copyRaw, e = json.MarshalIndent(translations, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	copyRaw = append(copyRaw, '\n')
	variantCopy := string(copyRaw)
	if e = os.WriteFile(copyFile, copyRaw, 0600); e != nil {
		t.Fatal(e)
	}
	settingsCode := strings.Replace(string(settingsRaw), "Status: status", `Status: "v2 "+status`, 1)
	settingsCode = strings.Replace(settingsCode, "//go:embed view/models.html\nvar modelViewSource string", "var modelViewSource = "+strconv.Quote(variantView), 1)
	settingsCode = strings.Replace(settingsCode, "//go:embed view/copy.json\nvar modelCopySource string", "var modelCopySource = "+strconv.Quote(variantCopy), 1)
	settingsReplacement := filepath.Join(t.TempDir(), "settings.go")
	if e = os.WriteFile(settingsReplacement, []byte(settingsCode), 0600); e != nil {
		t.Fatal(e)
	}
	connectionSource, e := filepath.Abs("../plugin/modelconnections/connection.go")
	if e != nil {
		t.Fatal(e)
	}
	connectionRaw, e := os.ReadFile(connectionSource)
	if e != nil {
		t.Fatal(e)
	}
	connectionReplacement := filepath.Join(t.TempDir(), "connection.go")
	connectionCode := string(connectionRaw)
	for _, change := range [][2]string{
		{"ContextWindow: 128000", "ContextWindow: 96000"},
		{"f.Login.Models[0]", "f.Login.Models[len(f.Login.Models)-1]"},
		{"out.ContextWindow = 8192", "out.ContextWindow = 6144"},
		{`out.ReconnectAction = "activate"`, `out.ReconnectAction = "probe"`},
		{`out.AddedAction = "activate"`, `out.AddedAction = "save"`},
		{"restore := true", "restore := false"},
		{`out.EndpointDefault = "http://127.0.0.1:8000/v1"`, `out.EndpointDefault = "http://127.0.0.1:9000/v1"`},
		{"out.TokenLimit = 1048576", "out.TokenLimit = 524288"},
	} {
		if strings.Count(connectionCode, change[0]) != 1 {
			t.Fatal("independent connection marker missing or ambiguous", change[0])
		}
		connectionCode = strings.Replace(connectionCode, change[0], change[1], 1)
	}
	if e = os.WriteFile(connectionReplacement, []byte(connectionCode), 0600); e != nil {
		t.Fatal(e)
	}
	browserSource, e := filepath.Abs(filepath.Join("..", "plugin", "modelconnections", "browser.go"))
	if e != nil {
		t.Fatal(e)
	}
	browserRaw, e := os.ReadFile(browserSource)
	if e != nil {
		t.Fatal(e)
	}
	browserReplacement := filepath.Join(t.TempDir(), "browser.go")
	marker := "func projectBrowserMenu(f contract.BrowserFacts) []contract.ChoiceRef {"
	if strings.Count(string(browserRaw), marker) != 1 {
		t.Fatal("browser policy fixture marker missing")
	}
	browserCode := strings.Replace(string(browserRaw), marker, marker+`
 if f.Reason == "ack" { refs:=[]contract.ChoiceRef{};for i:=range f.Received {refs=append(refs,contract.ChoiceRef{Source:"received",Index:i})};return refs }`, 1)
	if e = os.WriteFile(browserReplacement, []byte(browserCode), 0600); e != nil {
		t.Fatal(e)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: replacement, settingsSource: settingsReplacement, connectionSource: connectionReplacement, browserSource: browserReplacement}})
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	if e = os.WriteFile(overlayFile, overlay, 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("go", "build", "-overlay", overlayFile, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/model-connections").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	manifestFile := filepath.Join(dir, "steward.plugin.json")
	raw, e = os.ReadFile(manifestFile)
	if e != nil {
		t.Fatal(e)
	}
	var manifest rsiplugin.Manifest
	if e = rsiplugin.Decode(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	manifest.Version = "1.2.0"
	manifest, e = rsi.Seal(dir, manifest)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(manifest)
	if e = os.WriteFile(manifestFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	installed, e := host.Install(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	graph, e := host.Store.Graph(ctx, initial.Revision)
	if e != nil {
		t.Fatal(e)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "model-connections" {
			graph.Graph.Bindings[i].PackageDigest = installed.Digest
		}
	}
	next, e := host.Prepare(ctx, graph.Graph)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(originalPolicy)
	var variant rsi.Policy
	if e = json.Unmarshal(encoded, &variant); e != nil {
		t.Fatal(e)
	}
	variant.ID = "model-menu-v2"
	for i := range variant.Cases {
		if variant.Cases[i].Contract == contract.ConnectionPolicy {
			var out contract.ConnectionDefaults
			if e = json.Unmarshal(variant.Cases[i].Expected, &out); e != nil {
				t.Fatal(e)
			}
			var facts contract.ConnectionFacts
			if e = json.Unmarshal(variant.Cases[i].Input, &facts); e != nil {
				t.Fatal(e)
			}
			out.ContextWindow = 96000
			if facts.Preset == contract.TerminalSetup {
				out.EndpointDefault = "http://127.0.0.1:9000/v1"
				out.TokenLimit = 524288
			}
			if facts.Login != nil && facts.Login.RestoreOwnedCurrent {
				restore := false
				out.RestoreOwnedCurrent = &restore
			}
			if facts.Added != nil && !facts.Added.HasCurrent {
				out.AddedAction = "save"
			}
			if facts.Reconnect != nil {
				out.ReconnectAction = "probe"
			}
			if facts.Preset == contract.CLIShorthand {
				out.ContextWindow = 6144
			}
			if out.LoginModel != "" {
				out.LoginModel = "Alpha"
			}
			variant.Cases[i].Expected, _ = json.Marshal(out)
		}
		if variant.Cases[i].Contract == contract.Settings {
			var out contract.SettingsProjection
			if e = json.Unmarshal(variant.Cases[i].Expected, &out); e != nil {
				t.Fatal(e)
			}
			if out.Copy != nil {
				out.Copy.Source = variantCopy
				out.Copy.Digest = rsiplugin.FileDigest([]byte(variantCopy))
			}
			if out.View != nil {
				out.View.Source = variantView
				out.View.Digest = rsiplugin.FileDigest([]byte(variantView))
			}
			for j := range out.Rows {
				out.Rows[j].Status = "v2 " + out.Rows[j].Status
			}
			variant.Cases[i].Expected, _ = json.Marshal(out)
		}
		if variant.Cases[i].Contract == contract.Menu {
			var out contract.Projection
			if e = json.Unmarshal(variant.Cases[i].Expected, &out); e != nil {
				t.Fatal(e)
			}
			var facts contract.Facts
			if e = json.Unmarshal(variant.Cases[i].Input, &facts); e != nil {
				t.Fatal(e)
			}
			if facts.Browser != nil && facts.Browser.Reason == "ack" {
				refs := []contract.ChoiceRef{}
				for index := range facts.Browser.Received {
					refs = append(refs, contract.ChoiceRef{Source: "received", Index: index})
				}
				out.Order = &refs
			}
			for j := range out.Choices {
				out.Choices[j].Label = "v2 " + out.Choices[j].Label
			}
			variant.Cases[i].Expected, _ = json.Marshal(out)
		}
	}
	if e = host.Store.PutPolicy(ctx, variant); e != nil {
		t.Fatal(e)
	}
	selectGraph := func(id, from, to, policy string) {
		t.Helper()
		if e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: from, Target: to, PolicyID: policy, Goal: "independent model-menu display"}); e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: from, Target: to, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	selectGraph("model-menu-upgrade", initial.Revision, next.Revision, variant.ID)
	check("v2 Original")
	selectGraph("model-menu-rollback", next.Revision, initial.Revision, originalPolicy.ID)
	check("Original")
	if _, e = host.Store.Deactivate(ctx, "model-menu-disable", initial.Revision); e != nil {
		t.Fatal(e)
	}
	if _, managed, e := a.readModelMenuWithHost(ctx, host, contract.Facts{Candidates: []contract.Candidate{}, Catalogues: []contract.Catalogue{}, Browser: &contract.BrowserFacts{Reason: "ack"}}); e == nil || !managed {
		t.Fatal("deactivation restored native browser policy", managed, e)
	}
	if _, managed, e := a.readModelMenuWithHost(ctx, host, facts); e == nil || !managed {
		t.Fatal("deactivation restored native menu", managed, e)
	}
	if _, managed, e := a.readModelSettingsWithHost(ctx, host, settingsFacts); e == nil || !managed {
		t.Fatal("deactivation restored native Settings", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Reconnect: &contract.ReconnectFacts{}}); e == nil || !managed {
		t.Fatal("deactivation restored native reconnect policy", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Added: &contract.AddedFacts{}}); e == nil || !managed {
		t.Fatal("deactivation restored native added-connection policy", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{AccountBacked: true, Login: &contract.LoginFacts{Models: []string{}, RestoreOwnedCurrent: true}}); e == nil || !managed {
		t.Fatal("deactivation restored native login restore", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Preset: contract.TerminalSetup}); e == nil || !managed {
		t.Fatal("deactivation restored native terminal recipe", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, connectionFacts); e == nil || !managed {
		t.Fatal("deactivation restored native connection policy", managed, e)
	}
	if _, e := a.prepareModelShortcutWithHost(ctx, host, llm.ProfileInput{Name: "CLI", ProviderKind: "openai_compatible", Endpoint: "https://cli.example.test/v1", ModelID: "cli-model"}); e == nil {
		t.Fatal("deactivation restored native shorthand")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a, e = OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if e != nil {
		t.Fatal(e)
	}
	host = a.Plugins()
	host.Catalog.Launcher = carrier
	if _, managed, e := a.readModelMenuWithHost(ctx, host, contract.Facts{Candidates: []contract.Candidate{}, Catalogues: []contract.Catalogue{}, Browser: &contract.BrowserFacts{Reason: "ack"}}); e == nil || !managed {
		t.Fatal("deactivation restored native browser policy", managed, e)
	}
	if _, managed, e := a.readModelMenuWithHost(ctx, host, facts); e == nil || !managed {
		t.Fatal("cold deactivation restored native menu", managed, e)
	}
	if _, managed, e := a.readModelSettingsWithHost(ctx, host, settingsFacts); e == nil || !managed {
		t.Fatal("cold deactivation restored native Settings", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Reconnect: &contract.ReconnectFacts{}}); e == nil || !managed {
		t.Fatal("deactivation restored native reconnect policy", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Added: &contract.AddedFacts{}}); e == nil || !managed {
		t.Fatal("deactivation restored native added-connection policy", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{AccountBacked: true, Login: &contract.LoginFacts{Models: []string{}, RestoreOwnedCurrent: true}}); e == nil || !managed {
		t.Fatal("deactivation restored native login restore", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, contract.ConnectionFacts{Preset: contract.TerminalSetup}); e == nil || !managed {
		t.Fatal("deactivation restored native terminal recipe", managed, e)
	}
	if _, managed, e := a.readModelConnectionWithHost(ctx, host, connectionFacts); e == nil || !managed {
		t.Fatal("cold deactivation restored native connection policy", managed, e)
	}
	if _, e := a.prepareModelShortcutWithHost(ctx, host, llm.ProfileInput{Name: "CLI", ProviderKind: "openai_compatible", Endpoint: "https://cli.example.test/v1", ModelID: "cli-model"}); e == nil {
		t.Fatal("cold deactivation restored native shorthand")
	}
}
