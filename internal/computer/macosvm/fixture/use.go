package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/z-chenhao/assistant/internal/computer"
	computerfake "github.com/z-chenhao/assistant/internal/computer/fake"
)

// shellWords decodes the adapter's quoted argv without executing any command.
func shellWords(raw string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	active := false
	for _, c := range raw {
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			active = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			active = true
			continue
		}
		if c == ' ' || c == '\n' || c == '\t' {
			if active {
				words = append(words, word.String())
				word.Reset()
				active = false
			}
			continue
		}
		word.WriteRune(c)
		active = true
	}
	if active {
		words = append(words, word.String())
	}
	return words
}
func (s *Server) useCommand(command, input string) (any, bool) {
	words := shellWords(command)
	ctx := context.Background()
	instance := computer.Instance{ComputerID: "fixture", Epoch: 1}
	if len(words) == 4 && words[0] == "/Applications/CuaDriver.app/Contents/MacOS/cua-driver" && words[1] == "call" {
		var a map[string]any
		if json.Unmarshal([]byte(words[3]), &a) != nil {
			return nil, false
		}
		switch words[2] {
		case "list_apps":
			return map[string]any{"apps": []any{map[string]any{"bundle_id": "com.apple.TextEdit", "name": "TextEdit", "pid": 100, "running": true}}}, true
		case "list_windows":
			return map[string]any{"windows": []any{map[string]any{"app_name": "TextEdit", "title": "Fixture", "pid": 100, "window_id": 10, "bounds": map[string]any{"x": 0, "y": 0, "width": 100, "height": 100}}}}, true
		case "get_desktop_state":
			s.previews++
			c, _ := s.use.Observe(ctx, instance, computer.Target{ID: "native"}, "web-preview", computer.ObserveOptions{Screenshot: true})
			return capture(c), true
		case "get_window_state":
			c, _ := s.use.Observe(ctx, instance, computer.Target{ID: "native"}, "lease", computer.ObserveOptions{Accessibility: a["include_accessibility_tree"] != false, Screenshot: a["include_screenshot"] == true})
			if len(c.Elements) > 0 {
				c.Elements[0].Role, c.Elements[0].Value = "AXTextField", s.use.Text
			}
			if s.selectionActive {
				start, end := min(s.selectionAnchor, s.selectionCaret), max(s.selectionAnchor, s.selectionCaret)
				c.State += fmt.Sprintf(" selection=%d:%d", start, end-start)
			}
			return capture(c), true
		default:
			if value, handled := s.selectionInput(words[2], a); handled {
				return value, true
			}
			method := map[string]string{"set_value": "setValue", "click": "click", "press_key": "pressKey", "type_text": "typeText", "launch_app": "launchApp", "drag": "drag", "scroll": "scroll"}[words[2]]
			if method == "" {
				return nil, false
			}
			v, err := s.use.Act(ctx, instance, computer.Target{}, "lease", method, a)
			if err == nil {
				s.selectionActive = false
			}
			return v, err == nil
		}
	}
	if len(words) > 3 && strings.Contains(command, "--input-type=module -e") {
		var req struct {
			Snapshot   bool           `json:"snapshot"`
			Method     string         `json:"method"`
			HelperHash string         `json:"helper_sha256"`
			Args       map[string]any `json:"args"`
		}
		if json.Unmarshal([]byte(words[len(words)-1]), &req) != nil {
			return nil, false
		}
		if req.Snapshot {
			program := sha256.Sum256([]byte(words[len(words)-2]))
			if req.Method != "observe" || hex.EncodeToString(program[:]) != "3fb9f874f3e9fe3afa101884564186724e89fcfc435422d75bc20f019cc5210f" {
				return map[string]string{"error": "PERMISSION_DENIED"}, true
			}
		}
		if req.HelperHash != "" {
			// Independently pinned v1 bytes prove the SSH adapter sends the exact
			// primitive exercised by real Chromium, not its old presentation code.
			// A new fixed program version must retain its prior resource consumer.
			program := sha256.Sum256([]byte(words[len(words)-2]))
			name, _ := req.Args["name"].(string)
			media, _ := req.Args["mediaType"].(string)
			if req.Method != "playwright.upload" || hex.EncodeToString(program[:]) != "4f35613c0cd1b62614ee09ca8c8f01571a9a833297ac1684a0b86516faf46d22" || req.HelperHash != "fd0869a24ac31e39ed5d711a3f8acc4263f71549ab45b830ce405877446dc84a" || name == "" || media == "" {
				return map[string]string{"error": "PERMISSION_DENIED"}, true
			}
		}
		if s.browserEndpoint != "" {
			return s.browserCall(words[len(words)-1]), true
		}
		if req.Method == "inventory" {
			b, _ := s.use.Inventory(ctx, instance, "browsers")
			t, _ := s.use.Inventory(ctx, instance, "tabs")
			return map[string]any{"browsers": b, "tabs": t}, true
		}
		if req.Method == "observe" {
			if req.Snapshot {
				v, err := s.use.Snapshot(ctx, instance, computer.Target{Kind: "tab", ID: "fixture-tab"}, "lease", computer.ObserveOptions{Accessibility: req.Args["ax"] != false, Screenshot: req.Args["screenshot"] == true})
				if err != nil {
					return map[string]string{"error": "PROVIDER_UNAVAILABLE"}, true
				}
				c := capture(v.Capture)
				c["source"] = v.Source
				return c, true
			}

			c, _ := s.use.Observe(ctx, instance, computer.Target{Kind: "tab", ID: "fixture-tab"}, "lease", computer.ObserveOptions{Accessibility: req.Args["ax"] != false, Screenshot: req.Args["screenshot"] == true})
			return capture(c), true
		}
		v, err := s.use.Act(ctx, instance, computer.Target{ID: "fixture-tab", Kind: "tab"}, "lease", req.Method, req.Args)
		if err != nil {
			return nil, false
		}
		return v, true
	}
	if len(words) == 3 && words[0] == "/usr/bin/python3" && words[1] == "-c" {
		var req struct {
			Operation, Path, Data string
			Overwrite             bool
		}
		if json.Unmarshal([]byte(input), &req) != nil || !computer.SafeTransferPath(req.Path) {
			return map[string]string{"error": "PERMISSION_DENIED"}, true
		}
		if req.Operation == "write" {
			data, err := base64.StdEncoding.DecodeString(req.Data)
			if err != nil {
				return nil, false
			}
			err = s.use.WriteFile(ctx, instance, req.Path, req.Overwrite, strings.NewReader(string(data)))
			if err != nil {
				return map[string]string{"error": "PERMISSION_DENIED"}, true
			}
			return map[string]string{"data": ""}, true
		}
		data := s.use.Files[req.Path]
		if data == nil {
			return map[string]string{"error": "PERMISSION_DENIED"}, true
		}
		return map[string]string{"data": base64.StdEncoding.EncodeToString(data)}, true
	}
	return nil, false
}

// A protocol peer for the fixed native navigation primitives. This is a
// deterministic test document/caret, not a second production selection method.
func (s *Server) selectionInput(method string, args map[string]any) (any, bool) {
	step, extend := 0, false
	if method == "hotkey" {
		keys, _ := args["keys"].([]any)
		if len(keys) != 2 {
			return nil, false
		}
		if keys[0] == "cmd" && (keys[1] == "up" || keys[1] == "down") {
			s.selectionCaret, s.selectionActive = 0, false
			if keys[1] == "down" {
				s.selectionCaret = len(s.use.Text)
			}
			return map[string]string{"effect": "unverifiable"}, true
		}
		if keys[0] != "shift" {
			return nil, false
		}
		extend = true
		args = map[string]any{"key": keys[1]}
	} else if method != "press_key" {
		return nil, false
	}
	switch args["key"] {
	case "left":
		step = -1
	case "right":
		step = 1
	default:
		return nil, false
	}
	if extend && !s.selectionActive {
		s.selectionAnchor = s.selectionCaret
	}
	s.selectionActive = extend
	s.selectionCaret = max(0, min(len(s.use.Text), s.selectionCaret+step))
	return map[string]string{"effect": "unverifiable"}, true
}

func capture(c computer.Capture) map[string]any {
	elements := []any{}
	for _, e := range c.Elements {
		elements = append(elements, map[string]any{"element_index": e.Index, "element_token": e.Token, "actions": e.Actions, "role": e.Role, "value": e.Value})
	}
	return map[string]any{"tree_markdown": c.State, "elements": elements, "navigation": c.Navigation, "screenshot_png_b64": base64.StdEncoding.EncodeToString(c.PNG), "screenshot_width": c.Width, "screenshot_height": c.Height, "window_bounds": c.Geometry}
}
func newUseFake() *computerfake.Driver {
	return &computerfake.Driver{Geometry: "100x100", Files: map[string][]byte{}}
}
