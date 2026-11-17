package connectedapps

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
)

// Exact pre-method workflow/presentation for frozen Run/Child, explicit prior
// compositions that never activated Apps methods, and unbundled adapters. This
// is selected only by absence of the immutable managed boundary, never after a
// managed error. Remove after the inventory proves no such consumers remain.
func (s *Service) methodLegacyV1(ctx context.Context, id, hash, operation string, search searchArguments, execute executeArguments) (MethodOutput, error) {
	var result MethodOutput
	var err error
	name := "app-tools.json"
	if operation == "search" {
		var found composio.Tools
		found, err = s.SearchTools(ctx, id, hash, search.Query, search.Cursor)
		if err == nil {
			result.JSON, err = marshalTools(found)
		}
	} else {
		name = "app-result.json"
		var executed composio.Execution
		executed, err = s.Execute(ctx, id, hash, execute.Slug, execute.Version, execute.Hash, json.RawMessage(execute.Arguments))
		if err == nil {
			result.JSON, err = json.Marshal(executed)
		}
	}
	if err != nil {
		result.JSON = nil
		result.Ambiguous = composio.Uncertain(err)
		return result, err
	}
	mode := "inline"
	if len(result.JSON) > 60000 {
		mode = "artifact"
	}
	result.Plan = contract.Plan{Mode: mode, Name: name, Message: "The complete app response is in this artifact."}
	return result, nil
}

// SearchTools is the original pre-method/direct API recipe. Managed methods
// use the invocation-local page port; this is never an activated fallback.
func (s *Service) SearchTools(ctx context.Context, id, hash, query, cursor string) (composio.Tools, error) {
	if len(query) > 200 || len(cursor) > 4096 {
		return composio.Tools{}, ErrInvalid
	}
	state, c, err := s.active(ctx, id, hash)
	if err != nil {
		return composio.Tools{}, err
	}
	result, err := s.client(state).SearchTools(ctx, c.Toolkit, query, cursor)
	if err != nil {
		return composio.Tools{}, ErrUnavailable
	}
	return filterTools(result, c), nil
}

// Execute preserves the exact pre-method workflow for direct/unbundled and
// pre-service Tool consumers. Managed methods use the same fixed validation
// helpers through their own closed phases, and never call this legacy workflow.
func (s *Service) Execute(ctx context.Context, id, hash, slug, version, descriptor string, args json.RawMessage) (composio.Execution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(args) > 65536 || !json.Valid(args) || !strings.HasPrefix(strings.TrimSpace(string(args)), "{") {
		return composio.Execution{}, ErrInvalid
	}
	state, c, err := s.active(ctx, id, hash)
	if err != nil {
		return composio.Execution{}, err
	}
	client := s.client(state)
	if err = checkExecutionAccount(ctx, client, state, c); err != nil {
		return composio.Execution{}, err
	}
	if err = checkExecutionDescriptor(ctx, client, c, slug, version, descriptor); err != nil {
		return composio.Execution{}, err
	}
	return client.Execute(ctx, state.UserID, c.AccountID, slug, version, args)
}
