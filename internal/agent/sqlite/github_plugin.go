package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only authoritative admission/control. Accepted recipes survive version
// switching/deactivation, while Guardian stop/quarantine and Forget still win.
// The source graph can differ from the new Main's other current feature graph.
const validGitHubSourceMethod = `o.source_method_json IS NULL OR (
 EXISTS(SELECT 1 FROM rsi_activation a WHERE json_extract(a.receipt_json,'$.graph_revision')=json_extract(o.source_method_json,'$.graph_revision'))
 AND NOT(SELECT stopped FROM rsi_control WHERE singleton=1)
 AND NOT EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=json_extract(o.source_method_json,'$.graph_revision')))`

func githubPollFence(ctx context.Context, tx *sql.Tx, cfg github.Config, ticket github.Ticket) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled=1 AND version=? AND intent_privacy_epoch=? AND intent_privacy_epoch=`+githubEpoch+` FROM github_watch WHERE scope=?`, ticket.Version, ticket.Epoch, cfg.Scope()).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return github.ErrStopped
	}
	return nil
}
func githubMethodFence(ctx context.Context, tx *sql.Tx, revision string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?) AND NOT(SELECT stopped FROM rsi_control WHERE singleton=1)`, revision, revision).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return rsi.ErrRevoked
	}
	return nil
}

// ReadGitHubStep has no Run, effects or writes. Each pure process and input read
// rechecks the original poll/privacy ticket, plus Guardian's graph controls.
func (s *Store) ReadGitHubStep(ctx context.Context, cfg github.Config, ticket github.Ticket, request contract.Request, facts []byte) (contract.Plan, error) {
	if !ticket.Managed || ticket.GraphRevision == "" {
		return contract.Plan{}, &github.Failure{Code: "METHOD_UNAVAILABLE"}
	}
	return s.readGitHubPlan(ctx, ticket.GraphRevision, request, facts, func(ctx context.Context, tx *sql.Tx) error { return githubPollFence(ctx, tx, cfg, ticket) })
}

func (s *Store) githubBindingGoalPreview(ctx context.Context, cfg github.Config, version int64, goal string) (string, error) {
	revision, managed, err := s.ownerReadServiceSelection(ctx, contract.Contract)
	if err != nil {
		return "", err
	}
	if !managed {
		return githubContextPreviewLegacyV1(goal), nil
	}
	plan, err := s.readGitHubPlan(ctx, revision, contract.Request{History: []contract.Page{}, PollSeconds: cfg.PollSeconds}, []byte{}, func(ctx context.Context, tx *sql.Tx) error {
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT version=? AND privacy_epoch=`+githubEpoch+` FROM github_task_binding WHERE scope=?`, version, cfg.Scope()).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return github.ErrStopped
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !github.ValidRecipe(plan.Followup) {
		return "", &github.Failure{Code: "INVALID_METHOD"}
	}
	return github.Preview(goal, plan.Followup.GoalBytes, plan.Followup.Notice), nil
}
func (s *Store) readGitHubPlan(ctx context.Context, revision string, request contract.Request, facts []byte, fence func(context.Context, *sql.Tx) error) (contract.Plan, error) {
	store := s.Plugins()
	store.Fence = fence
	catalog := rsi.NewCatalog(s.DataRoot())
	if configured := s.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	host := rsi.Host{Catalog: catalog, Store: store}
	input, err := json.Marshal(request)
	if err != nil {
		return contract.Plan{}, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Contract, input, []rsi.InputData{{ID: contract.InputID, Data: facts}})
	if err != nil {
		if ctx.Err() != nil {
			return contract.Plan{}, ctx.Err()
		}
		return contract.Plan{}, &github.Failure{Code: "METHOD_UNAVAILABLE"}
	}
	var plan contract.Plan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return plan, &github.Failure{Code: "INVALID_METHOD"}
	}
	return plan, nil
}
