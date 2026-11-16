// Package mcp adapts configured stdio MCP servers to the ordinary ToolExecutor.
// Server annotations never grant permissions or choose retry safety.
package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/internal/tool"
)

const protocol = "2025-03-26"
const maxFrame = 1 << 20

var errInvalid = errors.New("invalid or unavailable MCP server")
var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Config is trusted process configuration, never model input. Only explicitly
// allowlisted tools are exposed. Effect calls use never: standard MCP provides
// no authoritative idempotency/query contract.
type Config struct {
	Name        string            `yaml:"name"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	Directory   string            `yaml:"directory"`
	Tools       map[string]string `yaml:"tools"` // read_only or external_effect
	Description string            `yaml:"description,omitempty"`
}

func (c Config) Validate() error {
	if !identifier.MatchString(c.Name) || strings.Contains(c.Name, "__") || !filepath.IsAbs(c.Command) || !filepath.IsAbs(c.Directory) || len(c.Tools) == 0 || len(c.Tools) > 4096 || len(c.Args) > 32 || len(c.Description) > 4096 {
		return errInvalid
	}
	for name, permission := range c.Tools {
		if !identifier.MatchString(name) || strings.Contains(name, "__") || (permission != "read_only" && permission != "external_effect") {
			return errInvalid
		}
	}
	return nil
}

type advertised struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type session struct {
	cmd          *exec.Cmd
	in           io.WriteCloser
	frames       *bufio.Scanner
	id           int
	stopRead     func() bool
	instructions string
}

func start(ctx context.Context, c Config) (*session, error) {
	dir, err := os.MkdirTemp(c.Directory, ".steward-mcp-")
	if err != nil {
		return nil, errInvalid
	}
	cmd, err := sandbox.Command(ctx, c.Command, c.Args, nil, c.Directory, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, errInvalid
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Dir = dir
	// No inherited auth, proxy, shell initialization or user HOME.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir, "LANG=C.UTF-8"}
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		os.RemoveAll(dir)
		return nil, errInvalid
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		os.RemoveAll(dir)
		return nil, errInvalid
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		os.RemoveAll(dir)
		return nil, errInvalid
	}
	s := &session{cmd: cmd, in: in, frames: bufio.NewScanner(out)}
	// Closing the read side also bounds a misbehaving descendant retaining
	// stdout after its server process was cancelled.
	s.stopRead = context.AfterFunc(ctx, func() { _ = out.Close() })
	s.frames.Buffer(make([]byte, 4096), maxFrame)
	var init struct {
		Protocol     string `json:"protocolVersion"`
		Instructions string `json:"instructions"`
		Capabilities struct {
			Tools *json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if s.call("initialize", map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "steward", "version": "1"}}, &init) != nil || init.Protocol != protocol || init.Capabilities.Tools == nil {
		s.close()
		return nil, errInvalid
	}
	s.instructions = init.Instructions
	if json.NewEncoder(in).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}) != nil {
		s.close()
		return nil, errInvalid
	}
	return s, nil
}
func (s *session) close() {
	s.stopRead()
	s.in.Close()
	// These are private, per-operation server processes. Always collect them.
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	_ = s.cmd.Wait()
	_ = os.RemoveAll(s.cmd.Dir)
}
func (s *session) call(method string, params any, result any) error {
	s.id++
	if json.NewEncoder(s.in).Encode(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params}) != nil {
		return errInvalid
	}
	for n := 0; n < 256 && s.frames.Scan(); n++ {
		var frame struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal(s.frames.Bytes(), &frame) != nil || frame.JSONRPC != "2.0" {
			return errInvalid
		}
		if len(frame.ID) == 0 && frame.Method != "" {
			continue
		}
		var id int
		if frame.Method != "" || json.Unmarshal(frame.ID, &id) != nil || id != s.id || len(frame.Error) > 0 || len(frame.Result) == 0 || json.Unmarshal(frame.Result, result) != nil {
			return errInvalid
		}
		return nil
	}
	return errInvalid
}

// Startup captures authoritative schema facts before default plugin selection.
// This collector is not a Run's method selection or result presentation.
func (s *session) list() ([]advertised, error) {
	var all []advertised
	cursor := ""
	catalog := catalogFacts{seen: map[string]bool{}}
	for page := 0; page < 16; page++ {
		items, next, err := s.listPage(cursor)
		if err != nil || catalog.accept(items) != nil {
			return nil, errInvalid
		}
		all = append(all, items...)
		if next == "" {
			sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
			return all, nil
		}
		if next == cursor {
			return nil, errInvalid
		}
		cursor = next
	}
	return nil, errInvalid
}

type catalogFacts struct{ seen map[string]bool }

func (c *catalogFacts) accept(items []advertised) error {
	for _, a := range items {
		if !identifier.MatchString(a.Name) || c.seen[a.Name] || len(a.Description) > 4096 || len(a.InputSchema) > 65536 {
			return errInvalid
		}
		c.seen[a.Name] = true
		if len(c.seen) > 4096 {
			return errInvalid
		}
	}
	return nil
}
func (s *session) listPage(cursor string) ([]advertised, string, error) {
	var response struct {
		Tools []advertised `json:"tools"`
		Next  string       `json:"nextCursor"`
	}
	params := map[string]string{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	if s.call("tools/list", params, &response) != nil {
		return nil, "", errInvalid
	}
	return response.Tools, response.Next, nil
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func Definitions(ctx context.Context, c Config) ([]tool.Definition, error) {
	if c.Validate() != nil {
		return nil, errInvalid
	}
	var err error
	c.Directory, err = filepath.EvalSymlinks(c.Directory)
	if err != nil {
		return nil, errInvalid
	}
	c.Command, err = filepath.EvalSymlinks(c.Command)
	if err != nil {
		return nil, errInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s, err := start(ctx, c)
	if err != nil {
		return nil, errInvalid
	}
	defer s.close()
	all, err := s.list()
	if err != nil {
		return nil, errInvalid
	}
	var defs []tool.Definition
	namespaceSource := c.Description
	if namespaceSource == "" {
		namespaceSource = s.instructions
	}
	for _, a := range all {
		permission, ok := c.Tools[a.Name]
		if !ok {
			continue
		}
		// MCP Tool inputSchema must describe an object. The common registry
		// also supports other schemas for trusted builtins, so enforce this
		// protocol-specific constraint at the adapter boundary.
		var input struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(a.InputSchema, &input) != nil || input.Type != "object" {
			return nil, errInvalid
		}
		spec := tool.Spec{Name: "mcp__" + c.Name + "__" + a.Name, Description: a.Description, ResourceHash: hash(struct {
			Config any
			Tool   advertised
		}{executionConfig(c), a}), Version: 1, InputSchema: string(a.InputSchema), Permission: permission, ReplayPolicy: "safe", Risk: "low", Provider: "mcp:" + c.Name, TimeoutMS: 10000, OutputBytes: 65536, ConflictPolicy: "server-v1", Exposure: tool.CodeMode, CodeModeOnly: true, NamespaceDescription: namespaceDescription(c.Description, s.instructions)}

		d := tool.Definition{Spec: spec, NamespaceSource: &namespaceSource, ConflictKeys: func(string) []string { return []string{"mcp:" + c.Name} }}
		if permission == "read_only" {
			d.Execute = func(ctx context.Context, args string) tool.Result { r, _ := invoke(ctx, c, a, args); return r }
		} else {
			d.Spec.ReplayPolicy = "never"
			d.Spec.Risk = "high"
			d.Spec.Description += " Host policy: external effect; exact frozen approval required. UNKNOWN_SIDE_EFFECT must not be retried; report uncertainty. No standard MCP receipt query is available."
			d.Invoke = func(ctx context.Context, r tool.Request) tool.Outcome {
				result, ambiguous := invoke(ctx, c, a, r.Arguments)
				return tool.Outcome{Result: result, Ambiguous: ambiguous}
			}
		}
		defs = append(defs, d)
	}
	if len(defs) != len(c.Tools) {
		return nil, errInvalid
	}
	if _, err = tool.NewRegistry(defs); err != nil {
		return nil, errInvalid
	}
	return defs, nil
}
