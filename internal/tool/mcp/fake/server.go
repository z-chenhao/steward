// Package fake implements an explicit stdio MCP peer for deterministic tests.
package fake

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const Schema = `{"type":"object","properties":{"text":{"type":"string","description":"Message text","maxLength":4096},"behavior":{"enum":["success","unknown","error","hold"]}},"required":["text"],"additionalProperties":false}`

func Serve(in io.Reader, out io.Writer, root string) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return io.ErrUnexpectedEOF
		}
		if request.ID == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}
			if data, err := os.ReadFile(filepath.Join(root, "mcp-initialize.json")); err == nil {
				if !json.Valid(data) {
					return io.ErrUnexpectedEOF
				}
				result = json.RawMessage(data)
			} else if !os.IsNotExist(err) {
				return err
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "read", "description": "Read structured fixture data", "inputSchema": json.RawMessage(Schema)},
				map[string]any{"name": "send", "description": "Send a fixture message", "inputSchema": json.RawMessage(Schema), "annotations": map[string]bool{"readOnlyHint": true}},
			}}
			if catalog, err := os.ReadFile(filepath.Join(root, "mcp-tools.json")); err == nil {
				if !json.Valid(catalog) {
					return io.ErrUnexpectedEOF
				}
				result = json.RawMessage(catalog)
			}
			if data, err := os.ReadFile(filepath.Join(root, "mcp-pages.json")); err == nil {
				var pages map[string]json.RawMessage
				var params struct {
					Cursor string `json:"cursor"`
				}
				if json.Unmarshal(data, &pages) != nil || json.Unmarshal(request.Params, &params) != nil || len(pages[params.Cursor]) == 0 {
					return io.ErrUnexpectedEOF
				}
				result = pages[params.Cursor]
			} else if !os.IsNotExist(err) {
				return err
			}
		case "tools/call":
			var p struct {
				Name      string                          `json:"name"`
				Arguments struct{ Text, Behavior string } `json:"arguments"`
			}
			if json.Unmarshal(request.Params, &p) != nil {
				return io.ErrUnexpectedEOF
			}
			if p.Name == "read" {
				if data, err := os.ReadFile(filepath.Join(root, "mcp-data.json")); err == nil {
					var values map[string]json.RawMessage
					if json.Unmarshal(data, &values) != nil {
						return io.ErrUnexpectedEOF
					}
					if value, ok := values[p.Arguments.Text]; ok {
						p.Arguments.Text = string(value)
					}
				}
			}
			if p.Arguments.Behavior == "hold" {
				if _, err := os.Stat(filepath.Join(root, "hold-barrier")); err == nil {
					if err := os.WriteFile(filepath.Join(root, "hold-accepted"), []byte("accepted"), 0600); err != nil {
						return err
					}
					deadline := time.Now().Add(time.Minute)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(filepath.Join(root, "hold-release")); err == nil {
							break
						}
						time.Sleep(5 * time.Millisecond)
					}
				} else {
					time.Sleep(time.Minute)
				}
			}
			if p.Name == "send" {
				f, err := os.OpenFile(filepath.Join(root, "mcp-effects.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				err = json.NewEncoder(f).Encode(p.Arguments)
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				if p.Arguments.Behavior == "unknown" {
					return nil
				}
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": p.Arguments.Text}}, "isError": p.Arguments.Behavior == "error"}
			// Host-owned fault fixture. Override only the post-dispatch reply,
			// after recording the actual effect; discovery remains unchanged.
			if reply, err := os.ReadFile(filepath.Join(root, "mcp-result.json")); err == nil {
				result = json.RawMessage(reply)
			} else if !os.IsNotExist(err) {
				return err
			}
		default:
			return io.ErrUnexpectedEOF
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
