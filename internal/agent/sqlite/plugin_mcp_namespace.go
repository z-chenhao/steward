package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/mcpbridge/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// This is a pure, process-local projection cache for new snapshots, not plugin
// state or an execution journal. Old snapshots never consult it.
type mcpNamespaceCache struct {
	sync.Mutex
	graph, owner string
	epoch        int64
	text         map[string]string
}

type mcpNamespaceSource struct {
	key, text, toolName, provider, resource string
}

func (s mcpNamespaceSource) check(r *Runs) error {
	d, exists := r.registry.Resolve(s.toolName)
	if !exists || d.Spec.Provider != s.provider || d.Spec.ResourceHash != s.resource || d.NamespaceSource == nil || *d.NamespaceSource != s.text {
		return agent.ErrClaimLost
	}
	return nil
}

// The only consumer is MCP new-snapshot namespace projection. Returning this
// private signal rolls back the creation transaction before any candidate IO.
// The same callback is retried with the original admission privacy fence.
type mcpNamespaceNeed struct {
	graph   string
	privacy capabilityPrivacy
	sources []mcpNamespaceSource
}

func (*mcpNamespaceNeed) Error() string { return "MCP namespace projection required" }

func (r *Runs) mcpNamespaceSpecs(ctx context.Context, tx *sql.Tx, specs []tool.Spec) ([]tool.Spec, error) {
	sources := map[int]mcpNamespaceSource{}
	providers := map[string]mcpNamespaceSource{}
	for i, spec := range specs {
		if !strings.HasPrefix(spec.Name, "mcp__") || !strings.HasPrefix(spec.Provider, "mcp:") {
			continue
		}
		definition, exists := r.registry.Resolve(spec.Name)
		if !exists || definition.NamespaceSource == nil {
			continue // explicit non-MCP fixture/adapter, never discovered by MCP
		}
		if definition.Spec.Provider != spec.Provider || definition.Spec.ResourceHash != spec.ResourceHash {
			return nil, llm.ModelIncompatible
		}
		text := *definition.NamespaceSource
		source, found := providers[spec.Provider]
		if found && source.text != text {
			return nil, llm.ModelIncompatible
		}
		if !found {
			source = mcpNamespaceSource{key: digest(spec.Provider + "\x00" + text), text: text, toolName: spec.Name, provider: spec.Provider, resource: spec.ResourceHash}
			providers[spec.Provider] = source
		}
		sources[i] = source
	}
	if len(sources) == 0 {
		return specs, nil
	}
	managed, err := managedService(ctx, tx, contract.Namespace)
	if err != nil || !managed {
		return specs, err // exact pre-service/default21 interpretation
	}
	// This optional catalog contribution disappears on deactivation or absent
	// carrier. The mandatory method still fails closed at actual invocation.
	for i := range sources {
		specs[i].NamespaceDescription = ""
	}
	if r.plugins == nil {
		return specs, nil
	}
	var graph string
	var available, blocked bool
	err = tx.QueryRowContext(ctx, `SELECT coalesce(c.active_graph,''),EXISTS(
SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b
JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') s
WHERE g.revision=c.active_graph AND json_extract(s.value,'$.contract')=?),
c.stopped=1 OR EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=c.active_graph)
FROM rsi_control c WHERE c.singleton=1`, contract.Namespace).Scan(&graph, &available, &blocked)
	if err != nil {
		return nil, err
	}
	if !available || graph == "" {
		return specs, nil
	}
	if blocked {
		return nil, rsi.ErrRevoked
	}
	privacy, err := readCapabilityPrivacy(ctx, tx)
	if err != nil {
		return nil, err
	}
	need := &mcpNamespaceNeed{graph: graph, privacy: privacy}
	missing := map[string]bool{}
	cache := &r.mcpNamespaces
	cache.Lock()
	for i, source := range sources {
		value, found := cache.text[source.key]
		if cache.graph == graph && cache.owner == privacy.owner && cache.epoch == privacy.epoch && found {
			specs[i].NamespaceDescription = value
		} else if !missing[source.key] {
			missing[source.key] = true
			need.sources = append(need.sources, source)
		}
	}
	cache.Unlock()
	if len(need.sources) != 0 {
		return specs, need
	}
	return specs, nil
}

func (r *Runs) prepareMCPNamespaces(ctx context.Context, need *mcpNamespaceNeed) error {
	if r.plugins == nil {
		return rsi.ErrNotFound
	}
	host := *r.plugins
	store := r.host.Plugins()
	host.Store = store
	for _, source := range need.sources {
		store.Fence = func(ctx context.Context, tx *sql.Tx) error {
			if err := need.privacy.check(ctx, tx); err != nil {
				return err
			}
			return source.check(r)
		}
		r.host.boundary("before_mcp_namespace_projection")
		input, _ := json.Marshal(contract.NamespaceRequest{InputID: "source"})
		raw, err := host.ReadService(ctx, need.graph, contract.Namespace, input, []rsi.InputData{{ID: "source", Data: []byte(source.text)}})
		r.host.boundary("after_mcp_namespace_projection")
		if err != nil {
			return err
		}
		if err := host.Store.CheckGraph(ctx, need.graph); err != nil {
			return err
		}
		var plan contract.NamespacePlan
		if rsiplugin.DecodeResult(raw, &plan) != nil || plan.Start < 0 || plan.End < plan.Start || plan.End > len(source.text) || !utf8.ValidString(source.text[plan.Start:plan.End]) || plan.Truncated && plan.End == len(source.text) {
			return rsi.ErrInvalid
		}
		text := source.text[plan.Start:plan.End]
		if plan.Truncated {
			text += "…"
		}
		// Use the actual CodeMode output ceiling, not the plugin's current
		// 512-rune policy, as the Host bound. All original views still fit.
		if len(text) > codemode.DefaultLimits().OutputBytes {
			return rsi.ErrInvalid
		}
		cache := &r.mcpNamespaces
		cache.Lock()
		if cache.graph != need.graph || cache.owner != need.privacy.owner || cache.epoch != need.privacy.epoch {
			cache.graph, cache.owner, cache.epoch = need.graph, need.privacy.owner, need.privacy.epoch
			cache.text = map[string]string{}
		}
		cache.text[source.key] = text
		cache.Unlock()
	}
	return nil
}
