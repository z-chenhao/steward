package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type scheduleGuidanceCache struct {
	sync.Mutex
	graph, hash string
	value       contract.GuidanceResult
}
type scheduleGuidanceNeed struct {
	graph   string
	privacy capabilityPrivacy
}

func (*scheduleGuidanceNeed) Error() string { return "Schedule guidance projection required" }

func (r *Runs) scheduleGuidanceSpecs(ctx context.Context, tx *sql.Tx, specs []tool.Spec) ([]tool.Spec, error) {
	var main, observation bool
	for _, spec := range specs {
		main = main || spec.Name == automation.ManageName
		observation = observation || spec.Name == automation.ObserveName
	}
	var err error
	if main {
		main, err = managedService(ctx, tx, contract.Guidance)
		if err != nil {
			return nil, err
		}
	}
	if observation {
		observation, err = managedService(ctx, tx, contract.Threshold)
		if err != nil {
			return nil, err
		}
	}
	if !main && !observation {
		return specs, nil
	}
	for i := range specs {
		if main && specs[i].Name == automation.ManageName || observation && specs[i].Name == automation.ObserveName {
			specs[i].Description = ""
			specs[i].ResourceHash = ""
		}
	}
	value, hash, err := r.readScheduleGuidance(ctx, tx)
	if err != nil || value == nil {
		return specs, err
	}
	for i := range specs {
		if main && specs[i].Name == automation.ManageName {
			specs[i].Description = value.Description
			specs[i].ResourceHash = hash
		}
		if observation && specs[i].Name == automation.ObserveName && value.Observation != nil {
			specs[i].Description = value.Observation.ToolDescription
			specs[i].ResourceHash = hash
		}
	}
	return specs, nil
}

// Public owning metadata, reused by actual capability and Root admission consumers.
// A cache miss rolls back the writer and uses the existing preparation mechanism.
func (r *Runs) readScheduleGuidance(ctx context.Context, tx *sql.Tx) (*contract.GuidanceResult, string, error) {
	var graph string
	var available, blocked bool
	err := tx.QueryRowContext(ctx, `SELECT coalesce(c.active_graph,''),EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') s WHERE g.revision=c.active_graph AND json_extract(s.value,'$.contract')=?),c.stopped OR EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=c.active_graph) FROM rsi_control c WHERE c.singleton=1`, contract.Guidance).Scan(&graph, &available, &blocked)
	if err != nil {
		return nil, "", err
	}
	if !available || graph == "" {
		return nil, "", nil
	}
	if blocked {
		return nil, "", rsi.ErrRevoked
	}
	privacy, err := readCapabilityPrivacy(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	cache := &r.scheduleGuidance
	cache.Lock()
	ready := cache.graph == graph
	value, hash := cache.value, cache.hash
	cache.Unlock()
	if !ready {
		return nil, "", &scheduleGuidanceNeed{graph: graph, privacy: privacy}
	}
	return &value, hash, nil
}

func (r *Runs) automationObservationInstruction(ctx context.Context, tx *sql.Tx, below int64) (string, error) {
	managed, err := managedService(ctx, tx, contract.Threshold)
	if err != nil {
		return "", err
	}
	if !managed {
		return legacyAutomationObservationInstruction(below), nil
	}
	value, _, err := r.readScheduleGuidance(ctx, tx)
	if err != nil {
		return "", err
	}
	if value == nil || value.Observation == nil {
		return "", rsi.ErrNotFound
	}
	// Only the configured integer is inserted by Host; this is not a template DSL.
	return value.Observation.InstructionPrefix + strconv.FormatInt(below, 10) + value.Observation.InstructionSuffix, nil
}

func (r *Runs) prepareScheduleGuidance(ctx context.Context, need *scheduleGuidanceNeed) error {
	if r.plugins == nil {
		return rsi.ErrNotFound
	}
	host := *r.plugins
	control := r.host.Plugins()
	control.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := need.privacy.check(ctx, tx); err != nil {
			return err
		}
		var graph string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&graph); err != nil {
			return err
		}
		if graph != need.graph {
			return agent.ErrConflict
		}
		return checkScheduleGraphTx(ctx, tx, graph)
	}
	host.Store = control
	raw, err := host.ReadService(ctx, need.graph, contract.Guidance, json.RawMessage(`{}`), nil)
	if err != nil {
		return err
	}
	var value contract.GuidanceResult
	if err = rsiplugin.DecodeResult(raw, &value); err != nil {
		return err
	}
	if strings.TrimSpace(value.Description) == "" || !utf8.ValidString(value.Description) || utf8.RuneCountInString(value.Description) > 4096 {
		return rsi.ErrInvalid
	}
	if value.Observation != nil {
		for _, text := range []string{value.Observation.ToolDescription, value.Observation.InstructionPrefix, value.Observation.InstructionSuffix} {
			if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 4096 {
				return rsi.ErrInvalid
			}
		}
	}
	cache := &r.scheduleGuidance
	cache.Lock()
	cache.graph, cache.value, cache.hash = need.graph, value, digest(string(raw))
	cache.Unlock()
	return nil
}

// Rebind only frozen feature wording/identity. Ordinary Registry selection still
// checks Host dispatch, permission, replay and structural schema authority.
func (r *Runs) scheduleRegistryForRun(ctx context.Context, claim agent.RunClaim, base *tool.Registry) (*tool.Registry, error) {
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
		if spec.Name == automation.ManageName || spec.Name == automation.ObserveName {
			frozen[spec.Name] = spec
		}
	}
	defs := []tool.Definition{}
	for _, s := range base.Describe() {
		d, _ := base.Resolve(s.Name)
		if s.Name == automation.ManageName || s.Name == automation.ObserveName {
			spec, ok := frozen[s.Name]
			if !ok {
				continue
			}
			d.Spec.Description, d.Spec.ResourceHash = spec.Description, spec.ResourceHash
		}
		defs = append(defs, d)
	}
	return tool.NewRegistry(defs)
}
