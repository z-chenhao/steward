package setup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/v1"
)

type countedPresentationSecrets struct {
	llm.SecretStore
	gets   int
	before func()
}

func (s *countedPresentationSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	s.gets++
	if s.before != nil {
		s.before()
	}
	return s.SecretStore.Get(ctx, ref)
}
func TestCapabilityPresentationFailureCannotProbeOrInventFacts(t *testing.T) {
	m := fixtureManager(t)
	ctx := context.Background()
	counter := &countedPresentationSecrets{SecretStore: m.secrets}
	m.secrets = counter
	initial := detail(t, m, "memory")
	m.Presentation = func(context.Context, contract.PresentationFacts) (contract.Presentation, error) {
		return contract.Presentation{}, errors.New("private presentation failure")
	}
	actual, err := m.Detail(ctx, "memory")
	if err != nil || !actual.PresentationUnavailable || actual.Configured != initial.Configured || actual.Revision != initial.Revision || actual.Verified || strings.Contains(actual.Message, "private") {
		t.Fatal("projection changed facts or leaked diagnostics", actual, err)
	}
	if _, err = m.Test(ctx, "memory", initial.Revision, "encryption_key_ref: env:NEVER_READ\n"); !errors.Is(err, web.ErrCapabilityUnavailable) {
		t.Fatal(err)
	}
	if counter.gets != 0 || len(m.proofs) != 0 {
		t.Fatal("unavailable policy performed diagnostic or accepted proof", counter.gets)
	}
}
func TestCapabilityPresentationFreezesBeforeProbeAndRetainsAcceptedOutcome(t *testing.T) {
	m := fixtureManager(t)
	ctx := context.Background()
	initial := detail(t, m, "memory")
	t.Setenv("STEWARD_PRESENTATION_KEY", strings.Repeat("k", 32))
	calls := 0
	changed := false
	m.Presentation = func(_ context.Context, f contract.PresentationFacts) (contract.Presentation, error) {
		calls++
		out := v1.ProjectPresentation(f)
		if changed {
			out.Copy.ProbeMemoryReady = "changed result"
		} else {
			out.Copy.ProbeMemoryReady = "accepted result"
		}
		return out, nil
	}
	counter := &countedPresentationSecrets{SecretStore: m.secrets, before: func() { changed = true }}
	m.secrets = counter
	tested, err := m.Test(ctx, "memory", initial.Revision, "encryption_key_ref: env:STEWARD_PRESENTATION_KEY\n")
	if err != nil || !tested.Verified || tested.State != "ready" || tested.Message != "accepted result" || calls != 1 || counter.gets == 0 {
		t.Fatal("diagnostic reloaded current projection or lost true result", tested, err, calls, counter.gets)
	}
	retained := detail(t, m, "memory")
	if !retained.Verified || retained.Message != "accepted result" {
		t.Fatal("current projection rewrote original proof", retained)
	}
}
func TestCapabilityPresentationCannotChangeDiagnosticState(t *testing.T) {
	m := fixtureManager(t)
	ctx := context.Background()
	initial := detail(t, m, "memory")
	m.Presentation = func(_ context.Context, f contract.PresentationFacts) (contract.Presentation, error) {
		out := v1.ProjectPresentation(f)
		out.Copy.ProbeMemoryFailed = "presentation says ready"
		return out, nil
	}
	tested, err := m.Test(ctx, "memory", initial.Revision, "encryption_key_ref: env:STEWARD_MISSING_PRESENTATION_KEY\n")
	if err != nil || tested.Verified || tested.State != "unavailable" || tested.Message != "presentation says ready" {
		t.Fatal("presentation changed authenticated readiness", tested, err)
	}
}
