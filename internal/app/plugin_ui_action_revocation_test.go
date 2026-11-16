package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/memory"
)

// A copy of the real stopped App fixture proves actual owner revocation without
// rewriting original lifecycle evidence or changing the selected plugin graph.
func TestUIActionActualForgetRevokesOriginalFeedbackAndReceipt(t *testing.T) {
	path, messageID, ref := os.Getenv("STEWARD_TEST_ACTION_CONFIG"), os.Getenv("STEWARD_TEST_ACTION_MESSAGE"), os.Getenv("STEWARD_TEST_ACTION_REF")
	if path == "" {
		t.Skip("requires actual action App system fixture")
	}
	source := os.Getenv("STEWARD_TEST_ACTION_SOURCE")
	if messageID == "" || ref == "" || source == "" {
		t.Fatal("original delivered identity required")
	}
	original, err := config.Load(path, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := original
	cfg.App.DataRoot = t.TempDir()
	if err = os.CopyFS(cfg.App.DataRoot, os.DirFS(original.App.DataRoot)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_TEST_ACTION_MEMORY_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	cfg.Memory = &memory.Config{EncryptionKeyRef: "env:STEWARD_TEST_ACTION_MEMORY_KEY"}
	ctx := context.Background()
	app, err := OpenControl(ctx, cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.runtimeRuns().ConfigureMemory(app.memory, memory.Vault{Secrets: app.control.Secrets, Ref: cfg.Memory.EncryptionKeyRef})
	before, err := app.Plugins().Store.Status(ctx)
	if err != nil || before.Revision != "" {
		t.Fatal("cold control reactivated graph", before, err)
	}
	page, err := app.Conversation(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	for _, m := range page.Messages {
		if m.MessageID == messageID {
			message = m
			break
		}
	}
	if message.MessageID == "" {
		t.Fatal("real original conversation unavailable")
	}
	raw, err := app.WebProjection(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if json.Unmarshal(raw, &spec) != nil || spec.ActionViews[ref].UnknownError != "Result unknown. Check the conversation." {
		t.Fatal("original private display unavailable before Forget")
	}
	if receipt, err := app.WebUIActionResult(ctx, ref, source); err != nil || receipt.Status != "succeeded" {
		t.Fatal("original action receipt unavailable", receipt, err)
	}
	result, err := app.ManageMemory(ctx, "action-owner-forget", memory.Mutation{Operation: "forget", Category: "preferences"})
	if err != nil || result["accepted"] != true {
		t.Fatal("real Forget rejected", result, err)
	}
	// First dependent read follows the real owner action, with no graph repair.
	if raw, err = app.WebProjection(ctx, message); err == nil && bytes.Contains(raw, []byte(`"action_views"`)) {
		t.Fatal("Forget retained action display in authorized projection")
	}
	if _, err = app.WebUIActionResult(ctx, ref, source); err == nil {
		t.Fatal("Forget retained original action receipt grant")
	}
	after, err := app.Plugins().Store.Status(ctx)
	if err != nil || after.Revision != before.Revision {
		t.Fatal("control changed selection", after, err)
	}
}
