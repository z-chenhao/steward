package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/skilllibrary/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ConfigureSkillLibrary moves accepted discovery/reads behind Host admission.
// The existing immutable-file reader remains a private driver; specs, resources
// and historical request hashes do not change when the policy is installed.
func (r *Runs) ConfigureSkillLibrary() error {
	if r.skillRead != nil {
		return nil
	}
	defs := []tool.Definition{}
	var reader func(context.Context, string) tool.Result
	for _, name := range []string{"builtin__skill_list", "builtin__skill_view"} {
		d, ok := r.registry.Resolve(name)
		if !ok {
			if len(defs) == 0 {
				return nil
			}
			return rsi.ErrInvalid
		}
		if d.Host || d.Execute == nil {
			return rsi.ErrInvalid
		}
		if name == "builtin__skill_view" {
			reader = d.Execute
		}
		d.Host = true
		d.Execute = nil
		defs = append(defs, d)
	}
	if err := r.registry.ReplaceGroup("builtin__skill_", defs); err != nil {
		return err
	}
	r.skillRead = reader
	return nil
}

func (t *tools) executeSkill(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var name, args, revision string
	var spec tool.Spec
	managed := false
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		_, n, a, e := t.acceptedBuiltin(ctx, tx, inv)
		if e != nil {
			return e
		}
		name, args = n, a
		if name != "builtin__skill_list" && name != "builtin__skill_view" {
			return tool.ErrApprovalDenied
		}
		var raw string
		if e = tx.QueryRowContext(ctx, `SELECT c.tools_json FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE r.run_id=?`, t.claim.RunID()).Scan(&raw); e != nil {
			return e
		}
		var specs []tool.Spec
		if e = json.Unmarshal([]byte(raw), &specs); e != nil {
			return e
		}
		for _, s := range specs {
			if s.Name == name {
				spec = s
				break
			}
		}
		return tx.QueryRowContext(ctx, `SELECT coalesce((SELECT graph_revision FROM rsi_run WHERE run_id=?),''),EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?)`, t.claim.RunID(), t.claim.RunID(), contract.SearchContract).Scan(&revision, &managed)
	})
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	policy := func(kind string, input any, out any) error {
		if t.runs.plugins == nil || revision == "" {
			return rsi.ErrNotFound
		}
		h := *t.runs.plugins
		store := t.runs.host.Plugins()
		store.Fence = t.fence
		h.Store = store
		raw, e := json.Marshal(input)
		if e != nil {
			return e
		}
		result, e := h.ReadService(ctx, revision, kind, raw, nil)
		if e != nil {
			return e
		}
		return rsiplugin.DecodeResult(result, out)
	}
	result := tool.Result{}
	if name == "builtin__skill_list" {
		q, ok := skill.ParseQuery(args)
		if !ok {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		entries := []skill.Summary{}
		found := false
		for _, resource := range spec.PromptContext {
			if resource.Kind == "skill_index" {
				found = json.Unmarshal([]byte(resource.Text), &entries) == nil
				break
			}
		}
		if !found || len(entries) > 129 {
			return tool.Result{}, agent.ErrStorage
		}
		if !managed {
			result = skill.ListFrozen(entries, args)
		} else {
			facts := contract.SearchFacts{Query: q.Query, Limit: q.Limit, Entries: make([]contract.Entry, len(entries))}
			for i, e := range entries {
				facts.Entries[i] = contract.Entry{Name: e.Name, Description: e.Description}
			}
			var plan contract.SearchPlan
			if err = policy(contract.SearchContract, facts, &plan); err != nil {
				return skillPolicyFailure(err)
			}
			result, err = skillSelection(entries, q.Limit, plan)
			if err != nil {
				return tool.Result{}, err
			}
		}
	} else {
		skillName, path, ok := skill.ResourceArguments(args)
		if !ok {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		if managed {
			var plan contract.ResourcePlan
			if err = policy(contract.ResourceContract, contract.ResourceFacts{Path: path}, &plan); err != nil {
				return skillPolicyFailure(err)
			}
			if !validSkillResourcePath(path, plan.Path) {
				return tool.Result{}, rsi.ErrInvalid
			}
			argsRaw, _ := json.Marshal(struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}{skillName, plan.Path})
			args = string(argsRaw)
		}
		selectedName, selectedPath, _ := skill.ResourceArguments(args)
		owned, handled, readErr := t.uiSkillResource(ctx, inv, selectedName, selectedPath)
		if readErr != nil {
			return skillPolicyFailure(readErr)
		}
		if handled {
			return owned, nil
		}
		// Runtime selection must still agree with the frozen file/resource hash.
		live, ok := t.runs.registry.Resolve(name)
		if !ok || live.Spec.ResourceHash != spec.ResourceHash || t.runs.skillRead == nil {
			return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
		}
		result = t.runs.skillRead(ctx, args)
	}
	if managed {
		h := *t.runs.plugins
		store := t.runs.host.Plugins()
		store.Fence = t.fence
		h.Store = store
		if err = h.Store.CheckGraph(ctx, revision); err != nil {
			return skillPolicyFailure(err)
		}
	}
	if err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.fence(ctx, tx) }); err != nil {
		return tool.Result{}, err
	}
	return result, nil
}

func skillSelection(entries []skill.Summary, limit int, p contract.SearchPlan) (tool.Result, error) {
	if len(p.Indices) > limit {
		return tool.Result{}, rsi.ErrInvalid
	}
	seen := map[int]bool{}
	out := make([]contract.Entry, 0, len(p.Indices))
	for _, i := range p.Indices {
		if i < 0 || i >= len(entries) || seen[i] {
			return tool.Result{}, rsi.ErrInvalid
		}
		seen[i] = true
		out = append(out, contract.Entry{Name: entries[i].Name, Description: entries[i].Description})
	}
	raw, err := json.Marshal(out)
	return tool.Result{Text: string(raw)}, err
}
func skillPolicyFailure(err error) (tool.Result, error) {
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	return tool.Result{}, err
}

func validSkillResourcePath(requested, selected string) bool {
	return fs.ValidPath(selected) && utf8.ValidString(selected) && utf8.RuneCountInString(selected) <= 512 && (requested == "" || requested == selected)
}
