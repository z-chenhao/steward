package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	mcpfake "github.com/z-chenhao/assistant/internal/tool/mcp/fake"
)

// These hostile owner-side runners deliberately bypass candidate evaluation.
// They prove that a permissive policy cannot widen the real peer port.
func TestMCPManagedHostRejectsEarlyDispatchChangedCursorAndForgedResult(t *testing.T) {
	for _, mode := range []string{"call-first", "changed-cursor", "repeat-call", "forged-part", "split-utf8", "method-failure", "missing-method", "drift", "valid"} {
		t.Run(mode, func(t *testing.T) {
			c := config(t)
			defs, err := Definitions(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			registry, _ := tool.NewRegistry(defs)
			send, _ := registry.Resolve("mcp__fixture__send")
			if mode == "drift" {
				catalog, _ := json.Marshal(map[string]any{"tools": []advertised{{Name: "read", Description: "Read structured fixture data", InputSchema: json.RawMessage(mcpfake.Schema)}, {Name: "send", Description: "Changed original send tool", InputSchema: json.RawMessage(mcpfake.Schema)}}})
				if err := os.WriteFile(filepath.Join(c.Directory, "mcp-tools.json"), catalog, 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := MethodRunner(func(ctx context.Context, name, args string, source func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error) {
				if name != "mcp__fixture__send" || args != `{"text":"原文"}` {
					t.Fatal("original accepted scope changed", name, args)
				}
				if mode == "call-first" {
					_, err := source(ctx, "call", contract.SourceRequest{Phase: "call"})
					return contract.Plan{}, err
				}
				q := contract.SourceRequest{Phase: "list"}
				if mode == "changed-cursor" {
					q.Cursor = "candidate-controlled"
				}
				if _, err := source(ctx, "list:0", q); err != nil {
					return contract.Plan{}, err
				}
				facts, err := source(ctx, "call", contract.SourceRequest{Phase: "call"})
				if err != nil {
					return contract.Plan{}, err
				}
				if mode == "repeat-call" {
					_, err := source(ctx, "call-again", contract.SourceRequest{Phase: "call"})
					return contract.Plan{}, err
				}
				if mode == "method-failure" {
					return contract.Plan{}, errors.New("candidate failed after source")
				}
				part := contract.Part{Index: 0, End: len(facts.Text[0])}
				if mode == "forged-part" {
					part.Index = 99
				}
				if mode == "split-utf8" {
					part.Start = 1
				}
				return contract.Plan{Parts: []contract.Part{part}}, nil
			})
			if mode == "missing-method" {
				runner = nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got := send.Invoke(WithMethod(ctx, runner), tool.Request{Arguments: `{"text":"原文"}`})
			beforeEffect := mode == "call-first" || mode == "changed-cursor" || mode == "missing-method" || mode == "drift"
			raw, err := os.ReadFile(filepath.Join(c.Directory, "mcp-effects.jsonl"))
			if beforeEffect {
				if !os.IsNotExist(err) || got.Ambiguous || got.Result.Error == nil {
					t.Fatal("unauthorized peer dispatch", string(raw), err, got)
				}
				if mode == "drift" && got.Result.Error.Code != "NON_DETERMINISTIC_REPLAY" {
					t.Fatal("original drift diagnostic changed", got)
				}
			} else if err != nil || strings.Count(string(raw), "原文") != 1 {
				t.Fatal("source was not dispatched exactly once", string(raw), err)
			}
			if mode == "valid" {
				if got.Ambiguous || got.Result.Error != nil || got.Result.Text != "原文" {
					t.Fatal(got)
				}
				if _, ok := got.Result.HostFence.(ManagedReceipt); !ok {
					t.Fatal("managed result lacks final transaction fence")
				}
			} else if !beforeEffect && (!got.Ambiguous || got.Result.Text != "" || got.Result.Error == nil) {
				t.Fatal("post-dispatch method failure invented certainty", got)
			}
		})
	}
}
