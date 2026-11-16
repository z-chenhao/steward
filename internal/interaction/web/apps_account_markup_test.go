package web

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	xhtml "golang.org/x/net/html"
)

func TestAppAccountMarkupKeepsPrivateObjectsOutAndEscapesDisplayValues(t *testing.T) {
	raw, err := os.ReadFile("../../../plugins/apps-interface/views/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.AccountMarkupSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileAppAccountMarkup(source.Account)
	if err != nil {
		t.Fatal(err)
	}
	data := appAccountMarkupData{Name: `<script>PRIVATE()</script>`, Alias: `work & home`, Status: `pending" onclick="PRIVATE()`, StatusLabel: `Pending <review>`}
	result, err := renderAppAccountMarkup(compiled, "copy", data)
	if err != nil {
		t.Fatal(err)
	}
	// Original account markup includes outer line separators; inspect its DOM root.
	nodes, err := markupNodes(strings.TrimSpace(string(result)))
	if err != nil || len(nodes) != 1 {
		t.Fatal("account display lost its original root", err)
	}
	root := nodes[0]
	status := markupOne(root, func(n *xhtml.Node) bool { return markupHas(n, "data-connection-status") })
	if status == nil || markupAttr(status, "data-connection-status") != data.Status || markupHas(status, "onclick") {
		t.Fatal("display value escaped its original attribute")
	}
	if strings.Contains(string(result), "<script>") || !strings.Contains(string(result), "&lt;script&gt;") || !strings.Contains(string(result), "&amp;") {
		t.Fatal("display values became executable markup", result)
	}
	for _, expression := range []string{"{{.Apps.State}}", "{{.Connection.ID}}", "{{.Name.String}}", "{{printf \"%s\" .Name}}", "{{range .Actions}}{{.}}{{end}}"} {
		invalid := strings.Replace(source.Account, "{{.Name}}", expression, 1)
		if _, err := parseAppAccountMarkup(invalid); err == nil {
			t.Fatal("account source acquired an internal object or execution primitive", expression)
		}
	}
	if _, err := renderAppAccountMarkup(compiled, "configure", data); err == nil {
		t.Fatal("unknown section acquired a render entry point")
	}
	for name, change := range map[string][2]string{
		"script":              {"<strong>", "<script>PRIVATE()</script><strong>"},
		"external-post":       {"action=\"/apps/configure\"", "action=\"https://example.invalid\""},
		"get-action":          {"method=\"post\"", "method=\"get\""},
		"lost-identity":       {"{{.Identity}}", ""},
		"duplicated-identity": {"{{.Identity}}", "{{.Identity}}{{.Identity}}"},
		"lost-authorization":  {"{{.Authorization}}", ""},
		"lost-warning":        {"{{.Warning}}", ""},
		"changed-control":     {"value=\"{{.Action}}\"", "value=\"disconnect\""},
		"hidden-control":      {"class=\"secondary\"", "class=\"secondary\" hidden"},
		"event-handler":       {"<strong>", "<strong onclick=\"PRIVATE()\">"},
		"additional-control":  {"{{.Buttons}}", "{{.Buttons}}<button name=\"action\" value=\"disconnect\">Disconnect</button>"},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := strings.Replace(source.Account, change[0], change[1], 1)
			if invalid == source.Account {
				t.Fatal("fixture did not alter source")
			}
			if _, err := compileAppAccountMarkup(invalid); err == nil {
				t.Fatal("candidate changed trusted account controls")
			}
		})
	}
}

func TestAppAccountMarkupActualTemplateRetainsOriginalControlsAndNeverFallsBack(t *testing.T) {
	raw, err := os.ReadFile("../../../plugins/apps-interface/views/markup.json")
	if err != nil {
		t.Fatal(err)
	}
	var source contract.AccountMarkupSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	compiled, err := compileAppAccountMarkup(source.Account)
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(raw string) string {
		nodes, err := markupNodes(raw)
		if err != nil {
			t.Fatal(err)
		}
		var clean func(*xhtml.Node)
		clean = func(n *xhtml.Node) {
			for c := n.FirstChild; c != nil; {
				next := c.NextSibling
				if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) == "" {
					n.RemoveChild(c)
				} else {
					clean(c)
				}
				c = next
			}
		}
		var out strings.Builder
		for _, n := range nodes {
			clean(n)
			out.WriteString(markupSerialize(n))
		}
		return strings.TrimSpace(out.String())
	}
	for flags := 0; flags < 8; flags++ {
		for _, language := range []string{"en", "zh-CN"} {
			host := newLayout(language)
			policy := AppInterfacePolicy{revision: "original_graph", accountManaged: true, accountMarkup: compiled}
			view := &appsView{Interface: &policy, ConnectURL: "https://connect.composio.dev/original?private=link", ExpiresAt: 123456}
			row := appAccountContextView{appAccountView: appAccountView{Connection: connectedapps.Connection{ID: `original<&"account`, Name: "Original <App>", Alias: "工作 & 个人", Status: "pending"}, UI: contract.Account{StatusLabel: "Pending", Warning: flags&1 != 0, Pending: flags&2 != 0, AuthorizationLink: flags&4 != 0, Actions: []string{"reconnect", "check", "disconnect"}}}, Apps: view}
			for _, part := range []string{"copy", "actions"} {
				actual, err := appAccountPart(host, row, part, language)
				if err != nil {
					t.Fatal(flags, language, part, err)
				}
				var legacy bytes.Buffer
				if err := host.ExecuteTemplate(&legacy, "app-account-"+part+"-legacy-v1", row); err != nil {
					t.Fatal(err)
				}
				if canonical(string(actual)) != canonical(legacy.String()) {
					t.Fatalf("original account controls changed flags=%d language=%s part=%s\nnew=%s\nold=%s", flags, language, part, actual, legacy.String())
				}
			}
			policy.accountMarkup = nil
			if out, err := appAccountPart(host, row, "copy", language); err == nil || out != "" {
				t.Fatal("managed missing source used original native controls")
			}
		}
	}
}
