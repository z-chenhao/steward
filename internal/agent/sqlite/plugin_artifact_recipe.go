package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func artifactRecipeTx(ctx context.Context, tx *sql.Tx, runID string) (contract.RecipePlan, bool, error) {
	var plan contract.RecipePlan
	var kinds string
	err := tx.QueryRowContext(ctx, `SELECT retention_days,media_kinds_json,overflow_name,overflow_media,stored_text,publish_text FROM run_artifact_recipe WHERE run_id=?`, runID).Scan(&plan.RetentionDays, &kinds, &plan.OverflowName, &plan.OverflowMedia, &plan.StoredText, &plan.PublishText)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, false, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(kinds), &plan.MediaKinds)
	}
	return plan, err == nil, err
}

// Recipe receipt text and generated metadata are policy. Explicit caller
// metadata/ResultJSON and hard quotas remain outside the candidate's authority.
func validArtifactRecipe(plan contract.RecipePlan, explicit int) bool {
	if plan.RetentionDays < 1 || plan.RetentionDays > 3650 || explicit > 0 && plan.RetentionDays != explicit {
		return false
	}
	if (artifact.Input{Name: plan.OverflowName, MediaType: plan.OverflowMedia}).Validate() != nil {
		return false
	}
	if !utf8.ValidString(plan.OverflowName) || !utf8.ValidString(plan.OverflowMedia) {
		return false
	}
	for _, text := range []string{plan.StoredText, plan.PublishText} {
		if text == "" || len(text) > 1024 || !utf8.ValidString(text) {
			return false
		}
	}
	seen := map[string]bool{}
	for _, kind := range plan.MediaKinds {
		if seen[kind] || kind != "image" && kind != "audio" && kind != "video" {
			return false
		}
		seen[kind] = true
	}
	return true
}

// The first complete acceptance wins. Pure computation may restart before the
// commit; already accepted recipes never depend on a current configuration or
// current candidate. There is no external effect or opaque Call journal here.
func (r *Runs) prepareArtifactRecipe(ctx context.Context, claim agent.RunClaim) error {
	managed, err := r.managedRunService(ctx, claim.RunID(), contract.Recipe)
	if err != nil || !managed {
		return err
	}
	if r.plugins == nil {
		return rsi.ErrNotFound
	}
	host := r.pluginHost(claim)
	revision, err := host.Store.Binding(ctx, claim.RunID())
	if err != nil {
		return err
	}
	if err := host.Store.CheckGraph(ctx, revision); err != nil {
		return err
	}
	found := false
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, claim); err != nil {
			return err
		}
		_, exists, err := artifactRecipeTx(ctx, tx, claim.RunID())
		found = exists
		return err
	})
	if err != nil || found {
		return err
	}
	explicit := 0
	if err := r.host.db.QueryRowContext(ctx, `SELECT retention_days FROM run_artifact_request WHERE run_id=?`, claim.RunID()).Scan(&explicit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rsi.ErrNotFound
		}
		return err
	}
	input, err := json.Marshal(contract.RecipeRequest{RetentionDays: explicit})
	if err != nil {
		return err
	}
	output, err := host.ReadService(ctx, revision, contract.Recipe, input, nil)
	if err != nil {
		return err
	}
	var plan contract.RecipePlan
	if err := rsiplugin.DecodeResult(output, &plan); err != nil {
		return err
	}
	if !validArtifactRecipe(plan, explicit) {
		return rsi.ErrInvalid
	}
	kinds, err := json.Marshal(plan.MediaKinds)
	if err != nil {
		return err
	}
	r.host.boundary("before_artifact_recipe_commit")
	return r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, claim); err != nil {
			return err
		}
		if err := r.pluginDispatchFence(ctx, tx, claim.RunID()); err != nil {
			return err
		}
		var actual string
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(graph_revision,'') FROM rsi_run WHERE run_id=?`, claim.RunID()).Scan(&actual); err != nil {
			return err
		}
		if actual != revision {
			return agent.ErrConflict
		}
		_, found, err := artifactRecipeTx(ctx, tx, claim.RunID())
		if err != nil || found {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO run_artifact_recipe VALUES(?,?,?,?,?,?,?,?,?)`, claim.RunID(), revision, explicit, plan.RetentionDays, string(kinds), plan.OverflowName, plan.OverflowMedia, plan.StoredText, plan.PublishText)
		return err
	})
}

func artifactRecipeBeforeDispatch(inv tool.Invocation, policy tool.Policy, definition tool.Definition) bool {
	if definition.Spec.Permission == "external_effect" || policy.Risk == "high" || policy.Replay != "safe" {
		return true
	}
	if inv.Call.Name != artifacttools.Manage {
		return false
	}
	var request struct {
		Operation string `json:"operation"`
	}
	return json.Unmarshal([]byte(inv.Call.Arguments), &request) == nil && (request.Operation == "save" || request.Operation == "publish")
}

// Hidden or schema-invalid calls do not get a preparation capability. Already running
// or terminal calls keep their original effect/recovery identity, never a newly
// computed recipe. The ordinary Prepare transaction still owns schema, role,
// approval and result acceptance.
func (t *tools) prepareArtifactDispatchRecipe(ctx context.Context, id string) (tool.Result, error) {
	needed := false
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		inv, policy, err := t.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if inv.Status != "queued" && inv.Status != "waiting" {
			return nil
		}
		var visible bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) s WHERE r.run_id=? AND json_extract(s.value,'$.name')=?)`, t.claim.RunID(), inv.Call.Name).Scan(&visible); err != nil {
			return err
		}
		definition, known := t.catalog().Resolve(inv.Call.Name)
		if !visible || !known || !tool.Validate(definition.Spec.InputSchema, inv.Call.Arguments) {
			return nil
		}
		needed = artifactRecipeBeforeDispatch(inv, policy, definition)
		return nil
	})
	if err != nil || !needed {
		return tool.Result{}, err
	}
	if err := t.runs.prepareArtifactRecipe(ctx, t.claim); err != nil {
		return artifactPolicyFailure(err)
	}
	return tool.Result{}, nil
}

// Check the durable preparation independently of the preparer's return value.
// A direct/buggy internal Start cannot bypass this before-effect invariant.
func (t *tools) artifactDispatchRecipeTx(ctx context.Context, tx *sql.Tx, inv tool.Invocation, policy tool.Policy) error {
	definition, known := t.catalog().Resolve(inv.Call.Name)
	if !known || !artifactRecipeBeforeDispatch(inv, policy, definition) {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?) OR EXISTS(SELECT 1 FROM run_artifact_recipe p JOIN rsi_run r USING(run_id) WHERE p.run_id=? AND p.graph_revision=r.graph_revision)`, t.claim.RunID(), contract.Recipe, t.claim.RunID()).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return agent.ErrConflict
	}
	return nil
}

// A pure operation may need storage only after its result is known. Mutating
// providers must already have a pre-effect recipe; finish/reconciliation never
// calls a candidate to reconstruct a missing preparation for such an effect.
func (t *tools) prepareArtifactResultRecipe(ctx context.Context, id string, result tool.Result) error {
	if result.Error != nil {
		return nil
	}
	var inv tool.Invocation
	var policy tool.Policy
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { var err error; inv, policy, err = t.load(ctx, tx, id); return err })
	if err != nil {
		return err
	}
	text, limit, valid := artifactOutputText(inv.Call.Name, policy, result)
	if result.Output == nil && len(result.Attachments) == 0 && result.PublishRefs == nil && (!valid || len(text) <= limit) {
		return nil
	}
	if definition, known := t.catalog().Resolve(inv.Call.Name); known && artifactRecipeBeforeDispatch(inv, policy, definition) {
		return nil
	}
	return t.runs.prepareArtifactRecipe(ctx, t.claim)
}

// A frozen recipe can be used without executing its package while accepting an
// already performed effect. Dispatch authorization is enforced before effects;
// reconciliation remains independently fenced by the owning effect receipt.
func (r *Runs) outputArtifactRecipe(ctx context.Context, id string) (contract.RecipePlan, error) {
	var plan contract.RecipePlan
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var runID string
		if err := tx.QueryRowContext(ctx, `SELECT run_id FROM tool_invocation WHERE invocation_id=?`, id).Scan(&runID); err != nil {
			return err
		}
		var err error
		plan, err = r.selectedArtifactRecipeTx(ctx, tx, runID)
		return err
	})
	return plan, err
}

func (r *Runs) selectedArtifactRecipeTx(ctx context.Context, tx *sql.Tx, runID string) (contract.RecipePlan, error) {
	var managed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?)`, runID, contract.Recipe).Scan(&managed); err != nil {
		return contract.RecipePlan{}, err
	}
	if !managed {
		return legacyArtifactRecipe(r.host.Artifacts().Policy.RetentionDays), nil
	}
	plan, found, err := artifactRecipeTx(ctx, tx, runID)
	if err == nil && !found {
		err = artifact.ErrUnavailable
	}
	return plan, err
}
