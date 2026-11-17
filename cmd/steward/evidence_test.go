package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	conversationsqlite "github.com/z-chenhao/assistant/internal/conversation/sqlite"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/llm/secret"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
)

func TestEvidenceCLIReadsOriginalAcrossReopenedBoundedPages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEWARD_DATA_ROOT", filepath.Join(root, "data"))
	text := strings.Repeat("original 中文 🧭\n", 1000)
	var saved, errOut bytes.Buffer
	// This fixture represents a retained original reference. Its explicit
	// native test carrier is limited to creation; every evidence command below
	// uses the ordinary owner control path with broken execution configuration.
	// Fresh stock creation is covered by the mandatory full30 Artifact CLI gate.
	if err := run(context.Background(), []string{"-test-bridge", "main", "artifact", "save", "original.txt", "text/plain"}, strings.NewReader(text), &saved, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	var descriptor artifact.Descriptor
	if json.Unmarshal(saved.Bytes(), &descriptor) != nil || descriptor.Ref == "" {
		t.Fatal("save output", saved.String())
	}
	config := filepath.Join(root, "broken-execution.yaml")
	if err := os.WriteFile(config, []byte("version: 1\njavascript:\n  command: /missing/node\n  script: /missing/script.mjs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	q := evidence.Request{Ref: descriptor.Ref}
	var joined strings.Builder
	for {
		input, _ := json.Marshal(q)
		var out, errOut bytes.Buffer
		if err := run(context.Background(), []string{"-config", config, "evidence", "read"}, bytes.NewReader(input), &out, &errOut); err != nil {
			t.Fatal(err, errOut.String())
		}
		var page evidence.Page
		if json.Unmarshal(out.Bytes(), &page) != nil || page.Ref != descriptor.Ref || page.Kind != "artifact" || page.Bytes > 4096 {
			t.Fatal(out.String())
		}
		joined.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		q.Offset = *page.NextOffset
	}
	if joined.String() != text {
		t.Fatal("CLI changed or omitted original bytes")
	}
	for _, input := range []string{`{"ref":"current","agent_id":"forged"}`, `{"ref":"memory:missing"}`, `{"ref":"artifact:missing"}`} {
		var out, errOut bytes.Buffer
		if err := run(context.Background(), []string{"-config", config, "evidence", "read"}, strings.NewReader(input), &out, &errOut); err == nil || out.Len() != 0 {
			t.Fatal("invalid or inaccessible evidence accepted", input, out.String(), err)
		}
	}
}

func TestMemoryEvidenceCLIUsesVaultWithoutEmbeddingOrProtectionWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	t.Setenv("STEWARD_DATA_ROOT", data)
	t.Setenv("STEWARD_EVIDENCE_SYNTHETIC_KEY", strings.Repeat("a", 32))
	setup := filepath.Join(root, "setup.yaml")
	config := filepath.Join(root, "memory.yaml")
	if err := os.WriteFile(setup, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("version: 1\nmemory:\n  encryption_key_ref: env:STEWARD_EVIDENCE_SYNTHETIC_KEY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var ignored bytes.Buffer
	if err := run(ctx, []string{"-config", setup, "status"}, strings.NewReader(""), &ignored, &ignored); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var owner string
	if err = db.QueryRow(`SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = db.Exec(`INSERT INTO task(task_id,task_ref,agent_id,conversation_id,title,anchor_task,anchor_artifact_refs,status,task_epoch,created_at,updated_at,version)
 VALUES('fixture-task','task:owner-task',?,?,'Owner task','Original work','[]','active',1,?,?,1)`, owner, owner, now.UnixMicro(), now.UnixMicro()); err != nil {
		t.Fatal(err)
	}
	history, err := conversationsqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.Append(ctx, []conversation.Message{{MessageID: "owner-message", ConversationID: owner, Role: "user", Content: conversation.Text("Original message"), Source: "cli", CreatedAt: now}}); err != nil {
		history.Close()
		t.Fatal(err)
	}
	if err = history.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := memorysqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureVault(memory.Vault{Secrets: secret.Environment{}, Ref: "env:STEWARD_EVIDENCE_SYNTHETIC_KEY"}); err != nil {
		m.Close()
		t.Fatal(err)
	}
	const marker = "SYNTHETIC_DECRYPTED_MEMORY_EVIDENCE"
	text := marker + strings.Repeat("原文 🧭\n", 950)
	fact := memory.Record{ID: "exact", UserID: artifact.LocalUser, ScopeType: "agent", ScopeID: owner, Tier: "recall", Content: text, CreatedAt: now, SourceRefs: []memory.SourceRef{
		{Type: "conversation", Ref: "conversation:owner-message"}, {Type: "task", Ref: "task:owner-task"}, {Type: "run", Ref: "run:private"},
	}}
	if err = m.Apply(ctx, fact); err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	// Keep a genuine legacy active document alongside the sealed record.
	m, err = memorysqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for id, content := range map[string]string{"legacy": "Visible legacy fact", "invalid-seal": "sealed:v1:invalid"} {
		fact.ID, fact.Content = id, content
		if err = m.Apply(ctx, fact); err != nil {
			m.Close()
			t.Fatal(err)
		}
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	invoke := func(path string, q evidence.Request) ([]byte, error) {
		t.Helper()
		raw, _ := json.Marshal(q)
		var out, errOut bytes.Buffer
		err := run(ctx, []string{"-config", path, "evidence", "read"}, bytes.NewReader(raw), &out, &errOut)
		return out.Bytes(), err
	}
	q := evidence.Request{Ref: "memory:exact"}
	var original strings.Builder
	for {
		raw, err := invoke(config, q)
		var page evidence.Page
		if err != nil || json.Unmarshal(raw, &page) != nil || page.Ref != q.Ref || page.Kind != "memory" || page.Offset != q.Offset || page.Bytes > 4096 ||
			len(page.Links) != 2 || page.Links[0].Ref != "conversation:owner-message" || page.Links[1].Ref != "task:owner-task" {
			t.Fatal("owner CLI lost the bounded record or its scoped sources", err)
		}
		original.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= q.Offset {
			t.Fatal("memory cursor did not advance")
		}
		q.Offset = *page.NextOffset
	}
	if original.String() != text {
		t.Fatal("owner CLI changed the original text")
	}
	if raw, err := invoke(config, evidence.Request{Ref: "memory:legacy"}); err != nil || !bytes.Contains(raw, []byte("Visible legacy fact")) {
		t.Fatal("existing active plaintext could not be read", err)
	}
	unavailable := func(path, ref string) {
		t.Helper()
		if raw, err := invoke(path, evidence.Request{Ref: ref}); err == nil || err.Error() != "memory evidence unavailable" || len(raw) != 0 {
			t.Fatal("sealed record did not fail closed", err)
		}
	}
	unavailable(config, "memory:invalid-seal")
	unavailable(setup, "memory:exact")
	t.Setenv("STEWARD_EVIDENCE_SYNTHETIC_KEY", strings.Repeat("b", 32))
	unavailable(config, "memory:exact")
	if raw, err := invoke(config, evidence.Request{Ref: "memory:missing"}); !errors.Is(err, artifact.ErrNotFound) || len(raw) != 0 {
		t.Fatal("missing memory did not return the scoped error", err)
	}
	memDB, err := sql.Open("sqlite", filepath.Join(data, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer memDB.Close()
	var legacy string
	if err = memDB.QueryRow(`SELECT content FROM memory_record WHERE memory_id='legacy'`).Scan(&legacy); err != nil || legacy != "Visible legacy fact" {
		t.Fatal("evidence startup rewrote legacy representation", err)
	}
	for _, table := range []string{"run", "tool_invocation", "memory_mutation", "artifact", "conversation_intent"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("evidence read persisted new work", table, err)
		}
	}
	if err = filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(path)
		if bytes.Contains(b, []byte(marker)) {
			t.Errorf("decrypted evidence persisted in %s", filepath.Base(path))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
