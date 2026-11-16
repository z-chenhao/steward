package app

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"testing"
)

func TestModelConnectionPreparationFailureRejectsBeforeNativeFallback(t *testing.T) {
	want := errors.New("owner preparation unavailable")
	calls := 0
	a := &App{prepareFeaturePlugins: func(context.Context) error { calls++; return want }}
	_, managed, err := a.readModelConnection(context.Background(), contract.ConnectionFacts{AccountBacked: true, Login: &contract.LoginFacts{Models: []string{"original"}}})
	if !errors.Is(err, want) || !managed || calls != 1 {
		t.Fatal("setup failure lost owner boundary", managed, err, calls)
	}
}

func TestModelConnectionPresentationPreparationFailureRejectsBeforeNativeFallback(t *testing.T) {
	want := errors.New("owner preparation unavailable")
	calls := 0
	a := &App{prepareFeaturePlugins: func(context.Context) error { calls++; return want }}
	if _, managed, err := a.readModelMenu(context.Background(), contract.Facts{}); !managed || !errors.Is(err, want) {
		t.Fatal(managed, err)
	}
	if _, managed, err := a.readModelSettings(context.Background(), contract.SettingsFacts{WithView: true}); !managed || !errors.Is(err, want) {
		t.Fatal(managed, err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestModelConnectionShortcutPreservesPreparationAndIdentityFences(t *testing.T) {
	want := errors.New("owner preparation unavailable")
	calls := 0
	a := &App{prepareFeaturePlugins: func(context.Context) error { calls++; return want }}
	invalid := llm.ProfileInput{Name: "CLI", ModelID: "model", ProviderKind: "openai_compatible", Endpoint: "file:///private"}
	if _, e := a.PrepareModelShortcut(context.Background(), invalid); e == nil || calls != 0 {
		t.Fatal("invalid identity prepared runtime", e, calls)
	}
	invalid.Endpoint = "https://cli.example.test/v1"
	out, e := a.PrepareModelShortcut(context.Background(), invalid)
	if !errors.Is(e, want) || calls != 1 || out.ContextWindow != 0 {
		t.Fatal("preparation failure restored native defaults", out, e, calls)
	}
}
