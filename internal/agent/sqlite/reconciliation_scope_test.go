package sqlite

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestToolUnknownReconciliationUsesOnlyOriginalQueryableResource(t *testing.T) {
	for _, mode := range []string{"no-query", "query-succeeded", "query-unknown", "query-target-drift", "removed-target"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var invoked, queried atomic.Int32
			policy := "query_before_retry"
			if mode == "no-query" {
				policy = "never"
			}
			d := effectDef(t, policy)
			d.Invoke = func(context.Context, tool.Request) tool.Outcome {
				invoked.Add(1)
				return tool.Outcome{Ambiguous: true}
			}
			var originalArgs, originalKey string
			if mode != "no-query" {
				d.Query = func(_ context.Context, q tool.Request) tool.Evidence {
					queried.Add(1)
					if q.Arguments != originalArgs || q.IdempotencyKey != originalKey || q.IdempotencyKey == "" {
						t.Error("query changed original accepted action", q)
						return tool.Evidence{State: "unknown"}
					}
					if mode == "query-unknown" {
						return tool.Evidence{State: "unknown"}
					}
					return tool.Evidence{State: "succeeded", Result: tool.Result{Text: "authoritative original receipt"}}
				}
			} else {
				d.Query = nil
			}
			unrelated, _ := tool.FixtureRegistry().Resolve("mcp__fixture__echo")
			unrelated.Spec.ResourceHash = "unrelated-original-resource"
			calls := effectCall(`{"target":"exact-original-target","text":"original requested action"}`)
			s, r, owner, executor, exchange := admitPolicy(t, []tool.Definition{d, unrelated}, calls)
			if _, err := executor.Execute(ctx, exchange, calls); !errors.Is(err, tool.ErrSuspended) {
				t.Fatal(err)
			}
			approvePolicy(t, s, r)
			result, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(result) != 1 || result[0].Error == nil || result[0].Error.Code != "UNKNOWN_SIDE_EFFECT" {
				t.Fatal("fixture did not reach actual uncertainty", result, err)
			}
			if err := s.db.QueryRow(`SELECT arguments_json,idempotency_key FROM tool_invocation WHERE tool_name=?`, d.Spec.Name).Scan(&originalArgs, &originalKey); err != nil {
				t.Fatal(err)
			}
			run, err := r.Load(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Fail(ctx, owner, run.Version, "UNKNOWN_SIDE_EFFECT"); err != nil {
				t.Fatal(err)
			}
			const endedRun = `SELECT json_object('status',status,'error',error_code,'ended',ended_at,'version',version) FROM run WHERE run_id=? AND status='failed'`
			var before, after string
			if err := s.db.QueryRow(endedRun, owner.RunID()).Scan(&before); err != nil {
				t.Fatal("fixture did not end the original Run", err)
			}
			unrelated.Spec.ResourceHash = "unrelated-new-resource"
			if err := r.registry.ReplaceGroup(unrelated.Spec.Name, []tool.Definition{unrelated}); err != nil {
				t.Fatal(err)
			}
			if mode == "query-target-drift" {
				d.Spec.ResourceHash += ":changed"
				if err := r.registry.ReplaceGroup(d.Spec.Name, []tool.Definition{d}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "removed-target" {
				if err := r.registry.ReplaceGroup(d.Spec.Name, nil); err != nil {
					t.Fatal(err)
				}
			}
			err = r.ReconcileUnknown(ctx)
			if mode == "query-target-drift" {
				if !errors.Is(err, llm.ModelIncompatible) {
					t.Fatal("changed query resource bypassed its original frozen identity", err)
				}
			} else if err != nil {
				t.Fatal("unrelated feature drift blocked query-only recovery", err)
			}
			wantQueries := int32(0)
			if mode == "query-succeeded" || mode == "query-unknown" {
				wantQueries = 1
			}
			if invoked.Load() != 1 || queried.Load() != wantQueries {
				t.Fatal("recovery dispatched or queried outside its original scope", invoked.Load(), queried.Load())
			}
			wantStatus := "unknown"
			if mode == "query-succeeded" {
				wantStatus = "succeeded"
			}
			if err := s.db.QueryRow(endedRun, owner.RunID()).Scan(&after); err != nil {
				t.Fatal("query recovery revived the ended Run", err)
			}
			if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status=? AND attempt_count=1`, d.Spec.Name, wantStatus) != 1 || after != before {
				t.Fatal("query recovery rewrote certainty or revived the ended Run")
			}
		})
	}
}

func TestToolUnknownUnfrozenOperationCannotAcquireNewQueryAuthority(t *testing.T) {
	for _, mode := range []string{"unavailable", "new-query", "new-no-query"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, r, a := controlFixture(t, nil)
			controlMain(t, r, a, "create", "create")
			owner := rootClaim(t, s, r)
			run, err := r.Load(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			generation, err := r.Generation(ctx, owner, run)
			if err != nil {
				t.Fatal(err)
			}
			const name = "unavailable_external"
			response := calls(name, `{}`)
			if err := s.Models().RecordUsageAnd(ctx, generation.Request, response, 0, nil, s.AcceptGeneration); err != nil {
				t.Fatal(err)
			}
			store := r.ToolStore(owner)
			invocations, err := store.Accept(ctx, generation.Request.GenerationID, response.ToolCalls, run.Tools)
			if err != nil || len(invocations) != 1 {
				t.Fatal(invocations, err)
			}
			if err := store.Start(ctx, invocations[0].ID); err != nil {
				t.Fatal(err)
			}
			if err := store.Unknown(ctx, invocations[0].ID); err != nil {
				t.Fatal(err)
			}
			run, err = r.Load(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Fail(ctx, owner, run.Version, "UNKNOWN_SIDE_EFFECT"); err != nil {
				t.Fatal(err)
			}
			var queries, effects atomic.Int32
			if mode != "unavailable" {
				replay := "query_before_retry"
				if mode == "new-no-query" {
					replay = "never"
				}
				d := effectDef(t, replay)
				d.Spec.Name = name
				d.Invoke = func(context.Context, tool.Request) tool.Outcome {
					effects.Add(1)
					return tool.Outcome{Ambiguous: true}
				}
				d.Query = nil
				if mode == "new-query" {
					d.Query = func(context.Context, tool.Request) tool.Evidence {
						queries.Add(1)
						return tool.Evidence{State: "succeeded", Result: tool.Result{Text: "unearned later receipt"}}
					}
				}
				if err := r.registry.ReplaceGroup(name, []tool.Definition{d}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ReconcileUnknown(ctx); err != nil {
				t.Fatal("unavailable original operation blocked recovery", err)
			}
			if queries.Load() != 0 || effects.Load() != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE invocation_id=? AND status='unknown' AND attempt_count=1 AND reconciliation_json IS NULL`, invocations[0].ID) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE run_id=? AND status='failed'`, owner.RunID()) != 1 {
				t.Fatal("a later definition acquired authority over an unfrozen operation", queries.Load(), effects.Load())
			}
		})
	}
}
