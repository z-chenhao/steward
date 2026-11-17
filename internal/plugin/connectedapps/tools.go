// Package connectedapps exposes explicitly connected accounts through Steward's
// existing tool executor. Composio metadata is untrusted discovery data.
package connectedapps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	apps "github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Configured MCP names cannot contain a second separator inside their tool
// name. This prefix therefore cannot capture a user's apps_* MCP server when
// refreshing the native connected-app group.
const Prefix = contract.ToolPrefix

func Definitions(ctx context.Context, service *apps.Service) ([]tool.Definition, error) {
	state, err := service.State(ctx)
	if err != nil {
		return nil, err
	}
	var definitions []tool.Definition
	for _, c := range state.Connections {
		if c.Status != "active" {
			continue
		}
		id, hash := c.ID, apps.BindingHash(state, c)
		name := Prefix + strings.ReplaceAll(id, "-", "") + "__"
		label := c.Name + " / " + c.Alias
		guidance := guidanceLegacyV1(label)
		base := tool.Spec{Version: 1, ResourceHash: hash, Provider: "composio", TimeoutMS: 60000, OutputBytes: 65536, Exposure: tool.CodeMode, NamespaceDescription: guidance.namespace}
		search := base
		search.Name = name + "search_tools"
		search.Description = guidance.search
		search.Permission, search.ReplayPolicy = "read_only", "safe"
		search.InputSchema = `{"type":"object","properties":{"query":{"type":"string","maxLength":200},"cursor":{"type":"string","maxLength":4096}},"required":["query"],"additionalProperties":false}`
		definitions = append(definitions, tool.Definition{Spec: search, Execute: func(ctx context.Context, args string) tool.Result {
			return invoke(ctx, service, id, hash, search.Name, args).Result
		}})
		execute := base
		execute.Name = name + "execute"
		execute.Description = guidance.execute
		execute.Permission, execute.Risk, execute.ReplayPolicy = "external_effect", "high", "never"
		execute.ConflictPolicy = "connected-app-v1"
		execute.InputSchema = `{"type":"object","properties":{"tool_slug":{"type":"string","minLength":1,"maxLength":160},"version":{"type":"string","minLength":1,"maxLength":160},"descriptor_hash":{"type":"string","minLength":64,"maxLength":64},"arguments_json":{"type":"string","minLength":2,"maxLength":65536}},"required":["tool_slug","version","descriptor_hash","arguments_json"],"additionalProperties":false}`
		definitions = append(definitions, tool.Definition{Spec: execute, ConflictKeys: func(string) []string { return []string{"connected-app:" + id} }, Invoke: func(ctx context.Context, request tool.Request) tool.Outcome {
			return invoke(ctx, service, id, hash, execute.Name, request.Arguments)
		}})
	}
	return definitions, nil
}
func failure(err error) tool.Result {
	if errors.Is(err, apps.ErrDenied) {
		return tool.Failure("PERMISSION_DENIED")
	}
	if errors.Is(err, apps.ErrInvalid) {
		return tool.Failure("INVALID_ARGUMENT")
	}
	return tool.Failure("PROVIDER_UNAVAILABLE")
}
func invoke(ctx context.Context, service *apps.Service, id, hash, name, arguments string) tool.Outcome {
	response, err := service.Method(ctx, id, hash, name, arguments)
	if err != nil {
		if response.Ambiguous {
			return tool.Outcome{Result: tool.Failure("UNKNOWN_SIDE_EFFECT"), Ambiguous: true}
		}
		return tool.Outcome{Result: failure(err)}
	}
	result := tool.Result{Text: string(response.JSON)}
	if response.Plan.Mode == "artifact" {
		result.Text = ""
		result.Output = &tool.BinaryOutput{Name: response.Plan.Name, MediaType: "application/json", Reader: bytes.NewReader(response.JSON), ResultJSON: func(ref string) string {
			encoded, _ := json.Marshal(map[string]string{"artifact_ref": ref, "message": response.Plan.Message})
			return string(encoded)
		}}
	}
	if response.Managed {
		result.HostFence = apps.ManagedReceipt{}
	}
	return tool.Outcome{Result: result}
}
