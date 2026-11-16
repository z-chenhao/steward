package mcp

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/tool"
	"strings"
)

// Exact pre-service interpreter for frozen Runs, existing default21 selections
// and explicitly unbundled adapters. Never a managed-error fallback. Remove
// once those consumers no longer require its original result interpretation.
// invoke reopens the sandboxed peer and verifies the frozen tool before dispatch.
// The boolean marks an ambiguous reply after dispatch, never permission to retry.
func invokeLegacyV1(ctx context.Context, c Config, a advertised, args string) (tool.Result, bool) {
	peer, err := start(ctx, c)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), false
	}
	defer peer.close()
	current, err := peer.list()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), false
	}
	matched := false
	for _, v := range current {
		if v.Name == a.Name {
			matched = hash(v) == hash(a)
		}
	}
	if !matched {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), false
	}
	var params any
	if json.Unmarshal([]byte(args), &params) != nil {
		return tool.Failure("INVALID_ARGUMENT"), false
	}
	return peer.callTool(a.Name, params)
}

// callTool retains V1's exact concatenation and error interpretation. Shared
// untrusted reply validation is a Host mechanism, not a second business policy.
func (s *session) callTool(name string, params any) (tool.Result, bool) {
	facts, valid := s.callToolFacts(name, params)
	if !valid {
		return tool.Failure("PROVIDER_UNAVAILABLE"), true
	}
	if facts.IsError {
		return tool.Failure("INVALID_ARGUMENT"), false
	}
	return tool.Result{Text: strings.Join(facts.Text, "")}, false
}
