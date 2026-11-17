package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	mcpfake "github.com/z-chenhao/assistant/internal/tool/mcp/fake"
)

// The owner-side runner deliberately bypasses candidate evaluation. An accepted
// or hostile method still cannot dispatch against an invalid rediscovered peer.
func TestMCPManagedCatalogBoundaryRejectsInvalidRediscoveryBeforeEffect(t *testing.T) {
	type page struct {
		Tools []advertised `json:"tools"`
		Next  string       `json:"nextCursor"`
	}
	catalog := func(pages, count int) map[string]page {
		out := map[string]page{}
		for n := 0; n < pages; n++ {
			key := ""
			if n > 0 {
				key = fmt.Sprintf("page-%d", n)
			}
			p := page{Tools: []advertised{}}
			if n < pages-1 {
				p.Next = fmt.Sprintf("page-%d", n+1)
			}
			for index := n * count / pages; index < (n+1)*count/pages; index++ {
				name := fmt.Sprintf("tool_%04d", index)
				if index == count-1 {
					name = "send"
				}
				p.Tools = append(p.Tools, advertised{Name: name, Description: "original catalog entry", InputSchema: json.RawMessage(mcpfake.Schema)})
			}
			out[key] = p
		}
		return out
	}
	for _, mode := range []string{"valid", "seventeenth-page", "extra-tool", "duplicate", "repeated-cursor", "schema-drift", "oversize-schema", "oversize-description", "invalid-name"} {
		t.Run(mode, func(t *testing.T) {
			c := config(t)
			c.Tools = map[string]string{"send": "external_effect"}
			write := func(pages map[string]page) {
				t.Helper()
				raw, err := json.Marshal(pages)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(c.Directory, "mcp-pages.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(catalog(16, 4096))
			defs, err := Definitions(context.Background(), c)
			if err != nil || len(defs) != 1 {
				t.Fatal("original maximum catalog was not captured", len(defs), err)
			}
			registry, err := tool.NewRegistry(defs)
			if err != nil {
				t.Fatal(err)
			}
			send, ok := registry.Resolve("mcp__fixture__send")
			if !ok {
				t.Fatal("original allowed tool missing")
			}
			pages := catalog(16, 4096)
			last := pages["page-15"]
			switch mode {
			case "seventeenth-page":
				// Keep the total within 4096 to isolate the page ceiling.
				pages = catalog(17, 4096)
			case "extra-tool":
				// Keep 16 pages to isolate the tool ceiling.
				pages = catalog(16, 4097)
			case "duplicate":
				last.Tools[0].Name = "tool_0000"
			case "repeated-cursor":
				last.Next = "page-15"
			case "schema-drift":
				last.Tools[len(last.Tools)-1].InputSchema = json.RawMessage(strings.ReplaceAll(mcpfake.Schema, `"maxLength":4096`, `"maxLength":4095`))
			case "oversize-schema":
				last.Tools[0].InputSchema = json.RawMessage(`{"type":"object","description":"` + strings.Repeat("s", 65536) + `"}`)
			case "oversize-description":
				last.Tools[0].Description = strings.Repeat("d", 4097)
			case "invalid-name":
				last.Tools[0].Name = "invalid/name"
			}
			if mode != "seventeenth-page" && mode != "extra-tool" {
				pages["page-15"] = last
			}
			write(pages)
			calls := 0
			runner := MethodRunner(func(ctx context.Context, _, _ string, source func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error) {
				cursor := ""
				for n := 0; n <= 16; n++ {
					reply, err := source(ctx, fmt.Sprintf("list:%d", n), contract.SourceRequest{Phase: "list", Cursor: cursor})
					if err != nil {
						return contract.Plan{}, err
					}
					if reply.NextCursor == "" {
						break
					}
					cursor = reply.NextCursor
				}
				calls++
				facts, err := source(ctx, "call", contract.SourceRequest{Phase: "call"})
				if err != nil {
					return contract.Plan{}, err
				}
				return contract.Plan{Parts: []contract.Part{{Index: 0, End: len(facts.Text[0])}}}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			got := send.Invoke(WithMethod(ctx, runner), tool.Request{Arguments: `{"text":"original catalog effect"}`})
			raw, effectErr := os.ReadFile(filepath.Join(c.Directory, "mcp-effects.jsonl"))
			if mode == "valid" {
				if got.Ambiguous || got.Result.Error != nil || got.Result.Text != "original catalog effect" || effectErr != nil || strings.Count(string(raw), "original catalog effect") != 1 || calls != 1 {
					t.Fatal("original maximum catalog could not dispatch exactly once", got, string(raw), effectErr, calls)
				}
			} else {
				if !os.IsNotExist(effectErr) || got.Ambiguous || got.Result.Error == nil || got.Result.Text != "" {
					t.Fatal("invalid catalog reached an external effect", mode, got, string(raw), effectErr)
				}
				if mode == "schema-drift" && (calls != 1 || got.Result.Error.Code != "NON_DETERMINISTIC_REPLAY") {
					t.Fatal("original schema mismatch lost its replay diagnostic", calls, got)
				}
				if mode != "schema-drift" && calls != 0 {
					t.Fatal("invalid catalog was treated as complete", calls)
				}
			}
		})
	}
}
