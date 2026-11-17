package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only immutable pure wording is cached, never account facts or input text.
// Factual labels are inserted in the final capability creation transaction.
type appsGuidanceCache struct {
	sync.Mutex
	graph string
	plan  contract.GuidancePlan
}

type appsGuidanceNeed struct {
	graph   string
	privacy capabilityPrivacy
}

func (*appsGuidanceNeed) Error() string { return "Apps guidance projection required" }

func appsToolName(name string) bool {
	return strings.HasPrefix(name, contract.ToolPrefix) && (strings.HasSuffix(name, "__search_tools") || strings.HasSuffix(name, "__execute"))
}

func (r *Runs) appsGuidanceSpecs(ctx context.Context, tx *sql.Tx, specs []tool.Spec) ([]tool.Spec, error) {
	found := false
	for _, spec := range specs {
		found = found || appsToolName(spec.Name) && spec.Provider == "composio"
	}
	if !found {
		return specs, nil
	}
	managed, err := managedService(ctx, tx, contract.Guidance)
	if err != nil || !managed {
		return specs, err
	}
	// Missing/deactivated contributions disappear; they never restore native
	// wording. Actual execution remains governed by its mandatory method.
	for index := range specs {
		if appsToolName(specs[index].Name) && specs[index].Provider == "composio" {
			specs[index].Description, specs[index].NamespaceDescription = "", ""
		}
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
FROM rsi_control c WHERE c.singleton=1`, contract.Guidance).Scan(&graph, &available, &blocked)
	if err != nil {
		return nil, err
	}
	if !available || graph == "" {
		return specs, nil
	}
	if blocked {
		return nil, rsi.ErrRevoked
	}
	state, err := readAppsStateTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	// A different process can revoke the binding without refreshing this
	// Registry. New snapshots must not advertise that withdrawn authority.
	active := specs[:0]
	remaining := false
	for _, spec := range specs {
		if appsToolName(spec.Name) && spec.Provider == "composio" {
			if _, valid := appsConnectionForSpec(state, spec); !valid {
				continue
			}
			remaining = true
		}
		active = append(active, spec)
	}
	specs = active
	if !remaining {
		return specs, nil
	}
	privacy, err := readCapabilityPrivacy(ctx, tx)
	if err != nil {
		return nil, err
	}
	cache := &r.appsGuidance
	cache.Lock()
	plan, ready := cache.plan, cache.graph == graph
	cache.Unlock()
	if !ready {
		return specs, &appsGuidanceNeed{graph: graph, privacy: privacy}
	}
	for index, spec := range specs {
		if !appsToolName(spec.Name) || spec.Provider != "composio" {
			continue
		}
		connection, valid := appsConnectionForSpec(state, spec)
		if !valid {
			return nil, rsi.ErrRevoked
		}
		label := connection.Name + " / " + connection.Alias
		recipe := plan.Execute
		if strings.HasSuffix(spec.Name, "__search_tools") {
			recipe = plan.Search
		}
		description := recipe.Before + label + recipe.After
		namespace := plan.Namespace.Before + label + plan.Namespace.After
		// Keep the enclosing CodeMode's original1MiB transport ceiling. The
		// default recipe preserves original long labels instead of imposing a
		// new arbitrary label limit; existing aggregate runtime limits remain.
		if !utf8.ValidString(description) || !utf8.ValidString(namespace) || len(description) > 1<<20 || len(namespace) > 1<<20 {
			return nil, rsi.ErrInvalid
		}
		specs[index].Description, specs[index].NamespaceDescription = description, namespace
	}
	return specs, nil
}

func (r *Runs) prepareAppsGuidance(ctx context.Context, need *appsGuidanceNeed) error {
	if r.plugins == nil {
		return rsi.ErrNotFound
	}
	host := *r.plugins
	store := r.host.Plugins()
	store.Fence = need.privacy.check
	host.Store = store
	r.host.boundary("before_apps_guidance_projection")
	raw, err := host.ReadService(ctx, need.graph, contract.Guidance, json.RawMessage(`{}`), nil)
	r.host.boundary("after_apps_guidance_projection")
	if err != nil {
		return err
	}
	if err := host.Store.CheckGraph(ctx, need.graph); err != nil {
		return err
	}
	var plan contract.GuidancePlan
	if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
		return err
	}
	cache := &r.appsGuidance
	cache.Lock()
	cache.graph, cache.plan = need.graph, plan
	cache.Unlock()
	return nil
}

// Only Apps wording is a versioned presentation field. Restore that frozen
// field for the exact original account resource before ordinary Select checks
// every execution attribute. Generic Registry authority is not weakened.
func (r *Runs) appsRegistryForRun(ctx context.Context, claim agent.RunClaim, base *tool.Registry) (*tool.Registry, error) {
	found := false
	for _, spec := range base.Describe() {
		found = found || appsToolName(spec.Name) && spec.Provider == "composio"
	}
	if !found {
		return base, nil
	}
	managed, err := r.managedRunService(ctx, claim.RunID(), contract.Guidance)
	if err != nil || !managed {
		return base, err
	}
	var raw string
	if err := r.host.db.QueryRowContext(ctx, `SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, claim.RunID()).Scan(&raw); err != nil {
		return nil, err
	}
	var specs []tool.Spec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, err
	}
	frozen := map[string]tool.Spec{}
	for _, spec := range specs {
		if appsToolName(spec.Name) && spec.Provider == "composio" {
			frozen[spec.Name] = spec
		}
	}
	definitions := []tool.Definition{}
	for _, spec := range base.Describe() {
		definition, _ := base.Resolve(spec.Name)
		if original, exists := frozen[spec.Name]; exists && spec.Provider == original.Provider && spec.ResourceHash != "" && spec.ResourceHash == original.ResourceHash {
			definition.Spec.Description = original.Description
		}
		definitions = append(definitions, definition)
	}
	return tool.NewRegistry(definitions)
}
