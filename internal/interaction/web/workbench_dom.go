package web

import (
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Untrusted markup gets no script, remote request, alternate action, or changed
// opaque identity. Approved cards and rich output are restored by Host only.
func safeWorkbenchDOM(source string, p page) bool {
	nodes, err := html.ParseFragmentWithOptions(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}, html.ParseOptionEnableScripting(false))
	if err != nil {
		return false
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	local := func(value string) bool {
		if strings.Contains(value, "ZgotmplZ") {
			return false
		}
		u, err := url.Parse(value)
		return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil && !strings.HasPrefix(value, "//") && (strings.HasPrefix(value, "/") || strings.HasPrefix(value, "#"))
	}
	allowedTags := " a article aside b br button circle code dd details dialog div dl dt em fieldset footer form g h1 h2 h3 header hr i img input label legend li line main nav ol option p path polygon polyline rect section select small span strong summary svg table tbody td textarea th thead time tr ul "
	ids := map[string]bool{}
	formOK := func(form *html.Node) bool {
		action := attr(form, "action")
		method := strings.ToLower(attr(form, "method"))
		if method == "get" && (action == "/tasks" || action == "/tasks/configurations") {
			return true
		}
		refresh := method == "get" && p.Tasks != nil && p.Tasks.Detail != nil && action == taskURL(p.Tasks.Detail.TaskRef)
		if method != "post" && !refresh {
			return false
		}
		expected := map[string]string{}
		if refresh {
			if p.Tasks.GitHubRule == nil || !p.Tasks.GitHubRule.CanStop {
				return false
			}
			expected["github_scope"] = p.Tasks.GitHubRule.Scope
		} else {
			switch action {
			case "/message":
				expected["source_id"] = p.SourceID
				if p.DraftTask != nil {
					expected["task_ref"] = p.DraftTask.Ref
				}
			case "/model/choose":
				expected["consent"] = "yes"
				expected["config_version"] = strconv.FormatInt(p.Status.ConfigVersion, 10)
			case "/task/github-rule":
				if p.Tasks == nil || p.Tasks.Detail == nil || p.Tasks.GitHubRule == nil || p.Workbench == nil || !p.Workbench.Presentation.Policy.GitHubRules {
					return false
				}
				d, rule := p.Tasks.Detail, p.Tasks.GitHubRule
				if !rule.CanStart && !rule.CanStop {
					return false
				}
				expected = map[string]string{"task_ref": d.TaskRef, "epoch": strconv.FormatInt(d.Epoch, 10), "scope": rule.Scope, "version": strconv.FormatInt(rule.Version, 10), "workbench_revision": p.Workbench.ReaderRevision}
			case "/task/feedback":
				ref := ""
				var scan func(*html.Node)
				scan = func(n *html.Node) {
					if n.Type == html.ElementNode && n.Data == "input" && attr(n, "name") == "feedback_ref" {
						ref = attr(n, "value")
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						scan(c)
					}
				}
				scan(form)
				var found bool
				if p.Attention != nil {
					for _, f := range p.Attention.Feedbacks {
						if f.Ref == ref {
							expected["source_id"], expected["feedback_ref"], expected["return_to"] = ref, ref, taskURL(f.TaskRef)
							found = true
							break
						}
					}
				}
				if !found {
					return false
				}
			case "/schedule/trial":
				if p.Schedule == nil || p.Schedule.Ref == "" {
					return false
				}
				expected = map[string]string{"ref": p.Schedule.Ref, "version": strconv.FormatInt(p.Schedule.Version, 10), "source_id": p.Schedule.Key}
			case "/schedule/configure":
				if p.Schedule == nil {
					return false
				}
				s := p.Schedule
				expected = map[string]string{"ref": s.Ref, "version": strconv.FormatInt(s.Version, 10), "source_id": s.Key, "original_spec": s.OriginalSpec, "original_timezone": s.OriginalTimezone, "task_ref": s.TaskRef}
			default:
				return false
			}
		}
		hidden := map[string]string{}
		var visit func(*html.Node) bool
		visit = func(n *html.Node) bool {
			if n.Type == html.ElementNode && n.Data == "input" && attr(n, "type") == "hidden" {
				name, value := attr(n, "name"), attr(n, "value")
				want, ok := expected[name]
				if !ok || value != want {
					return false
				}
				if _, duplicate := hidden[name]; duplicate {
					return false
				}
				hidden[name] = value
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if !visit(c) {
					return false
				}
			}
			return true
		}
		if !visit(form) {
			return false
		}
		for name := range expected {
			if name == "task_ref" && expected[name] == "" {
				continue
			}
			if _, ok := hidden[name]; !ok {
				return false
			}
		}
		return true
	}
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if !strings.Contains(allowedTags, " "+n.Data+" ") {
				return false
			}
			if id := attr(n, "id"); id != "" {
				if ids[id] {
					return false
				}
				ids[id] = true
			}
			for _, a := range n.Attr {
				if a.Namespace != "" || strings.HasPrefix(a.Key, "on") || a.Key == "style" || a.Key == "srcdoc" || a.Key == "srcset" || strings.HasPrefix(a.Key, "formaction") || a.Key == "formmethod" || a.Key == "formtarget" {
					return false
				}
				if (a.Key == "href" || a.Key == "src") && !local(a.Val) {
					return false
				}
			}
			if n.Data == "form" && !formOK(n) {
				return false
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !visit(c) {
				return false
			}
		}
		return true
	}
	for _, n := range nodes {
		if !visit(n) {
			return false
		}
	}
	return true
}
