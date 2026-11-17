package web

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type capabilityViewAssets struct{ View, Copy *contract.Asset }

// Display-only DTO: no raw configuration document, host path, Manager, methods,
// proof storage or other page state is available to the candidate template.
type capabilityViewRecord struct {
	ID, Title, Description, Help, State, Message, Revision                           string
	ParentMemory, Configured, PendingRestart, Verified, Candidate, Checked, Advanced bool
	Form                                                                             *contract.Form
}
type capabilityViewData struct {
	Capability   *capabilityViewRecord
	Capabilities []capabilityViewRecord
}

func capabilityViewRecordOf(v Capability) capabilityViewRecord {
	return capabilityViewRecord{ID: v.ID, Title: v.Title, Description: v.Description, Help: v.Help, State: v.State, Message: v.Message, Revision: v.Revision, ParentMemory: v.ParentMemory, Configured: v.Configured, PendingRestart: v.PendingRestart, Verified: v.Verified, Candidate: v.Candidate, Checked: v.Checked, Advanced: v.Advanced, Form: v.Form}
}
func capabilityViewModel(p page) capabilityViewData {
	out := capabilityViewData{Capabilities: []capabilityViewRecord{}}
	if p.Capability != nil {
		v := capabilityViewRecordOf(*p.Capability)
		out.Capability = &v
	}
	for _, v := range p.Capabilities {
		out.Capabilities = append(out.Capabilities, capabilityViewRecordOf(v))
	}
	return out
}

const capabilityViewFields = "Capability Capabilities ID Title Description Help State Message Revision ParentMemory Configured PendingRestart Verified Candidate Checked Advanced Form Mode Available Enabled Error Memory Embedding Skills Execution Computer Providers KeyRef Endpoint Model Dimensions Root Scope Toolchains ProviderVersion GuestBuild DriverVersion DriverSHA256 DriverTeam DriverBundle Architecture User Port PasswordRef HostKey Node Script ManifestVersion Original Name Command Directory Args Tools Permission Label Text"

func compileCapabilityView(source, language string, entries []contract.Translation) (*template.Template, error) {
	copy := map[string]string{}
	patterns := []translatedPattern{}
	if len(entries) > 256 {
		return nil, ErrCapabilityUnavailable
	}
	for _, entry := range entries {
		if entry.Key == "" || entry.Value == "" || len(entry.Key) > 16384 || len(entry.Value) > 16384 || copy[entry.Key] != "" {
			return nil, ErrCapabilityUnavailable
		}
		copy[entry.Key] = entry.Value
		if strings.Contains(entry.Key, "%d") {
			pattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(entry.Key), "%d", "([0-9]+)") + "$"
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, ErrCapabilityUnavailable
			}
			patterns = append(patterns, translatedPattern{expression: re, text: entry.Value})
		}
	}
	owned := func(text string) string {
		if language != "zh-CN" {
			return text
		}
		if value, ok := copy[text]; ok {
			return value
		}
		for _, pattern := range patterns {
			if matches := pattern.expression.FindStringSubmatch(text); matches != nil {
				result := pattern.text
				for _, value := range matches[1:] {
					result = strings.Replace(result, "%d", value, 1)
				}
				return result
			}
		}
		return text
	}
	functions := template.FuncMap{"tr": func(s string) string { return translate(language, s) }, "capabilityTr": owned, "capabilityHelp": func(v *capabilityViewRecord) helpView {
		return help("capability-"+v.ID+"-help", owned(v.Title), owned(v.Description)+" "+owned(v.Help))
	}, "capabilityURL": capabilityURL}
	t, err := compileInspectionView(source, functions, localizedLayout(language), inspectionTemplateSpec{root: "capability-settings-view", names: map[string]bool{"capabilities-page": true, "capability-status": true, "capability-guided-form": true}, privateName: func(name string) bool { return strings.HasPrefix(name, "capability-") }, shared: map[string]bool{"help": true, "icon-close": true}, fields: capabilityViewFields, functions: " capabilityTr capabilityHelp capabilityURL eq ne gt and or not len ", variables: true, unavailable: ErrCapabilityUnavailable})
	if err != nil {
		return nil, err
	}
	if !boundedCapabilityTemplate(t) {
		return nil, ErrCapabilityUnavailable
	}
	return t, nil
}

type capabilityViewBuffer struct {
	bytes.Buffer
	remaining int
}

func (w *capabilityViewBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, ErrCapabilityUnavailable
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}
func (w *capabilityViewBuffer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func renderCapabilityView(p page, r *http.Request) (template.HTML, error) {
	assets := p.CapabilityView
	if assets == nil || assets.View == nil || assets.Copy == nil || assets.View.Resource != contract.ViewResource || assets.Copy.Resource != contract.CopyResource {
		return "", ErrCapabilityUnavailable
	}
	for _, v := range []*contract.Asset{assets.View, assets.Copy} {
		if len(v.Source) == 0 || len(v.Source) > 65536 || rsiplugin.FileDigest([]byte(v.Source)) != v.Digest {
			return "", ErrCapabilityUnavailable
		}
	}
	var entries []contract.Translation
	if err := json.Unmarshal([]byte(assets.Copy.Source), &entries); err != nil {
		return "", ErrCapabilityUnavailable
	}
	t, err := compileCapabilityView(assets.View.Source, requestLanguage(r), entries)
	if err != nil {
		return "", err
	}
	data := capabilityViewModel(p)
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	out := capabilityViewBuffer{remaining: 65536 + 16*len(raw)}
	if err = t.ExecuteTemplate(&out, "capabilities-page", data); err != nil {
		return "", err
	}
	if !safeCapabilityViewDOM(out.String(), data) {
		return "", ErrCapabilityUnavailable
	}
	return template.HTML(out.String()), nil
}
func safeCapabilityViewDOM(source string, data capabilityViewData) bool {
	nodes, err := html.ParseFragmentWithOptions(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}, html.ParseOptionEnableScripting(false))
	if err != nil {
		return false
	}
	tags := " a button details div fieldset form h1 h2 h3 input label legend noscript option p section select span strong summary textarea svg path circle line polyline rect "
	allowed := " class id role title placeholder tabindex type name value maxlength autocomplete spellcheck autocapitalize required for hidden open checked selected disabled inert method action href width height viewbox fill stroke stroke-width stroke-linecap stroke-linejoin d cx cy r x y x1 x2 y1 y2 points rx ry rows min max step formnovalidate "
	dataAttrs := " data-status data-required data-capability-draft data-capability-feedback data-capability-fields data-capability-form data-capability-mode data-context-help data-help-fallback data-help-trigger data-help-tooltip "
	forms := 0
	var visit func(*html.Node) bool
	visit = func(n *html.Node) bool {
		attrs := map[string]string{}
		if n.Type == html.ElementNode {
			if !strings.Contains(tags, " "+n.Data+" ") {
				return false
			}
			for _, attr := range n.Attr {
				key := strings.ToLower(attr.Key)
				attrs[key] = attr.Val
				if attr.Namespace != "" || strings.HasPrefix(key, "on") || key == "style" || (key == "fill" || key == "stroke") && strings.Contains(strings.ToLower(attr.Val), "url(") {
					return false
				}
				if !strings.Contains(allowed, " "+key+" ") && !strings.HasPrefix(key, "aria-") && !strings.Contains(dataAttrs, " "+key+" ") {
					return false
				}
				if key == "href" && n.Data != "a" || key == "action" && n.Data != "form" {
					return false
				}
			}
			if n.Data == "a" {
				href := attrs["href"]
				if href != "/settings/capabilities" && !validCapability(strings.TrimPrefix(href, "/settings/capabilities/")) {
					return false
				}
			}
			if n.Data == "form" {
				forms++
				if forms > 1 || data.Capability == nil || attrs["action"] != "/capability/configure" || strings.ToLower(attrs["method"]) != "post" {
					return false
				}
				hidden := map[string]string{}
				valid := true
				var controls func(*html.Node)
				controls = func(c *html.Node) {
					if c.Type == html.ElementNode {
						a := map[string]string{}
						for _, attr := range c.Attr {
							a[attr.Key] = attr.Val
						}
						if name := a["name"]; name != "" {
							if c.Data == "button" {
								if name != "action" || !validCapabilityViewAction(a["value"], data.Capability.Form) {
									valid = false
								}
							} else if c.Data == "input" || c.Data == "select" || c.Data == "textarea" {
								if name != "editor" && name != "capability" && name != "revision" && !capabilityPostedField(name) {
									valid = false
								}
								if c.Data == "input" && a["type"] == "hidden" {
									if _, exists := hidden[name]; exists {
										valid = false
									}
									hidden[name] = a["value"]
									if name != "editor" && name != "capability" && name != "revision" && name != "provider_count" && !strings.HasSuffix(name, ".original") && !strings.HasSuffix(name, ".tool_count") {
										valid = false
									}
								}
							} else {
								valid = false
							}
						}
					}
					for child := c.FirstChild; child != nil; child = child.NextSibling {
						controls(child)
					}
				}
				controls(n)
				if !valid || hidden["editor"] != "guided" || hidden["capability"] != data.Capability.ID || hidden["revision"] != data.Capability.Revision {
					return false
				}
				if data.Capability.ID == "mcp" {
					f := data.Capability.Form
					if f == nil || hidden["provider_count"] != strconv.Itoa(len(f.Providers)) {
						return false
					}
					for i, p := range f.Providers {
						prefix := "provider." + strconv.Itoa(i)
						if hidden[prefix+".original"] != p.Original || hidden[prefix+".tool_count"] != strconv.Itoa(len(p.Tools)) {
							return false
						}
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if !visit(child) {
				return false
			}
		}
		return true
	}
	for _, node := range nodes {
		if !visit(node) {
			return false
		}
	}
	expected := 0
	if data.Capability != nil && data.Capability.Form != nil && data.Capability.Form.Available {
		expected = 1
	}
	return forms == expected
}
func validCapabilityViewAction(value string, form *contract.Form) bool {
	if value == "apply" || value == "add-provider" {
		return true
	}
	for _, prefix := range []string{"remove-provider-", "add-tool-", "remove-tool-"} {
		if strings.HasPrefix(value, prefix) {
			if form == nil {
				return false
			}
			parts := strings.Split(strings.TrimPrefix(value, prefix), "-")
			i, err := strconv.Atoi(parts[0])
			if err != nil || i < 0 || i >= len(form.Providers) {
				return false
			}
			if prefix != "remove-tool-" {
				return len(parts) == 1
			}
			if len(parts) != 2 {
				return false
			}
			j, err := strconv.Atoi(parts[1])
			return err == nil && j >= 0 && j < len(form.Providers[i].Tools)
		}
	}
	return false
}

// The only iterable data are the six records, <=32 providers and <=128 tools.
// Count expansion through private helper calls too, including empty bodies:
// output-byte limits alone cannot bound nested or exponentially reused helpers.
func boundedCapabilityTemplate(t *template.Template) bool {
	const limit = 4_000_000
	memo := map[string]int{}
	active := map[string]bool{}
	add := func(a, b int) int {
		if a > limit-b {
			return limit + 1
		}
		return a + b
	}
	var cost func(string) int
	var node func(parse.Node) int
	node = func(n parse.Node) int {
		if n == nil {
			return 0
		}
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return 0
			}
			total := 1
			for _, child := range n.Nodes {
				total = add(total, node(child))
				if total > limit {
					break
				}
			}
			return total
		case *parse.IfNode:
			if len(n.Pipe.Decl) > 0 || n.Pipe.IsAssign {
				return limit + 1
			}
			return add(1, max(node(n.List), node(n.ElseList)))
		case *parse.WithNode:
			if len(n.Pipe.Decl) > 0 || n.Pipe.IsAssign {
				return limit + 1
			}
			return add(1, max(node(n.List), node(n.ElseList)))
		case *parse.RangeNode:
			if n.Pipe == nil || len(n.Pipe.Cmds) != 1 || len(n.Pipe.Cmds[0].Args) != 1 {
				return limit + 1
			}
			var fields []string
			switch value := n.Pipe.Cmds[0].Args[0].(type) {
			case *parse.FieldNode:
				fields = value.Ident
			case *parse.VariableNode:
				fields = value.Ident
			}
			if len(fields) == 0 {
				return limit + 1
			}
			last := fields[len(fields)-1]
			if last != "Capabilities" && last != "Providers" && last != "Tools" {
				return limit + 1
			}
			body := node(n.List)
			if body > limit/128 {
				return limit + 1
			}
			return add(1, add(body*128, node(n.ElseList)))
		case *parse.ActionNode:
			if len(n.Pipe.Decl) > 0 || n.Pipe.IsAssign {
				return limit + 1
			}
			return 1
		case *parse.TemplateNode:
			return add(1, cost(n.Name))
		default:
			return 1
		}
	}
	cost = func(name string) int {
		if name != "capabilities-page" && !strings.HasPrefix(name, "capability-") {
			return 64
		}
		if value, ok := memo[name]; ok {
			return value
		}
		if active[name] {
			return limit + 1
		}
		active[name] = true
		part := t.Lookup(name)
		if part == nil || part.Tree == nil {
			return limit + 1
		}
		value := node(part.Tree.Root)
		active[name] = false
		memo[name] = value
		return value
	}
	return cost("capabilities-page") <= limit
}
