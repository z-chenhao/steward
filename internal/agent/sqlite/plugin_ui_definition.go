package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type uiDefinitionCache struct {
	sync.Mutex
	graph      string
	definition contract.Definition
	digest     string
	summary    *skill.Summary
}
type uiDefinitionNeed struct {
	graph   string
	privacy capabilityPrivacy
}

func (*uiDefinitionNeed) Error() string { return "UI definition projection required" }

func (r *Runs) uiDefinitionSpecs(ctx context.Context, tx *sql.Tx, specs []tool.Spec) ([]tool.Spec, error) {
	found := false
	for _, s := range specs {
		found = found || s.Name == ui.ShowName || s.Name == "builtin__skill_list"
	}
	if !found {
		return specs, nil
	}
	managed, err := managedService(ctx, tx, contract.Normalize)
	if err != nil || !managed {
		return specs, err
	}
	var graph string
	var available, blocked bool
	err = tx.QueryRowContext(ctx, `SELECT coalesce(c.active_graph,''),EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') s WHERE g.revision=c.active_graph AND json_extract(s.value,'$.contract')=?),c.stopped OR EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=c.active_graph) FROM rsi_control c WHERE singleton=1`, contract.Normalize).Scan(&graph, &available, &blocked)
	if err != nil {
		return nil, err
	}
	if !available || graph == "" {
		filtered := specs[:0]
		for _, s := range specs {
			if s.Name != ui.ShowName {
				filtered = append(filtered, s)
			}
		}
		return uiSkillIndex(filtered, nil)
	}
	if blocked {
		return nil, rsi.ErrRevoked
	}
	privacy, err := readCapabilityPrivacy(ctx, tx)
	if err != nil {
		return nil, err
	}
	cache := &r.uiDefinition
	cache.Lock()
	definition, digest, summary, ready := cache.definition, cache.digest, cache.summary, cache.graph == graph
	cache.Unlock()
	if !ready {
		return specs, &uiDefinitionNeed{graph: graph, privacy: privacy}
	}
	for i := range specs {
		if specs[i].Name == ui.ShowName {
			specs[i].InputSchema = definition.InputSchema
			specs[i].ModelInputSchema = definition.ModelInputSchema
			specs[i].Description = definition.Description
			specs[i].ResourceHash = digest
		}
	}
	if summary != nil {
		return uiSkillIndex(specs, summary)
	}
	return specs, nil
}

func (r *Runs) prepareUIDefinition(ctx context.Context, need *uiDefinitionNeed) error {
	if r.plugins == nil {
		return rsi.ErrNotFound
	}
	h := *r.plugins
	store := r.host.Plugins()
	store.Fence = need.privacy.check
	h.Store = store
	normalized, err := h.ServiceInstance(ctx, need.graph, contract.Normalize)
	if err != nil {
		return err
	}
	defined, err := h.ServiceInstance(ctx, need.graph, contract.DefinitionContract)
	if err != nil {
		return err
	}
	if normalized.InstanceID != defined.InstanceID || normalized.PackageDigest != defined.PackageDigest {
		return rsi.ErrInvalid
	}
	raw, err := h.ReadService(ctx, need.graph, contract.DefinitionContract, json.RawMessage(`{}`), nil)
	if err != nil {
		return err
	}
	var definition contract.Definition
	if err = rsiplugin.DecodeResult(raw, &definition); err != nil {
		return err
	}
	// Validate executable schemas using the ordinary Registry parser. All
	// authority attributes remain the Host's existing definition.
	d := ui.ShowDefinition()
	d.Spec.InputSchema, d.Spec.ModelInputSchema, d.Spec.Description = definition.InputSchema, definition.ModelInputSchema, definition.Description
	if _, err = tool.NewRegistry([]tool.Definition{d}); err != nil {
		return err
	}
	var summary *skill.Summary
	pkg, err := h.Catalog.Load(normalized.PackageDigest)
	if err != nil {
		return err
	}
	if !uiLegacySkillPackage(pkg) {
		raw, err := readUISkillFile(h.Catalog, pkg, "SKILL.md")
		if err != nil {
			return err
		}
		value, err := skill.IntelligentUISummary(raw)
		if err != nil {
			return err
		}
		value.Version = pkg.Digest
		summary = &value
	}
	if err = h.Store.CheckGraph(ctx, need.graph); err != nil {
		return err
	}
	cache := &r.uiDefinition
	cache.Lock()
	cache.graph, cache.definition, cache.digest, cache.summary = need.graph, definition, defined.PackageDigest, summary
	cache.Unlock()
	return nil
}

func (r *Runs) uiRegistryForRun(ctx context.Context, claim agent.RunClaim, base *tool.Registry) (*tool.Registry, error) {
	managed, err := r.managedRunService(ctx, claim.RunID(), contract.Normalize)
	if err != nil || !managed {
		return base, err
	}
	var raw string
	if err = r.host.db.QueryRowContext(ctx, `SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, claim.RunID()).Scan(&raw); err != nil {
		return nil, err
	}
	var specs []tool.Spec
	if err = json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, err
	}
	var frozen *tool.Spec
	for i := range specs {
		if specs[i].Name == ui.ShowName {
			frozen = &specs[i]
		}
	}
	defs := []tool.Definition{}
	for _, s := range base.Describe() {
		d, _ := base.Resolve(s.Name)
		if s.Name == ui.ShowName {
			if frozen == nil {
				continue
			}
			// Replace only feature-owned metadata. Permission/replay/Host dispatch are
			// still compared to the original authority by ordinary Registry.Select.
			d.Spec.InputSchema, d.Spec.ModelInputSchema, d.Spec.Description, d.Spec.ResourceHash = frozen.InputSchema, frozen.ModelInputSchema, frozen.Description, frozen.ResourceHash
		}
		defs = append(defs, d)
	}
	return tool.NewRegistry(defs)
}

// Only the owning entry changes. The complete index remains frozen in the
// ordinary capability PromptContext; other Skill identities remain untouched.
func uiSkillIndex(specs []tool.Spec, summary *skill.Summary) ([]tool.Spec, error) {
	for i := range specs {
		if specs[i].Name != "builtin__skill_list" {
			continue
		}
		for j, context := range specs[i].PromptContext {
			if context.Kind != "skill_index" {
				continue
			}
			var entries []skill.Summary
			if err := json.Unmarshal([]byte(context.Text), &entries); err != nil {
				return nil, err
			}
			filtered := make([]skill.Summary, 0, len(entries)+1)
			for _, entry := range entries {
				if entry.Name != "intelligent_ui" {
					filtered = append(filtered, entry)
				}
			}
			if summary != nil {
				filtered = append(filtered, *summary)
			}
			raw, err := json.Marshal(filtered)
			if err != nil {
				return nil, err
			}
			specs[i].PromptContext = append([]tool.PromptContext(nil), specs[i].PromptContext...)
			specs[i].PromptContext[j].Text = string(raw)
			specs[i].PromptContext[j].Source = "skills:" + rsiplugin.FileDigest(raw)
		}
	}
	return specs, nil
}
