package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/memory"
)

type cardsObservedStream struct {
	io.ReadCloser
	closed bool
	reads  int
}

func (s *cardsObservedStream) Read(p []byte) (int, error) { s.reads++; return s.ReadCloser.Read(p) }
func (s *cardsObservedStream) Close() error               { s.closed = true; return s.ReadCloser.Close() }

type cardsRevokingArtifacts struct {
	artifactStore
	afterOpen func() error
	source    *cardsObservedStream
}

func (s *cardsRevokingArtifacts) Open(ctx context.Context, scope artifact.Scope, ref string) (io.ReadCloser, error) {
	source, err := s.artifactStore.Open(ctx, scope, ref)
	if err != nil {
		return nil, err
	}
	s.source = &cardsObservedStream{ReadCloser: source}
	if err = s.afterOpen(); err != nil {
		_ = s.source.Close()
		return nil, err
	}
	return s.source, nil
}

// The owning system gate supplies a real published Cards Run, sealed package
// and Artifact. Copies are made only while that Host is closed; destructive
// controls have separate databases/files, with no fixture repair or reselect.
func TestUICardsActualOpenRevocationFence(t *testing.T) {
	path := os.Getenv("STEWARD_TEST_CARDS_CONFIG")
	if path == "" {
		t.Skip("requires actual Cards App system fixture")
	}
	original, err := config.Load(path, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	ref, resource := os.Getenv("STEWARD_TEST_CARDS_REF"), os.Getenv("STEWARD_TEST_CARDS_ARTIFACT")
	if ref == "" || resource == "" {
		t.Fatal("original Cards identities required")
	}
	for _, operation := range []string{"delete", "forget"} {
		t.Run(operation, func(t *testing.T) {
			cfg := original
			cfg.App.DataRoot = t.TempDir()
			if err := os.CopyFS(cfg.App.DataRoot, os.DirFS(original.App.DataRoot)); err != nil {
				t.Fatal(err)
			}
			// Enable the actual owner memory control using the real Memory store and
			// existing vault configuration. No privacy rows are edited by this test.
			t.Setenv("STEWARD_TEST_CARDS_MEMORY_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			cfg.Memory = &memory.Config{EncryptionKeyRef: "env:STEWARD_TEST_CARDS_MEMORY_KEY"}
			ctx := context.Background()
			a, err := OpenControl(ctx, cfg, "", "")
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			// Supply the real owning Memory adapter to this control-only fixture.
			// Published UI facts and its frozen Run graph are untouched.
			a.runtimeRuns().ConfigureMemory(a.memory, memory.Vault{Secrets: a.control.Secrets, Ref: cfg.Memory.EncryptionKeyRef})
			before, err := a.Plugins().Store.Status(ctx)
			if before.Revision != "" {
				t.Fatal("control-only cold open reactivated disabled graph", before)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.WebCardsView(ctx, ref); err != nil {
				t.Fatal("original view unavailable before control", err)
			}
			destination, err := a.WebCardsLink(ctx, ref, 0)
			if err != nil || destination != "https://example.com/alpha" {
				t.Fatal("original link unavailable", destination, err)
			}
			source, media, err := a.OpenWebCardsImage(ctx, ref, 0)
			if err != nil {
				t.Fatal("original real Artifact unavailable", err)
			}
			data, err := io.ReadAll(source)
			closeErr := source.Close()
			if err != nil || closeErr != nil || media != "image/png" || !bytes.HasPrefix(data, []byte{137, 80, 78, 71}) {
				t.Fatal("real original PNG missing", media, err, closeErr)
			}
			descriptor, err := a.DescribeArtifact(ctx, resource)
			if err != nil {
				t.Fatal(err)
			}
			var controlErr error
			controlAccepted := false
			wrapped := &cardsRevokingArtifacts{artifactStore: a.artifacts}
			wrapped.afterOpen = func() error {
				if operation == "delete" {
					controlErr = a.DeleteArtifact(ctx, resource, descriptor.Version)
					controlAccepted = controlErr == nil
					return controlErr
				}
				result, err := a.ManageMemory(ctx, "cards-forget-during-open", memory.Mutation{Operation: "forget", Category: "preferences"})
				controlErr = err
				controlAccepted = err == nil && result["accepted"] == true
				if err == nil && result["accepted"] != true {
					t.Fatal("Forget not accepted", result)
				}
				return err
			}
			a.artifacts = wrapped
			source, _, err = a.OpenWebCardsImage(ctx, ref, 0)
			if !controlAccepted || controlErr != nil {
				t.Fatal("real control action was not accepted", operation, controlErr)
			}
			if err == nil || source != nil {
				if source != nil {
					_ = source.Close()
				}
				t.Fatal("revoked image escaped App after real open", operation, err)
			}
			if wrapped.source == nil || !wrapped.source.closed || wrapped.source.reads != 0 {
				t.Fatal("revoked opened stream was not closed unread", wrapped.source)
			}
			if _, _, err = a.OpenWebCardsImage(ctx, ref, 0); err == nil {
				t.Fatal("revoked image revived")
			}
			if operation == "forget" {
				if _, err = a.WebCardsView(ctx, ref); err == nil {
					t.Fatal("Forget revived view")
				}
				if _, err = a.WebCardsLink(ctx, ref, 0); err == nil {
					t.Fatal("Forget revived original link")
				}
			} else {
				deleted, describeErr := a.DescribeArtifact(ctx, resource)
				if describeErr != nil || deleted.DeletedAt == nil || deleted.Version <= descriptor.Version {
					t.Fatal("owner tombstone metadata missing", deleted, describeErr)
				}
				if reopened, openErr := a.OpenArtifact(ctx, resource); openErr == nil {
					_ = reopened.Close()
					t.Fatal("deleted Artifact bytes still readable")
				}
				if _, err = a.WebCardsLink(ctx, ref, 0); err != nil {
					t.Fatal("Artifact deletion incorrectly revoked independent link", err)
				}
			}
			after, err := a.Plugins().Store.Status(ctx)
			if err != nil || before.Revision != after.Revision {
				t.Fatal("test repaired selection", before, after, err)
			}
		})
	}
}
