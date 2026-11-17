package web

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
)

func originalWorkbenchPresentation(t *testing.T) contract.Presentation {
	t.Helper()
	raw, err := os.ReadFile("../../rsi/builtin/web-workbench-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct{ Expected contract.Presentation }
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle.Expected
}

// The baseline uses the captured native templates and dictionary, not owner
// assets or the current mutable templates/localization implementation.
func capturedNativeWorkbenchLayout(t *testing.T, r *http.Request) *template.Template {
	t.Helper()
	base := "../../../tests/fixtures/plugins/web-workbench-native-v1/"
	raw, err := os.ReadFile(base + "internal/interaction/web/locales/zh-CN.json")
	if err != nil {
		t.Fatal(err)
	}
	var dictionary map[string]string
	if err = json.Unmarshal(raw, &dictionary); err != nil {
		t.Fatal(err)
	}
	tr := func(text string) string {
		if requestLanguage(r) != "zh-CN" {
			return text
		}
		if v, ok := dictionary[text]; ok {
			return v
		}
		for key, value := range dictionary {
			if strings.Contains(key, "%d") {
				pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(key), "%d", "([0-9]+)") + "$"
				if values := regexp.MustCompile(pattern).FindStringSubmatch(text); values != nil {
					for _, v := range values[1:] {
						value = strings.Replace(value, "%d", v, 1)
					}
					return value
				}
			}
		}
		return text
	}
	out := requestLayout(r).Funcs(template.FuncMap{"tr": tr})
	for _, name := range []string{"tasks", "task", "attention", "conversation", "schedule-editor"} {
		raw, err := os.ReadFile(base + "internal/interaction/web/templates/" + name + ".html")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = out.Parse(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func normalizedWorkbenchDOM(t *testing.T, source string) string {
	t.Helper()
	node, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var clean func(*html.Node, bool)
	clean = func(n *html.Node, preserve bool) {
		preserve = preserve || n.Data == "pre" || n.Data == "textarea" || n.Data == "code"
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling

			// Transport configuration and versioned stylesheet URLs are Host
			// additions, not visible template content. CSS is checked in-browser.
			transport := false
			if c.Type == html.ElementNode {
				for _, a := range c.Attr {
					if c.Data == "script" && a.Key == "id" && a.Val == "web-workbench-config" || c.Data == "link" && a.Key == "rel" && a.Val == "stylesheet" {
						transport = true
					}
				}
			}
			if transport {
				n.RemoveChild(c)
				c = next
				continue
			}

			if c.Type == html.TextNode && !preserve && strings.TrimSpace(c.Data) == "" {
				n.RemoveChild(c)
			} else {
				clean(c, preserve)
			}
			c = next
		}
	}
	clean(node, false)
	var out bytes.Buffer
	if err = html.Render(&out, node); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func TestWorkbenchOriginalOwnedViewsKeepNativeDOMAndFacts(t *testing.T) {
	original := originalWorkbenchPresentation(t)
	for _, language := range []string{"en", "zh-CN"} {
		for _, path := range []string{"/tasks", "/tasks/configurations", "/tasks/task:visible", "/tasks/executions/owned-root"} {
			t.Run(language+path, func(t *testing.T) {
				s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Tasks: workspaceReader{}}
				r := languageRequest("GET", path, language, "")
				p, err := s.page(r.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				p.SourceID = "original-source-identity"
				p.Language = language
				var native bytes.Buffer
				if err = capturedNativeWorkbenchLayout(t, r).Execute(&native, p); err != nil {
					t.Fatal(err)
				}
				p.Workbench = &workbenchPresentation{Presentation: original, ReaderRevision: "sha256:" + strings.Repeat("a", 64)}
				owned, err := workbenchPageLayout(p, r)
				if err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				if err = owned.Execute(&got, p); err != nil {
					t.Fatal(err)
				}
				if normalizedWorkbenchDOM(t, got.String()) != normalizedWorkbenchDOM(t, native.String()) {
					a, b := normalizedWorkbenchDOM(t, got.String()), normalizedWorkbenchDOM(t, native.String())
					i := 0
					for i < len(a) && i < len(b) && a[i] == b[i] {
						i++
					}
					t.Fatalf("managed default changed DOM at %d: got %q want %q", i, a[i:min(i+180, len(a))], b[i:min(i+180, len(b))])
				}
			})
		}
	}
}
func TestWorkbenchPrivateBindingsCannotExposeDomainEnvelopes(t *testing.T) {
	original := originalWorkbenchPresentation(t)
	original.View.Source = strings.Replace(original.View.Source, `{{define "task-header"}}`, `{{define "task-header"}}{{printf "%#v" .}}`, 1)
	original.View.Digest = rsiplugin.FileDigest([]byte(original.View.Source))
	s := &Server{Core: &updatesCore{allowed: true, status: "idle"}, Tasks: workspaceReader{}}
	r := httptest.NewRequest("GET", "http://localhost/tasks/task:visible", nil)
	p, err := s.page(r.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	p.Tasks.Detail.Messages = []conversation.Message{{MessageID: "PRIVATE_ORIGINAL_MESSAGE", Content: conversation.Text("PRIVATE_UNDISPLAYED_BODY")}}
	p.Tasks.Approvals = []approvalView{{ID: "approval:public", Hash: "PRIVATE_APPROVAL_HASH", Raw: "PRIVATE_APPROVAL_ARGUMENTS"}}
	p.Workbench = &workbenchPresentation{Presentation: original, ReaderRevision: "sha256:" + strings.Repeat("a", 64)}
	owned, err := workbenchPageLayout(p, r)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err = owned.ExecuteTemplate(&got, "task-header", p); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE_ORIGINAL_MESSAGE", "PRIVATE_UNDISPLAYED_BODY", "PRIVATE_APPROVAL_HASH", "PRIVATE_APPROVAL_ARGUMENTS"} {
		if strings.Contains(got.String(), secret) {
			t.Fatal("display DTO exposed domain envelope", secret)
		}
	}
}
func TestWorkbenchOwnerCannotRewriteNativeFormIdentity(t *testing.T) {
	original := originalWorkbenchPresentation(t)
	original.View.Source = strings.Replace(original.View.Source, `name="source_id" value="{{.SourceID}}"`, `name="source_id" value="FORGED_SOURCE"`, 1)
	original.View.Digest = rsiplugin.FileDigest([]byte(original.View.Source))
	p := page{SourceID: "original-source-identity", Status: Status{Readiness: "ready"}, Workbench: &workbenchPresentation{Presentation: original, ReaderRevision: "sha256:" + strings.Repeat("a", 64)}}
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	owned, err := workbenchPageLayout(p, r)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err = owned.ExecuteTemplate(&got, "conversation-page", p); err == nil {
		t.Fatal("candidate changed effect identity")
	}
}

// Real HTTP tests use the original captured producer, not a native fallback.
func workbenchTestReader(t *testing.T) func(context.Context, string) (contract.Presentation, string, bool, error) {
	t.Helper()
	original := originalWorkbenchPresentation(t)
	revision := "sha256:" + strings.Repeat("a", 64)
	return func(_ context.Context, reference string) (contract.Presentation, string, bool, error) {
		if reference != "" && reference != revision {
			return contract.Presentation{}, "", true, errWorkbenchUnavailable
		}
		return original, revision, true, nil
	}
}
