package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func accountIntentPackage(t *testing.T, version string) string {
	t.Helper()
	if version == "1.8.0" {
		return defaultFeaturePackage(t, "apps-accounts", nil)
	}
	directory := t.TempDir()
	path, err := filepath.Abs("../plugin/appsaccounts/intent.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/intent.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
	code = strings.ReplaceAll(code, `"personal"`, `"primary"`)
	code = strings.ReplaceAll(code, `"personal_"`, `"primary_"`)
	marker := "for i := len(rows) - 1; i >= 0; i-- {"
	if !strings.Contains(code, marker) {
		t.Fatal("pending policy marker changed")
	}
	code = strings.Replace(code, marker, "for i:=0;i<len(rows);i++ {", 1)
	if version == "2.0.0" {
		marker = "return rsiplugin.JSONResult(ctx, chooseConnect(rows, request.Alias))"
		if !strings.Contains(code, marker) {
			t.Fatal("intent output marker changed")
		}
		code = strings.Replace(code, marker, `plan:=chooseConnect(rows,request.Alias);if request.Count==7 {value:=request.Count;plan=contract.ConnectPlan{Kind:"pending",Row:&value}};if request.Count==8 {plan=contract.ConnectPlan{Kind:"new",Alias:"rewritten"}};if request.Count==9 {plan=contract.ConnectPlan{Kind:"new",Alias:"work"}};return rsiplugin.JSONResult(ctx,plan)`, 1)
	}
	override := filepath.Join(t.TempDir(), "intent.go")
	if err := os.WriteFile(override, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if version != "1.1.0" && version != "2.0.0" {
		code = strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "`+version+`"`, 1)
		if err := os.WriteFile(override, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
	}
	authPath, err := filepath.Abs("../plugin/appsaccounts/auth_config.go")
	if err != nil {
		t.Fatal(err)
	}
	authRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/auth_config.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	authCode := string(authRaw)
	if version == "1.2.0" {
		authCode = strings.ReplaceAll(authCode, "Limit: 200", "Limit: 100")
		authCode = strings.ReplaceAll(authCode, "row.Created > rows[*chosen].Created", "row.Created < rows[*chosen].Created")
		authCode = strings.ReplaceAll(authCode, "row.ID > rows[*chosen].ID", "row.ID < rows[*chosen].ID")
	}
	if version == "2.0.0" {
		authCode = strings.Replace(authCode, "plan := chooseAuthConfig(request, rows)", `plan := chooseAuthConfig(request, rows);if request.Step=="begin"&&request.Explicit&&request.Saved {plan=contract.AuthPlan{Kind:"fetch",Source:"saved"}};if request.Count==7 {value:=request.Count;plan=contract.AuthPlan{Kind:"done",Row:&value}};if request.Count==8 {value:=0;plan=contract.AuthPlan{Kind:"done",Row:&value}};if request.Count==9 {plan=contract.AuthPlan{Kind:"fetch",Source:"auto",Limit:200}}`, 1)
	}
	authOverride := filepath.Join(t.TempDir(), "auth_config.go")
	if err := os.WriteFile(authOverride, []byte(authCode), 0600); err != nil {
		t.Fatal(err)
	}
	flowPath, err := filepath.Abs("../plugin/appsaccounts/session_flow.go")
	if err != nil {
		t.Fatal(err)
	}
	flowRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/session_flow.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	flowCode := string(flowRaw)
	if version == "1.3.0" {
		flowCode = strings.Replace(flowCode, "TTLSeconds: 600", "TTLSeconds: 300", 1)
	}
	bad := ""
	switch version {
	case "3.0.0":
		bad = `if call.OperationID=="owner-read"&&request.Step=="toolkit"&&!request.NoAuth {plan=contract.SessionPlan{Kind:"public"}}`
	case "3.1.0":
		bad = `if call.OperationID=="owner-read"&&request.Step=="session" {plan=contract.SessionPlan{Kind:"session"}}`
	case "3.2.0":
		bad = `if call.OperationID=="owner-read"&&request.Step=="link" {plan=contract.SessionPlan{Kind:"pending",TTLSeconds:601}}`
	case "3.3.0":
		bad = `if call.OperationID=="owner-read"&&request.Step=="toolkit"&&!request.NoAuth {plan=contract.SessionPlan{Kind:"session"}}`
	}
	if bad != "" {
		flowCode = strings.Replace(flowCode, "return rsiplugin.JSONResult(ctx, chooseSessionFlow(request))", "plan:=chooseSessionFlow(request);"+bad+";return rsiplugin.JSONResult(ctx,plan)", 1)
	}
	flowOverride := filepath.Join(t.TempDir(), "session_flow.go")
	if err := os.WriteFile(flowOverride, []byte(flowCode), 0600); err != nil {
		t.Fatal(err)
	}

	checkPath, err := filepath.Abs("../plugin/appsaccounts/account_check.go")
	if err != nil {
		t.Fatal(err)
	}
	checkRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/account_check.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	checkCode := string(checkRaw)
	if version == "1.4.0" {
		checkCode = strings.Replace(checkCode, "request.Initializing || request.Initiated", "request.Initializing", 1)
	}
	badCheck := ""
	switch version {
	case "4.0.0":
		badCheck = `if call.OperationID=="owner-read"&&request.Disabled{plan=contract.AccountCheckPlan{Status:"active"}}`
	case "4.1.0":
		badCheck = `if call.OperationID=="owner-read"&&!request.Public&&!request.Disabled&&!request.Active&&!request.Initializing&&!request.Initiated{plan=contract.AccountCheckPlan{Status:"pending"}}`
	case "4.2.0":
		badCheck = `if call.OperationID=="owner-read"&&!request.Public{plan=contract.AccountCheckPlan{Status:"refresh"}}`
	case "4.3.0":
		badCheck = `if call.OperationID=="owner-read"&&request.Public{plan=contract.AccountCheckPlan{Status:"active"}}`
	}
	if badCheck != "" {
		checkCode = strings.Replace(checkCode, "return rsiplugin.JSONResult(ctx, chooseAccountCheck(request))", "plan:=chooseAccountCheck(request);"+badCheck+";return rsiplugin.JSONResult(ctx,plan)", 1)
	}
	checkOverride := filepath.Join(t.TempDir(), "account_check.go")
	if err := os.WriteFile(checkOverride, []byte(checkCode), 0600); err != nil {
		t.Fatal(err)
	}

	closePath, err := filepath.Abs("../plugin/appsaccounts/account_close.go")
	if err != nil {
		t.Fatal(err)
	}
	closeRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/account_close.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	closeCode := string(closeRaw)
	if version == "1.5.0" {
		closeCode = strings.Replace(closeCode, `case "revoked":`, `case "revoked":if request.Revoked{return contract.AccountClosePlan{Kind:"done"}}`, 1)
	}
	badClose := ""
	switch version {
	case "5.0.0":
		badClose = `if call.OperationID=="owner-read"&&request.Step=="closed" {plan=contract.AccountClosePlan{Kind:"revoke"}}`
	case "5.1.0":
		badClose = `if call.OperationID=="owner-read"&&request.Step=="account"&&!request.Matched {plan=contract.AccountClosePlan{Kind:"revoke"}}`
	case "5.2.0":
		badClose = `if call.OperationID=="owner-read"&&request.Step=="revoked" {plan=contract.AccountClosePlan{Kind:"revoke"}}`
	case "5.3.0":
		badClose = `if call.OperationID=="owner-read"&&request.Step=="account"&&request.Matched {plan=contract.AccountClosePlan{Kind:"delete"}}`
	case "5.4.0":
		badClose = `if call.OperationID=="owner-read"&&request.Step=="deleted" {plan=contract.AccountClosePlan{Kind:"delete"}}`
	}
	if badClose != "" {
		closeCode = strings.Replace(closeCode, "return rsiplugin.JSONResult(ctx, chooseAccountClose(request))", "plan:=chooseAccountClose(request);"+badClose+";return rsiplugin.JSONResult(ctx,plan)", 1)
	}
	closeOverride := filepath.Join(t.TempDir(), "account_close.go")
	if err := os.WriteFile(closeOverride, []byte(closeCode), 0600); err != nil {
		t.Fatal(err)
	}

	reconnectPath, err := filepath.Abs("../plugin/appsaccounts/reconnect.go")
	if err != nil {
		t.Fatal(err)
	}
	reconnectRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/reconnect.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	reconnectCode := string(reconnectRaw)
	if version == "1.6.0" {
		reconnectCode = strings.Replace(reconnectCode, `case "begin":`, `case "begin":if !request.Pending{return contract.ReconnectPlan{Kind:"deny"}}`, 1)
	}
	badReconnect := ""
	switch version {
	case "6.0.0":
		badReconnect = `if call.OperationID=="owner-read"&&request.Step=="begin" {return rsiplugin.JSONResult(ctx,contract.ReconnectPlan{Kind:"connect"})}`
	case "6.1.0":
		badReconnect = `if call.OperationID=="owner-read"&&request.Step=="closed" {return rsiplugin.JSONResult(ctx,contract.ReconnectPlan{Kind:"close"})}`
	case "6.2.0":
		badReconnect = `if call.OperationID=="owner-read"&&request.Step=="closed" {return rsiplugin.JSONResult(ctx,map[string]string{"kind":"connect","alias":"other"})}`
	case "6.3.0":
		badReconnect = `if call.OperationID=="owner-read"&&request.Step=="closed" {return rsiplugin.JSONResult(ctx,contract.ReconnectPlan{Kind:"deny"})}`
	}
	if badReconnect != "" {
		reconnectCode = strings.Replace(reconnectCode, "return rsiplugin.JSONResult(ctx, chooseReconnect(request))", badReconnect+";return rsiplugin.JSONResult(ctx,chooseReconnect(request))", 1)
	}
	reconnectOverride := filepath.Join(t.TempDir(), "reconnect.go")
	if err := os.WriteFile(reconnectOverride, []byte(reconnectCode), 0600); err != nil {
		t.Fatal(err)
	}

	settingsPath, err := filepath.Abs("../plugin/appsaccounts/settings.go")
	if err != nil {
		t.Fatal(err)
	}
	settingsRaw, err := os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/settings.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	settingsCode := string(settingsRaw)
	if version == "1.7.0" {
		settingsCode = strings.Replace(settingsCode, "apps[indices[i]] < apps[indices[j]]", "apps[indices[i]] > apps[indices[j]]", 1)
		settingsCode = strings.Replace(settingsCode, `if request.Mode == "add" || len(indices) == 0 {`, `if request.Mode=="add" {return append([]int{-1},indices...)};if len(indices)==0 {`, 1)
	}
	badSettings := ""
	switch version {
	case "7.0.0":
		badSettings = `if call.OperationID=="owner-read"&&request.Mode=="saved"&&request.Count==2{indices=[]int{0,0}}`
	case "7.1.0":
		badSettings = `if call.OperationID=="owner-read"&&request.Mode=="saved"&&request.Count==2{indices=[]int{0,request.Count}}`
	case "7.2.0":
		badSettings = `if call.OperationID=="owner-read"&&request.Mode=="add"{indices=[]int{-1}}`
	case "7.3.0":
		badSettings = `if call.OperationID=="owner-read"&&request.Mode=="remove"&&request.Count==2{indices=[]int{request.Remove}}`
	case "7.4.0":
		badSettings = `if call.OperationID=="owner-read"&&request.Mode=="saved"&&request.Offset>=contract.SettingsPageSize{indices[request.Offset]=indices[0]}`
	}
	if badSettings != "" {
		settingsCode = strings.Replace(settingsCode, "indices := chooseSettings(request, apps)", "indices:=chooseSettings(request,apps);"+badSettings, 1)
	}
	settingsOverride := filepath.Join(t.TempDir(), "settings.go")
	if err := os.WriteFile(settingsOverride, []byte(settingsCode), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: override, authPath: authOverride, flowPath: flowOverride, checkPath: checkOverride, closePath: closeOverride, reconnectPath: reconnectOverride, settingsPath: settingsOverride}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/apps-accounts").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-accounts-pre-inventory-v1/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func activateAccountIntent(t *testing.T, ctx context.Context, host rsi.Host, baseline, version string) (string, string) {
	t.Helper()
	pkg, err := host.Install(ctx, accountIntentPackage(t, version))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "apps-accounts", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "apps-accounts", StateVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := func(i int) *int { return &i }
	defaultAlias := "personal"
	pending := 2
	if version == "1.1.0" || version == "2.0.0" {
		defaultAlias = "primary"
		pending = 0
	}
	policy := rsi.Policy{ID: "accounts-owner-" + version, AllowedContracts: []string{contract.ConnectIntent, contract.AuthConfig, contract.SessionFlow, contract.AccountCheck, contract.AccountClose, contract.Reconnect, contract.Settings}, AllowedRequests: []string{"input.read"}}
	for _, fixture := range []struct {
		rows  []contract.ConnectRow
		alias string
		plan  contract.ConnectPlan
	}{
		{plan: contract.ConnectPlan{Kind: "new", Alias: defaultAlias}},
		{rows: []contract.ConnectRow{{Alias: "work", Facts: contract.Active}}, plan: contract.ConnectPlan{Kind: "alias_required"}},
		{rows: []contract.ConnectRow{{Alias: "old", Facts: contract.Pending}, {Alias: "middle", Facts: contract.Pending}, {Alias: "new", Facts: contract.Pending}}, plan: contract.ConnectPlan{Kind: "pending", Row: ref(pending)}},
		{rows: []contract.ConnectRow{{Alias: "work", Facts: contract.Active}}, alias: "work", plan: contract.ConnectPlan{Kind: "conflict"}},
		{rows: []contract.ConnectRow{{Alias: "work", Facts: contract.Pending}}, alias: "work", plan: contract.ConnectPlan{Kind: "pending", Row: ref(0)}},
		{rows: []contract.ConnectRow{{Alias: "work", Facts: contract.Disconnected}}, alias: "work", plan: contract.ConnectPlan{Kind: "new", Alias: "work"}},
		{rows: []contract.ConnectRow{{Alias: "public", Facts: contract.Active | contract.Public}}, plan: contract.ConnectPlan{Kind: "new", Alias: defaultAlias}},
	} {
		input, _ := json.Marshal(contract.ConnectRequest{Count: len(fixture.rows), Alias: fixture.alias})
		output, _ := json.Marshal(fixture.plan)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.ConnectIntent, Input: input, Inputs: []rsi.InputData{{ID: contract.ConnectInputID, Data: contract.EncodeConnectRows(fixture.rows)}}, Expected: output})
	}
	authLimit := 200
	newest := 1
	if version == "1.2.0" {
		authLimit = 100
		newest = 0
	}
	baseFacts := contract.AuthToolkit | contract.AuthEnabled | contract.AuthRouter | contract.AuthUnmanaged | contract.AuthValidID
	choices := []contract.AuthRow{{ID: "ac_a", Created: "2025", Facts: baseFacts}, {ID: "ac_z", Created: "2026", Facts: baseFacts}, {ID: "ac_bad", Created: "9999"}}
	for _, fixture := range []struct {
		request contract.AuthRequest
		rows    []contract.AuthRow
		plan    contract.AuthPlan
	}{
		{request: contract.AuthRequest{Step: "begin"}, plan: contract.AuthPlan{Kind: "fetch", Source: "auto", Limit: authLimit}},
		{request: contract.AuthRequest{Step: "begin", Explicit: true}, plan: contract.AuthPlan{Kind: "fetch", Source: "explicit"}},
		{request: contract.AuthRequest{Step: "begin", Saved: true}, plan: contract.AuthPlan{Kind: "fetch", Source: "saved"}},
		{request: contract.AuthRequest{Step: "result", Page: 1}, rows: choices, plan: contract.AuthPlan{Kind: "done", Row: ref(newest)}},
		{request: contract.AuthRequest{Step: "result", Page: 1, HasNext: true}, rows: choices, plan: contract.AuthPlan{Kind: "next", Limit: authLimit, Row: ref(newest)}},
		{request: contract.AuthRequest{Step: "result", Override: true}, rows: []contract.AuthRow{{ID: "ac_explicit", Facts: baseFacts | contract.AuthExplicitMatch}}, plan: contract.AuthPlan{Kind: "done", Row: ref(0)}},
		{request: contract.AuthRequest{Step: "result", Override: true}, rows: []contract.AuthRow{{ID: "ac_bad"}}, plan: contract.AuthPlan{Kind: "reject"}},
		{request: contract.AuthRequest{Step: "error", Status: 401}, plan: contract.AuthPlan{Kind: "unreadable"}},
		{request: contract.AuthRequest{Step: "error", Status: 403}, plan: contract.AuthPlan{Kind: "unreadable"}},
		{request: contract.AuthRequest{Step: "error", Status: 500}, plan: contract.AuthPlan{Kind: "reject"}},
		{request: contract.AuthRequest{Step: "error", Status: 403, Override: true}, plan: contract.AuthPlan{Kind: "reject"}},
		{request: contract.AuthRequest{Step: "result", NoHosted: true}, plan: contract.AuthPlan{Kind: "requires_config"}},
		{request: contract.AuthRequest{Step: "result", Page: 20, HasNext: true}, rows: choices, plan: contract.AuthPlan{Kind: "reject"}},
		{request: contract.AuthRequest{Step: "result", Page: 2, HasNext: true, CursorSeen: true}, rows: choices, plan: contract.AuthPlan{Kind: "reject"}},
	} {
		fixture.request.Count = len(fixture.rows)
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(fixture.plan)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.AuthConfig, Input: input, Inputs: []rsi.InputData{{ID: contract.AuthConfigInputID, Data: contract.EncodeAuthRows(fixture.rows)}}, Expected: output})
	}
	ttl := 600
	if version == "1.3.0" {
		ttl = 300
	}
	for _, fixture := range []struct {
		request contract.SessionRequest
		plan    contract.SessionPlan
	}{
		{contract.SessionRequest{Step: "begin"}, contract.SessionPlan{Kind: "toolkit"}},
		{contract.SessionRequest{Step: "toolkit"}, contract.SessionPlan{Kind: "auth"}},
		{contract.SessionRequest{Step: "toolkit", NoAuth: true}, contract.SessionPlan{Kind: "public"}},
		{contract.SessionRequest{Step: "auth"}, contract.SessionPlan{Kind: "session"}},
		{contract.SessionRequest{Step: "session"}, contract.SessionPlan{Kind: "link"}},
		{contract.SessionRequest{Step: "link"}, contract.SessionPlan{Kind: "pending", TTLSeconds: ttl}},
	} {
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(fixture.plan)
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.SessionFlow, Input: input, Expected: output})
	}

	initiated := "pending"
	if version == "1.4.0" {
		initiated = "expired"
	}
	for _, fixture := range []struct {
		request contract.AccountCheckRequest
		status  string
	}{
		{contract.AccountCheckRequest{}, "expired"},
		{contract.AccountCheckRequest{Public: true}, "refresh"},
		{contract.AccountCheckRequest{Active: true}, "active"},
		{contract.AccountCheckRequest{Active: true, Disabled: true}, "expired"},
		{contract.AccountCheckRequest{Initializing: true}, "pending"},
		{contract.AccountCheckRequest{Initiated: true}, initiated},
		{contract.AccountCheckRequest{Initializing: true, Disabled: true}, "expired"},
	} {
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(contract.AccountCheckPlan{Status: fixture.status})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.AccountCheck, Input: input, Expected: output})
	}

	afterProved := "delete"
	if version == "1.5.0" {
		afterProved = "done"
	}
	for _, fixture := range []struct {
		request contract.AccountCloseRequest
		kind    string
	}{
		{contract.AccountCloseRequest{Step: "closed"}, "account"},
		{contract.AccountCloseRequest{Step: "account", Matched: true}, "revoke"},
		{contract.AccountCloseRequest{Step: "account"}, "done"},
		{contract.AccountCloseRequest{Step: "account", Failed: true}, "done"},
		{contract.AccountCloseRequest{Step: "revoked", Matched: true, Revoked: true}, afterProved},
		{contract.AccountCloseRequest{Step: "revoked", Matched: true, Failed: true}, "delete"},
		{contract.AccountCloseRequest{Step: "deleted", Matched: true, Revoked: true}, "done"},
	} {
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(contract.AccountClosePlan{Kind: fixture.kind})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.AccountClose, Input: input, Expected: output})
	}

	nonPending := "close"
	if version == "1.6.0" {
		nonPending = "deny"
	}
	for _, fixture := range []struct {
		request contract.ReconnectRequest
		kind    string
	}{
		{contract.ReconnectRequest{Step: "begin", Pending: true}, "close"},
		{contract.ReconnectRequest{Step: "begin"}, nonPending},
		{contract.ReconnectRequest{Step: "closed", Pending: true}, "connect"},
	} {
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(contract.ReconnectPlan{Kind: fixture.kind})
		policy.Cases = append(policy.Cases, rsi.Case{InstanceID: "apps-accounts", Contract: contract.Reconnect, Input: input, Expected: output})
	}

	sorted := []int{1, 0}
	added := []int{0, 1, -1}
	if version == "1.7.0" {
		sorted = []int{0, 1}
		added = []int{-1, 0, 1}
	}
	for _, fixture := range []struct {
		request contract.SettingsRequest
		apps    []string
		indices []int
	}{
		{contract.SettingsRequest{Mode: "saved", Count: 2, Remove: -1}, []string{"b", "a"}, sorted},
		{contract.SettingsRequest{Mode: "saved", Remove: -1}, []string{}, []int{-1}},
		{contract.SettingsRequest{Mode: "keep", Count: 2, Remove: -1}, nil, []int{0, 1}},
		{contract.SettingsRequest{Mode: "add", Count: 2, Remove: -1}, nil, added},
		{contract.SettingsRequest{Mode: "remove", Count: 2, Remove: 0}, nil, []int{1}},
		{contract.SettingsRequest{Mode: "remove", Count: 1, Remove: 0}, nil, []int{-1}},
	} {
		input, _ := json.Marshal(fixture.request)
		output, _ := json.Marshal(contract.SettingsPlan{Indices: fixture.indices})
		test := rsi.Case{InstanceID: "apps-accounts", Contract: contract.Settings, Input: input, Expected: output}
		if fixture.request.Mode == "saved" {
			test.Inputs = []rsi.InputData{{ID: contract.SettingsInputID, Data: contract.EncodeSettingsApps(fixture.apps)}}
		}
		policy.Cases = append(policy.Cases, test)
	}
	if err := host.Store.PutPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	id := "accounts-" + version
	if err := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: graph.Revision, PolicyID: policy.ID, Goal: "Independently evolve account intent without connection authority"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: graph.Revision, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	return graph.Revision, pkg.Digest
}

func retainedAccountPackage(t *testing.T, name string) string {
	t.Helper()
	directory := t.TempDir()
	path, err := filepath.Abs("../plugin/appsaccounts/intent.go")
	if err != nil {
		t.Fatal(err)
	}
	oldPath, err := filepath.Abs(filepath.Join("../../tests/fixtures/plugins", name, "intent.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("../../tests/fixtures/plugins", name, "origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var origin struct{ Files map[string]string }
	if err := json.Unmarshal(raw, &origin); err != nil {
		t.Fatal(err)
	}
	oldRaw, err := os.ReadFile(oldPath)
	if err != nil || rsiplugin.FileDigest(oldRaw) != "sha256:"+origin.Files["intent.go.txt"] {
		t.Fatal("retained original source changed", err)
	}
	overlay, _ := json.Marshal(struct{ Replace map[string]string }{map[string]string{path: oldPath}})
	file := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(file, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-overlay", file, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/apps-accounts").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	raw, err = os.ReadFile(filepath.Join("../../tests/fixtures/plugins", name, "steward.plugin.json"))
	if err != nil || rsiplugin.FileDigest(raw) != "sha256:"+origin.Files["steward.plugin.json"] {
		t.Fatal("retained manifest changed", err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}
