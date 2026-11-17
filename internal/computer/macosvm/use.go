package macosvm

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/computer"
)

// BrowserConfig is Guest provisioning configuration, never a model parameter.
// playwright-core and Node are installed in the sealed base, not on the Host.
type BrowserConfig struct {
	Node   string `yaml:"node" json:"node"`
	Module string `yaml:"module" json:"module"`
	Port   int    `yaml:"port" json:"port"`
}

func (c BrowserConfig) Validate() error {
	if !strings.HasPrefix(c.Node, "/") || !strings.HasPrefix(c.Module, "/") || strings.ContainsAny(c.Node+c.Module, "\x00\n\r") || c.Port < 1024 || c.Port > 65535 {
		return computer.ErrInvalid
	}
	return nil
}

//go:embed browser.mjs
var browserSource string

//go:embed transfer.py
var transferSource string

func (p *Provider) useGuest(ctx context.Context, i computer.Instance) (SSHGuest, string, error) {
	if err := p.Prepare(ctx); err != nil {
		return SSHGuest{}, "", err
	}
	g, ok := p.guest.(SSHGuest)
	if !ok {
		return SSHGuest{}, "", computer.ErrUnavailable
	}
	status, ip, err := p.state(ctx, i.NativeName)
	if err != nil || status != "running" || ip == "" {
		return SSHGuest{}, "", computer.ErrStale
	}
	return g, ip, nil
}
func (g SSHGuest) driver(ctx context.Context, ip, method string, args map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(args)
	output, err := g.command(ctx, ip, "/Applications/CuaDriver.app/Contents/MacOS/cua-driver call "+method+" "+quote(string(raw)), false)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if json.Unmarshal([]byte(output), &v) != nil {
		return nil, computer.ErrUnavailable
	}
	if v["error"] != nil || v["isError"] == true || v["is_error"] == true || v["effect"] == "refused" {
		return nil, computer.ErrInvalid
	}
	if structured, ok := v["structuredContent"].(map[string]any); ok {
		return structured, nil
	}
	return v, nil
}
func text(v any) string         { s, _ := v.(string); return s }
func number(v any) int          { n, _ := v.(float64); return int(n) }
func dict(v any) map[string]any { m, _ := v.(map[string]any); return m }
func (p *Provider) browser(ctx context.Context, i computer.Instance, method string, args map[string]any) (map[string]any, error) {
	if p.config.Browser == nil {
		return nil, computer.ErrUnavailable
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return nil, err
	}
	cfg := p.config.Browser
	raw, _ := json.Marshal(map[string]any{"method": method, "args": args, "port": cfg.Port, "module": cfg.Module, "root": p.transferRoot()})
	output, err := g.command(ctx, ip, quote(cfg.Node)+" --input-type=module -e "+quote(browserSource)+" "+quote(string(raw)), false)
	if err != nil {
		if method != "inventory" && method != "observe" && !strings.HasPrefix(method, "playwright.waitFor") && method != "playwright.count" && method != "playwright.textContent" {
			return nil, computer.ErrUnknown
		}
		return nil, err
	}
	return decodeBrowserResponse(output)
}

func decodeBrowserResponse(output string) (map[string]any, error) {
	var v map[string]any
	if json.Unmarshal([]byte(output), &v) != nil {
		return nil, computer.ErrUnknown
	}
	switch text(v["error"]) {
	case "":
		return v, nil
	case "INVALID_ARGUMENT":
		return nil, computer.ErrInvalid
	case "UNKNOWN_SIDE_EFFECT":
		return nil, computer.ErrUnknown
	case "PERMISSION_DENIED":
		return nil, computer.ErrIntegrity
	case "STALE_ELEMENT_REFERENCE":
		return nil, computer.ErrElement
	case "STALE_SURFACE_HANDLE":
		return nil, computer.ErrStale
	default:
		return nil, computer.ErrUnavailable
	}
}
func (p *Provider) Inventory(ctx context.Context, i computer.Instance, kind string) ([]computer.Target, error) {
	if kind == "browsers" || kind == "tabs" {
		if p.config.Browser == nil {
			return []computer.Target{}, nil
		}
		v, err := p.browser(ctx, i, "inventory", map[string]any{})
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(v[kind])
		var targets []computer.Target
		if json.Unmarshal(raw, &targets) != nil {
			return nil, computer.ErrUnavailable
		}
		return targets, nil
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return nil, err
	}
	method := "list_windows"
	if kind == "apps" {
		method = "list_apps"
	}
	v, err := g.driver(ctx, ip, method, map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, ok := v[kind].([]any)
	if !ok {
		return nil, computer.ErrUnavailable
	}
	out := []computer.Target{}
	for _, row := range rows {
		m := dict(row)
		if kind == "apps" {
			if m["running"] != true || number(m["pid"]) < 1 {
				continue
			}
			id := text(m["bundle_id"])
			if id == "" {
				continue
			}
			out = append(out, computer.Target{ID: id, Kind: "app", Name: text(m["name"]), Native: map[string]any{"pid": m["pid"]}})
		} else {
			out = append(out, windowTarget(m))
		}
	}
	return out, nil
}
func windowTarget(m map[string]any) computer.Target {
	hash, _ := computer.Fingerprint([]any{m["pid"], m["window_id"]})
	return computer.Target{ID: "window:" + hash[:24], Kind: "window", Name: text(m["title"]), Native: map[string]any{"pid": m["pid"], "window_id": m["window_id"], "bounds": m["bounds"]}}
}
func (p *Provider) nativeTarget(ctx context.Context, i computer.Instance, t computer.Target) (map[string]any, error) {
	if t.Native["window_id"] != nil {
		return t.Native, nil
	}
	windows, err := p.Inventory(ctx, i, "windows")
	if err != nil {
		return nil, err
	}
	var selected map[string]any
	for _, w := range windows {
		if w.Native["pid"] == t.Native["pid"] {
			if selected != nil {
				return nil, computer.ErrInvalid
			}
			selected = w.Native
		}
	}
	if selected == nil {
		return nil, computer.ErrStale
	}
	return selected, nil
}
func parseCapture(v map[string]any) (computer.Capture, error) {
	c := computer.Capture{State: text(v["tree_markdown"]), Geometry: fmt.Sprint(v["window_bounds"]), Navigation: text(v["navigation"]), Width: number(v["screenshot_width"]), Height: number(v["screenshot_height"])}
	for _, row := range func() []any { rows, _ := v["elements"].([]any); return rows }() {
		m := dict(row)
		actions := []string{}
		if values, ok := m["actions"].([]any); ok {
			for _, a := range values {
				if action := semanticAction(text(a)); action != "press" && action != "click" {
					actions = append(actions, semanticAction(text(a)))
				}
			}
		}
		index := number(m["element_index"])
		if m["element_index"] == nil {
			index = number(m["index"])
		}
		c.Elements = append(c.Elements, computer.Element{Index: index, Token: text(m["element_token"]), Actions: actions, Value: text(m["value"]), Role: text(m["role"])})
	}
	if text(v["screenshot_png_b64"]) != "" {
		var err error
		c.PNG, err = base64.StdEncoding.DecodeString(text(v["screenshot_png_b64"]))
		if err != nil {
			return c, computer.ErrUnavailable
		}
	}
	if geometry := text(v["geometry"]); geometry != "" {
		c.Geometry = geometry
	}
	return c, nil
}
func (p *Provider) Observe(ctx context.Context, i computer.Instance, t computer.Target, lease string, options computer.ObserveOptions) (computer.Capture, error) {
	if t.Kind == "tab" {
		v, err := p.browser(ctx, i, "observe", map[string]any{"tabId": t.ID, "screenshot": options.Screenshot, "ax": options.Accessibility})
		if err != nil {
			return computer.Capture{}, err
		}
		c, err := parseCapture(v)
		c.HasAX = options.Accessibility
		return c, err
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return computer.Capture{}, err
	}
	target, err := p.nativeTarget(ctx, i, t)
	if err != nil {
		return computer.Capture{}, err
	}
	args := map[string]any{"pid": target["pid"], "window_id": target["window_id"], "session": lease, "include_screenshot": options.Screenshot, "include_accessibility_tree": options.Accessibility, "tree_format": "elements", "full_output": true}
	v, err := g.driver(ctx, ip, "get_window_state", args)
	if err != nil {
		return computer.Capture{}, err
	}
	c, err := parseCapture(v)
	c.HasAX = options.Accessibility
	return c, err
}
func (p *Provider) Act(ctx context.Context, i computer.Instance, t computer.Target, lease, method string, m map[string]any) (any, error) {
	if t.Kind == "tab" || t.Kind == "browser" {
		m["tabId"] = t.ID
		m["browserId"] = t.ID
		return p.browser(ctx, i, method, m)
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return nil, err
	}
	if method == "launchApp" {
		return g.driver(ctx, ip, "launch_app", map[string]any{"bundle_id": m["app"], "session": lease})
	}
	target, err := p.nativeTarget(ctx, i, t)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"pid": target["pid"], "window_id": target["window_id"], "session": lease}
	// A separate inventory read validates geometry without replacing AX captures.
	if m["x"] != nil {
		windows, err := p.Inventory(ctx, i, "windows")
		if err != nil {
			return nil, err
		}
		valid := false
		for _, w := range windows {
			if w.Native["window_id"] == target["window_id"] {
				valid = fmt.Sprint(w.Native["bounds"]) == text(m["geometry"])
			}
		}
		if !valid {
			return nil, computer.ErrStale
		}
		args["x"], args["y"] = m["x"], m["y"]
	}
	if m["element_token"] != nil {
		args["element_token"] = m["element_token"]
	}
	if method == "selectText" {
		dispatch, ok := m["selection_dispatch"].(computer.SelectionDispatch)
		if !ok {
			return nil, computer.ErrInvalid
		}
		return nativeSelection(ctx, dispatch, args, func(method string, params map[string]any) (map[string]any, error) {
			return g.driver(ctx, ip, method, params)
		})
	}
	if method == "pressKey" {
		dispatch, ok := m["key_dispatch"].(computer.KeyDispatch)
		if !ok {
			return nil, computer.ErrInvalid
		}
		return nativeKey(ctx, dispatch, args, func(method string, params map[string]any) (map[string]any, error) {
			return g.driver(ctx, ip, method, params)
		})
	}
	toolName := map[string]string{"click": "click", "scroll": "scroll", "drag": "drag", "setValue": "set_value", "performSecondaryAction": "click", "typeText": "type_text", "paste": "type_text"}[method]
	if toolName == "" {
		return nil, computer.ErrInvalid
	}
	switch method {
	case "click":
		opts := dict(m["options"])
		if opts["button"] != nil {
			args["button"] = opts["button"]
		}
		if opts["count"] == float64(2) {
			toolName = "double_click"
		}
	case "scroll":
		args["direction"] = m["direction"]
		if m["amount"] != nil {
			args["amount"] = m["amount"]
		}
	case "drag":
		args["from_x"], args["from_y"], args["to_x"], args["to_y"] = m["x"], m["y"], m["to_x"], m["to_y"]
		delete(args, "x")
		delete(args, "y")
	case "setValue":
		args["value"] = m["value"]
	case "performSecondaryAction":
		args["action"] = m["value"]

	default:
		args["text"] = m["text"]
	}
	v, err := g.driver(ctx, ip, toolName, args)
	if err != nil {
		if err == computer.ErrInvalid {
			return nil, err
		}
		return nil, computer.ErrUnknown
	}
	// Never leak raw native IDs or provider metadata from an action response.
	return map[string]any{"effect": v["effect"], "verified": v["verified"]}, nil
}
func (p *Provider) transferRoot() string { return "/Users/" + p.config.Guest.User + "/StewardTransfer" }
func (p *Provider) transfer(ctx context.Context, i computer.Instance, method, path string, overwrite bool, data []byte) ([]byte, error) {
	if !computer.SafeTransferPath(path) {
		return nil, computer.ErrIntegrity
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return nil, err
	}
	args, _ := json.Marshal(map[string]any{"root": p.transferRoot(), "path": path, "operation": method, "overwrite": overwrite, "data": base64.StdEncoding.EncodeToString(data)})
	output, err := g.commandInput(ctx, ip, "/usr/bin/python3 -c "+quote(transferSource), false, bytes.NewReader(args))
	if err != nil {
		if method == "write" {
			return nil, computer.ErrUnknown
		}
		return nil, computer.ErrIntegrity
	}
	var v struct {
		Data  string `json:"data"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(output), &v) != nil {
		return nil, computer.ErrUnknown
	}
	if v.Error == "UNKNOWN_SIDE_EFFECT" {
		return nil, computer.ErrUnknown
	}
	if v.Error != "" {
		return nil, computer.ErrIntegrity
	}
	result, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return nil, computer.ErrIntegrity
	}
	return result, nil
}
func (p *Provider) ReadFile(ctx context.Context, i computer.Instance, path string) (io.ReadCloser, error) {
	data, err := p.transfer(ctx, i, "read", path, false, nil)
	return io.NopCloser(bytes.NewReader(data)), err
}
func (p *Provider) WriteFile(ctx context.Context, i computer.Instance, path string, overwrite bool, reader io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return computer.ErrInvalid
	}
	_, err = p.transfer(ctx, i, "write", path, overwrite, data)
	return err
}

var _ computer.Driver = (*Provider)(nil)

func semanticAction(action string) string {
	switch action {
	case "AXPress":
		return "press"
	case "AXShowMenu":
		return "show_menu"
	case "AXConfirm":
		return "confirm"
	case "AXCancel":
		return "cancel"
	case "AXPick":
		return "pick"
	case "AXOpen":
		return "open"
	case "AXShowAlternateUI":
		return "show_alternate_ui"
	case "AXShowDefaultUI":
		return "show_default_ui"
	case "AXRaise":
		return "raise"
	}
	return action
}

// The embedded Guest programs and pinned module are part of the frozen bridge
// capability. An upgrade cannot silently replay against different programs.
func UseResourceHash() string {
	hash, _ := computer.Fingerprint([]string{"computer-use-v1", "playwright-core@1.64.1", browserSource, transferSource})
	return hash
}
