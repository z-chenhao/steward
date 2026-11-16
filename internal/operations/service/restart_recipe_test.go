package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRestartRecipeOriginalDeploymentPersistsAndCannotBeReinterpreted(t *testing.T) {
	ctx := context.Background()
	m, p := fixture(t)
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := m.Begin()
	must(t, err)
	recipe := RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("b", 64), Recipe: RestartRecipe{Attempts: 1, RetryDelayMS: 120000}}
	must(t, session.FreezeRestartRecipe(&recipe))
	must(t, session.Heartbeat("ready"))
	changed := recipe
	changed.Recipe.Attempts = 2
	if session.FreezeRestartRecipe(&changed) == nil {
		t.Fatal("same deployment reinterpreted recipe")
	}
	must(t, session.Close())
	reopened, err := New(m.root, m.home, m.executable)
	must(t, err)
	reopened.command, reopened.now = m.command, m.now
	// First dependent operation after the real reopen; no Start, Begin or repair.
	before := len(p.calls)
	status, err := reopened.Inspect(ctx)
	must(t, err)
	if status.Code != "process_exited" || len(p.calls) == before {
		t.Fatal("cold admitted deployment did not use its original recipe", status)
	}
	before = len(p.calls)
	status, err = reopened.Inspect(ctx)
	must(t, err)
	if status.Code != "restart_limit" || len(p.calls) != before {
		t.Fatal("one-attempt recipe restored native three-attempt policy", status)
	}
	state, err := reopened.load()
	must(t, err)
	if state.Recipe == nil || *state.Recipe != recipe || state.Restarts != 1 {
		t.Fatal("original recipe/counter changed", state)
	}
}

func TestRestartRecipeUnavailableSurvivesColdReadWithoutNativeRestart(t *testing.T) {
	ctx := context.Background()
	m, p := fixture(t)
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := m.Begin()
	must(t, err)
	must(t, session.FreezeRestartRecipe(nil))
	must(t, session.Heartbeat("ready"))
	must(t, session.Close())
	reopened, err := New(m.root, m.home, m.executable)
	must(t, err)
	reopened.command, reopened.now = m.command, m.now
	before := len(p.calls)
	status, err := reopened.Inspect(ctx)
	must(t, err)
	if status.Code != "restart_policy_unavailable" || len(p.calls) != before {
		t.Fatal("unavailable producer revived native restart", status)
	}
	status, err = reopened.Status()
	must(t, err)
	if status.Code != "restart_policy_unavailable" {
		t.Fatal("retained latest outcome lost", status)
	}
}

func TestRestartRecipeOldSessionCannotCrossExplicitDeploymentFence(t *testing.T) {
	ctx := context.Background()
	m, _ := fixture(t)
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	old, _, err := m.Begin()
	must(t, err)
	must(t, m.Stop(ctx))
	must(t, m.Start(ctx))
	receipt := RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("c", 64), Recipe: RestartRecipe{Attempts: 2, RetryDelayMS: 60000}}
	if old.FreezeRestartRecipe(&receipt) == nil {
		t.Fatal("old startup wrote new deployment recipe")
	}
	must(t, old.Heartbeat("configuration_error"))
	status, err := m.Status()
	must(t, err)
	if status.Code == "configuration_error" {
		t.Fatal("old deployment poisoned current health")
	}
	state, err := m.load()
	must(t, err)
	if state.Recipe != nil || state.RecipeStatus != recipePending {
		t.Fatal("new pending deployment altered by old session")
	}
	must(t, old.Close())
	// First offline inspection must also ignore the prior deployment's fatal code.
	status, err = m.Inspect(ctx)
	must(t, err)
	if status.Code != "restart_policy_unavailable" {
		t.Fatal("old heartbeat poisoned current offline inspection", status)
	}
}

func TestRestartRecipeDelayIsAppliedAfterDurableAdmission(t *testing.T) {
	ctx := context.Background()
	m, p := fixture(t)
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := m.Begin()
	must(t, err)
	receipt := RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("d", 64), Recipe: RestartRecipe{Attempts: 2, RetryDelayMS: 120000}}
	must(t, session.FreezeRestartRecipe(&receipt))
	must(t, session.Heartbeat("ready"))
	must(t, session.Close())
	_, err = m.Inspect(ctx)
	must(t, err)
	start := m.now()
	m.now = func() time.Time { return start.Add(time.Minute) }
	before := len(p.calls)
	_, err = m.Inspect(ctx)
	must(t, err)
	if len(p.calls) != before {
		t.Fatal("owned two-minute delay replaced by native minute")
	}
	m.now = func() time.Time { return start.Add(2 * time.Minute) }
	_, err = m.Inspect(ctx)
	must(t, err)
	if len(p.calls) == before {
		t.Fatal("second original deployment retry not admitted")
	}
}

func TestRestartRecipeLegacyDeploymentRecoveryDoesNotMigrate(t *testing.T) {
	ctx := context.Background()
	m, peer := fixture(t)
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	// An installed original control-v1 deployment has no graph or recipe fields.
	must(t, m.save(settings{Version: 1, Installed: true, Enabled: true, Notice: "ready"}))
	session, _, err := m.Begin()
	must(t, err)
	if session.NeedsRestartRecipe() {
		t.Fatal("recovery implicitly requested a new deployment policy")
	}
	state, err := m.load()
	must(t, err)
	if state.Version != 1 || state.Recipe != nil {
		t.Fatal("recovery rewrote original deployment", state)
	}
	must(t, session.Heartbeat("ready"))
	must(t, session.Close())
	reopened, err := New(m.root, m.home, m.executable)
	must(t, err)
	reopened.command, reopened.now = m.command, m.now
	before := len(peer.calls)
	// First dependent action after real reopen: no select, admission or repair.
	status, err := reopened.Inspect(ctx)
	must(t, err)
	if status.Code != "process_exited" || len(peer.calls) == before {
		t.Fatal("original deployment lost its restart policy", status)
	}
	must(t, reopened.Stop(ctx))
	must(t, reopened.Start(ctx))
	next, _, err := reopened.Begin()
	must(t, err)
	if !next.NeedsRestartRecipe() {
		t.Fatal("explicit new deployment retained historical policy")
	}
	must(t, next.FreezeRestartRecipe(nil))
	must(t, next.Close())
	before = len(peer.calls)
	status, err = reopened.Inspect(ctx)
	must(t, err)
	if status.Code != "restart_policy_unavailable" || len(peer.calls) != before {
		t.Fatal("new deployment revived historical fallback", status)
	}
}
