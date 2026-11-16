package connectedapps

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
)

// MethodRunner is supplied only after the original Tool is durably started.
// Its source is a closure for that exact account and accepted arguments.
type MethodRunner func(context.Context, string, string, contract.Request, func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error)
type methodKey struct{}

func WithMethod(ctx context.Context, runner MethodRunner) context.Context {
	return context.WithValue(ctx, methodKey{}, runner)
}

// ManagedReceipt is local completion proof; the original Tool transaction must
// still check the frozen graph and current connection before publishing bytes.
type ManagedReceipt struct{}

type MethodOutput struct {
	JSON      []byte
	Plan      contract.Plan
	Ambiguous bool
	Managed   bool
}

type searchArguments struct {
	Query  string `json:"query"`
	Cursor string `json:"cursor"`
}

type executeArguments struct {
	Slug      string `json:"tool_slug"`
	Version   string `json:"version"`
	Hash      string `json:"descriptor_hash"`
	Arguments string `json:"arguments_json"`
}

func decodeMethodArguments(raw string, out any) bool {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil && decoder.Decode(new(any)) == io.EOF
}

// Method drives the isolated business method using closed, invocation-local
// phases. The method never receives the original body, account identity,
// credentials, descriptor, query, cursor or approved effect arguments.
func (s *Service) Method(ctx context.Context, id, hash, name, arguments string) (MethodOutput, error) {
	var result MethodOutput
	runner, selected := ctx.Value(methodKey{}).(MethodRunner)
	if selected && runner == nil {
		return result, ErrUnavailable
	}
	prefix := contract.ToolPrefix + strings.ReplaceAll(id, "-", "") + "__"
	operation := ""
	var search searchArguments
	var execute executeArguments
	switch name {
	case prefix + "search_tools":
		operation = "search"
		if !decodeMethodArguments(arguments, &search) || len(search.Query) > 200 || len(search.Cursor) > 4096 {
			return result, ErrInvalid
		}
	case prefix + "execute":
		operation = "execute"
		if !decodeMethodArguments(arguments, &execute) || len(execute.Arguments) > 65536 || !json.Valid([]byte(execute.Arguments)) || !strings.HasPrefix(strings.TrimSpace(execute.Arguments), "{") {
			return result, ErrInvalid
		}
	default:
		return result, ErrDenied
	}
	if !selected {
		return s.methodLegacyV1(ctx, id, hash, operation, search, execute)
	}
	result.Managed = true
	s.mu.RLock()
	defer s.mu.RUnlock()
	var state State
	var connection Connection
	var sourceErr error
	bound, account, descriptor, complete, dispatched := false, false, false, false, false
	used := map[string]bool{}
	source := func(ctx context.Context, key string, input contract.SourceRequest) (contract.SourceReply, error) {
		facts := contract.SourceReply{}
		if key != input.Phase || used[input.Phase] || complete || input.Limit < 0 || input.Limit > 1000 || input.Phase != "search" && input.Limit != 0 {
			return facts, ErrInvalid
		}
		used[input.Phase] = true
		if input.Phase == "binding" {
			if bound {
				return facts, ErrInvalid
			}
			state, connection, sourceErr = s.active(ctx, id, hash)
			if sourceErr != nil {
				return facts, sourceErr
			}
			bound = true
			facts.NeedsAccount = !connection.Public
			facts.HasCursor = search.Cursor != ""
			return facts, nil
		}
		if !bound {
			return facts, ErrDenied
		}
		// The local mutex cannot fence a second process changing the same store.
		if _, _, err := s.active(ctx, id, hash); err != nil {
			sourceErr = err
			return facts, err
		}
		client := s.client(state)
		switch input.Phase {
		case "search":
			if operation != "search" || search.Cursor == "" && input.Limit == 0 || search.Cursor != "" && input.Limit != 0 {
				return facts, ErrDenied
			}
			var found composio.Tools
			found, sourceErr = client.SearchToolsPage(ctx, connection.Toolkit, search.Query, search.Cursor, input.Limit)
			if sourceErr != nil {
				sourceErr = ErrUnavailable
			}
			if sourceErr == nil {
				result.JSON, sourceErr = marshalTools(filterTools(found, connection))
			}
		case "account":
			if operation != "execute" || connection.Public {
				return facts, ErrDenied
			}
			sourceErr = checkExecutionAccount(ctx, client, state, connection)
			account = sourceErr == nil
			return facts, sourceErr
		case "descriptor":
			if operation != "execute" {
				return facts, ErrDenied
			}
			sourceErr = checkExecutionDescriptor(ctx, client, connection, execute.Slug, execute.Version, execute.Hash)
			descriptor = sourceErr == nil
			return facts, sourceErr
		case "execute":
			if operation != "execute" || !descriptor || !connection.Public && !account || dispatched {
				return facts, ErrDenied
			}
			dispatched = true
			var executed composio.Execution
			executed, sourceErr = client.Execute(ctx, state.UserID, connection.AccountID, execute.Slug, execute.Version, json.RawMessage(execute.Arguments))
			if sourceErr == nil {
				result.JSON, sourceErr = json.Marshal(executed)
			}
		default:
			return facts, ErrInvalid
		}
		if sourceErr != nil {
			return facts, sourceErr
		}
		complete = true
		facts.Bytes = len(result.JSON)
		return facts, nil
	}
	plan, err := runner(ctx, name, arguments, contract.Request{Operation: operation}, source)
	if sourceErr != nil {
		result.JSON = nil
		result.Ambiguous = composio.Uncertain(sourceErr)
		return result, sourceErr
	}
	if err == nil && (!complete || !validMethodPlan(plan, len(result.JSON))) {
		err = ErrUnavailable
	}
	if err == nil {
		_, _, err = s.active(ctx, id, hash)
	}
	if err != nil {
		result.JSON = nil
		result.Ambiguous = dispatched
		return result, err
	}
	result.Plan = plan
	return result, nil
}

func validMethodPlan(plan contract.Plan, bytes int) bool {
	return (plan.Mode == "artifact" || plan.Mode == "inline" && bytes <= 65536) && len(plan.Name) > 0 && len(plan.Name) <= 160 && plan.Name != "." && plan.Name != ".." && !strings.ContainsAny(plan.Name, "/\\\x00") && utf8.ValidString(plan.Name) && len(plan.Message) <= 16384 && utf8.ValidString(plan.Message)
}

// marshalTools encodes original descriptor facts once for both historical and
// managed consumers. Hashes and schemas are authoritative Host output.
func marshalTools(found composio.Tools) ([]byte, error) {
	type descriptor struct {
		composio.Tool
		Hash string `json:"descriptor_hash"`
	}
	items := make([]descriptor, 0, len(found.Items))
	for _, item := range found.Items {
		items = append(items, descriptor{item, ToolHash(item)})
	}
	return json.Marshal(struct {
		Items []descriptor `json:"items"`
		Next  string       `json:"next_cursor,omitempty"`
	}{items, found.NextCursor})
}

func checkExecutionAccount(ctx context.Context, client composio.Client, state State, connection Connection) error {
	if connection.Public {
		return nil
	}
	account, err := client.Account(ctx, connection.AccountID)
	if err != nil {
		return ErrUnavailable
	}
	if !validAccount(account, state, connection) || account.Status != "ACTIVE" {
		return ErrDenied
	}
	return nil
}

func checkExecutionDescriptor(ctx context.Context, client composio.Client, connection Connection, slug, version, descriptor string) error {
	item, err := client.Tool(ctx, slug, version)
	if err != nil {
		return ErrUnavailable
	}
	if item.Slug != slug || item.Version != version || item.Toolkit.Slug != connection.Toolkit || connection.Public && !item.NoAuth || item.Deprecated || ToolHash(item) != descriptor {
		return ErrDenied
	}
	return nil
}
