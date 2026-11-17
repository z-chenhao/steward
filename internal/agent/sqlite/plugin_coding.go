package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (r *Runs) bindCodeImportPolicy(ctx context.Context, tx *sql.Tx, taskID, scope string) error {
	managed, err := managedService(ctx, tx, contract.ImportContract)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO code_import_policy_binding(task_id,managed,scope_hash) VALUES(?,?,?)`, taskID, managed, scope)
	return err
}

func (r *Runs) prepareCodeImportPolicy(ctx context.Context, c agent.RunClaim, taskID, scope string) (coding.ImportLimits, error) {
	var limits coding.ImportLimits
	var managed bool
	var graph, driver, raw sql.NullString
	var savedScope string
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT managed,scope_hash,graph_revision,driver_hash,limits_json FROM code_import_policy_binding WHERE task_id=?`, taskID).Scan(&managed, &savedScope, &graph, &driver, &raw)
	})
	if err != nil {
		return limits, err
	}
	if savedScope != scope {
		return limits, llm.ModelIncompatible
	}
	if !managed {
		return coding.NativeImportLimits(), nil
	}
	if r.plugins == nil {
		return limits, llm.ProviderUnavailable
	}
	h := r.pluginHost(c)
	if graph.Valid {
		if driver.String != coding.DriverFingerprint() || rsiplugin.DecodeResult([]byte(raw.String), &limits) != nil || !limits.Valid() {
			return limits, llm.ModelIncompatible
		}
		if err = h.Store.CheckGraph(ctx, graph.String); err != nil {
			return limits, codeMethodError(err)
		}
		if _, err = h.ServiceInstance(ctx, graph.String, contract.ImportContract); err != nil {
			return limits, codeMethodError(err)
		}
		return limits, nil
	}
	// An unselected Task intent uses this Root's exact frozen graph. Once a pure
	// choice is accepted, all later Roots and Children keep the Task's choice.
	revision, err := h.Store.Binding(ctx, c.RunID())
	if err != nil {
		return limits, err
	}
	if revision == "" {
		return limits, llm.ProviderUnavailable
	}
	input, _ := json.Marshal(contract.ImportFacts{Ceilings: contract.ImportLimits{Entries: 4096, FileBytes: 8 << 20, TreeBytes: 64 << 20}})
	result, err := h.ReadService(ctx, revision, contract.ImportContract, input, nil)
	if err != nil {
		return limits, codeMethodError(err)
	}
	if rsiplugin.DecodeResult(result, &limits) != nil || !limits.Valid() {
		return limits, llm.ModelIncompatible
	}
	encodedLimits, _ := json.Marshal(limits)
	err = r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		if err := r.pluginDispatchFence(ctx, tx, c.RunID()); err != nil {
			return err
		}
		var currentGraph, currentDriver, currentRaw sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT graph_revision,driver_hash,limits_json FROM code_import_policy_binding WHERE task_id=? AND managed=1 AND scope_hash=?`, taskID, scope).Scan(&currentGraph, &currentDriver, &currentRaw); err != nil {
			return err
		}
		if currentGraph.Valid {
			if currentDriver.String != coding.DriverFingerprint() || rsiplugin.DecodeResult([]byte(currentRaw.String), &limits) != nil || !limits.Valid() {
				return llm.ModelIncompatible
			}
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE code_import_policy_binding SET graph_revision=?,driver_hash=?,limits_json=? WHERE task_id=? AND managed=1 AND graph_revision IS NULL`, revision, coding.DriverFingerprint(), string(encodedLimits), taskID)
		return err
	})
	return limits, err
}
func codeMethodError(err error) error {
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrUnknown) {
		return llm.ProviderUnavailable
	}
	if errors.Is(err, rsi.ErrInvalid) {
		return llm.ModelIncompatible
	}
	return err
}

// A retained Task recipe does not grant dispatch after Guardian stop or
// quarantine, even when the new Root has a different or empty graph.
func codeImportPolicyFence(ctx context.Context, tx *sql.Tx, taskID string) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM code_import_policy_binding b,rsi_control c WHERE b.task_id=? AND b.managed=1 AND b.graph_revision IS NOT NULL AND c.singleton=1 AND (c.stopped=1 OR EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=b.graph_revision)))`, taskID).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return llm.ProviderUnavailable
	}
	return nil
}

type codeReportMethod struct {
	Managed               bool
	Revision, TaskID, Ref string
	Epoch                 int64
	Recipe                contract.ReportRecipe
}

// legacyCodeReportRecipe is only for the never-activated report boundary.
// Managed reads obtain their recipe exclusively from the selected package.
func legacyCodeReportRecipe() contract.ReportRecipe {
	return contract.ReportRecipe{Commands: 256, CommandBytes: 4 << 20, PatchBytes: 8 << 20, Order: "oldest"}
}
func validCodeReportRecipe(p contract.ReportRecipe) bool {
	return p.Commands >= 1 && p.Commands <= 256 && p.CommandBytes >= 1 && p.CommandBytes <= 4<<20 && p.PatchBytes >= 1 && p.PatchBytes <= 8<<20 && (p.Order == "oldest" || p.Order == "recent")
}

const codeReportScopeSQL = `SELECT t.task_id,coalesce(p.epoch,0) FROM task t LEFT JOIN memory_privacy p USING(agent_id) WHERE t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.created_at>coalesce(p.updated_at,0)`

func (s *Store) prepareCodeReportMethod(ctx context.Context, ref, taskID string) (codeReportMethod, error) {
	method := codeReportMethod{Ref: ref, TaskID: taskID}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return method, err
	}
	err = tx.QueryRowContext(ctx, codeReportScopeSQL, ref).Scan(&method.TaskID, &method.Epoch)
	if err == nil && method.TaskID != taskID {
		err = agent.ErrNotFound
	}
	if err == nil {
		method.Managed, err = managedService(ctx, tx, contract.ReportContract)
	}
	if err == nil && method.Managed {
		err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&method.Revision)
	}
	tx.Rollback()
	if errors.Is(err, sql.ErrNoRows) {
		err = agent.ErrNotFound
	}
	if err != nil {
		return method, err
	}
	if !method.Managed {
		method.Recipe = legacyCodeReportRecipe()
		return method, nil
	}
	if method.Revision == "" {
		return method, llm.ProviderUnavailable
	}
	store := s.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error { return codeReportMethodFence(ctx, tx, method) }
	catalog := rsi.NewCatalog(s.DataRoot())
	if configured := s.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	host := rsi.Host{Catalog: catalog, Store: store}
	input, _ := json.Marshal(contract.ReportFacts{Ceilings: contract.ReportCeilings{Commands: 256, CommandBytes: 4 << 20, PatchBytes: 8 << 20}})
	raw, err := host.ReadService(ctx, method.Revision, contract.ReportContract, input, nil)
	if err != nil {
		return method, codeMethodError(err)
	}
	if rsiplugin.DecodeResult(raw, &method.Recipe) != nil || !validCodeReportRecipe(method.Recipe) {
		return method, llm.ModelIncompatible
	}
	return method, nil
}
func codeReportMethodFence(ctx context.Context, tx *sql.Tx, method codeReportMethod) error {
	var taskID string
	var epoch int64
	if err := tx.QueryRowContext(ctx, codeReportScopeSQL, method.Ref).Scan(&taskID, &epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return agent.ErrNotFound
		}
		return err
	}
	if taskID != method.TaskID || epoch != method.Epoch {
		return agent.ErrNotFound
	}
	if !method.Managed {
		return nil
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?) AND NOT(SELECT stopped FROM rsi_control WHERE singleton=1)`, method.Revision, method.Revision).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return llm.ProviderUnavailable
	}
	return nil
}
