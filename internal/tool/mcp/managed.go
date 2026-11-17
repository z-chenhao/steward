package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

// MethodRunner is a private domain seam installed only by the accepted Tool
// store. The candidate never receives this Go closure or any execution token.
// The port is bound to the adapter's original configuration/tool/arguments.
type MethodRunner func(context.Context, string, string, func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error)
type methodKey struct{}

// ManagedReceipt is process-local proof that managed dispatch produced the
// result. The owning Tool transaction must recheck its original graph before
// publishing. It is never encoded in candidate input/output or stored as data.
type ManagedReceipt struct{}

func WithMethod(ctx context.Context, run MethodRunner) context.Context {
	return context.WithValue(ctx, methodKey{}, run)
}

func invoke(ctx context.Context, c Config, a advertised, args string) (tool.Result, bool) {
	method, managed := ctx.Value(methodKey{}).(MethodRunner)
	if !managed {
		return invokeLegacyV1(ctx, c, a, args)
	}
	// A selected managed method is mandatory, even if its carrier is absent.
	if method == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), false
	}
	var peer *session
	defer func() {
		if peer != nil {
			peer.close()
		}
	}()
	catalog := catalogFacts{seen: map[string]bool{}}
	page, cursor := 0, ""
	complete, matched, dispatched, replied := false, false, false, false
	code := "PROVIDER_UNAVAILABLE"
	var original contract.SourceReply
	port := func(ctx context.Context, key string, q contract.SourceRequest) (contract.SourceReply, error) {
		empty := contract.SourceReply{Text: []string{}}
		switch q.Phase {
		case "list":
			if complete || dispatched || page >= 16 || q.Cursor != cursor || key != fmt.Sprintf("list:%d", page) {
				return empty, errInvalid
			}
			if peer == nil {
				var err error
				peer, err = start(ctx, c)
				if err != nil {
					return empty, err
				}
			}
			items, next, err := peer.listPage(cursor)
			if err != nil || catalog.accept(items) != nil || next != "" && next == cursor {
				return empty, errInvalid
			}
			for _, current := range items {
				if current.Name == a.Name {
					matched = hash(current) == hash(a)
				}
			}
			page++
			cursor, complete = next, next == ""
			empty.NextCursor = next
			return empty, nil
		case "call":
			if key != "call" || q.Cursor != "" || !complete || dispatched || peer == nil {
				return empty, errInvalid
			}
			if !matched {
				code = "NON_DETERMINISTIC_REPLAY"
				return empty, errInvalid
			}
			var params any
			if json.Unmarshal([]byte(args), &params) != nil {
				code = "INVALID_ARGUMENT"
				return empty, errInvalid
			}
			// From this point only a valid original reply proves certainty.
			dispatched = true
			var valid bool
			original, valid = peer.callToolFacts(a.Name, params)
			if !valid {
				return empty, errInvalid
			}
			replied = true
			reply := original
			if reply.IsError {
				// The method needs error truth, not an untrusted diagnostic body.
				reply.Text = []string{}
			}
			return reply, nil
		}
		return empty, errInvalid
	}
	plan, err := method(ctx, "mcp__"+c.Name+"__"+a.Name, args, port)
	// A candidate cannot erase an already validated peer error by failing its
	// presentation step. Deadline/cancellation still follows original Tool rules.
	if replied && original.IsError && ctx.Err() == nil {
		return tool.Failure("INVALID_ARGUMENT"), false
	}
	if err != nil || !replied || ctx.Err() != nil {
		return tool.Failure(code), dispatched
	}
	if plan.Separator != "" && plan.Separator != "\n" && plan.Separator != "\n\n" || len(plan.Parts) > len(original.Text) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), dispatched
	}
	var text strings.Builder
	seen := map[int]bool{}
	for index, p := range plan.Parts {
		if p.Index < 0 || p.Index >= len(original.Text) || seen[p.Index] {
			return tool.Failure("PROVIDER_UNAVAILABLE"), dispatched
		}
		source := original.Text[p.Index]
		if p.Start < 0 || p.End < p.Start || p.End > len(source) || !utf8.ValidString(source[p.Start:p.End]) {
			return tool.Failure("PROVIDER_UNAVAILABLE"), dispatched
		}
		seen[p.Index] = true
		if index > 0 {
			text.WriteString(plan.Separator)
		}
		text.WriteString(source[p.Start:p.End])
	}
	if text.Len() > maxFrame {
		return tool.Failure("PROVIDER_UNAVAILABLE"), dispatched
	}
	return tool.Result{Text: text.String(), HostFence: ManagedReceipt{}}, false
}

// callToolFacts validates the original untrusted reply without accepting any
// candidate claim about content type, error status or effect certainty.
func (s *session) callToolFacts(name string, params any) (contract.SourceReply, bool) {
	out := contract.SourceReply{Text: []string{}}
	var reply *struct {
		Content *[]struct {
			Type string  `json:"type"`
			Text *string `json:"text"`
		} `json:"content"`
		IsError json.RawMessage `json:"isError"`
	}
	if s.call("tools/call", map[string]any{"name": name, "arguments": params}, &reply) != nil || reply == nil || reply.Content == nil {
		return out, false
	}
	if len(reply.IsError) > 0 {
		var flag *bool
		if json.Unmarshal(reply.IsError, &flag) != nil || flag == nil {
			return out, false
		}
		out.IsError = *flag
	}
	for _, part := range *reply.Content {
		if part.Type != "text" || part.Text == nil {
			return out, false
		}
		out.Text = append(out.Text, *part.Text)
	}
	return out, true
}
