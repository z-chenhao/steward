package app

import (
	"context"
	"errors"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/config"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/skill"
)

func TestExperienceReviewerCanInspectPrivateQueueWithoutPublicationDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := open(ctx, cfg, "", secretmemory.New(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.skillConfig = nil
	if _, err = a.ReviewExperience(ctx, skill.ReviewRequest{Operation: "list"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReviewExperience(ctx, skill.ReviewRequest{Operation: "promote", CandidateRef: "experience:unknown", Version: 1}); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("missing publication directory accepted", err)
	}
}
