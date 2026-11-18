package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
)

func TestCodingDevelopmentExactFreshStewardBuild(t *testing.T) {
	image, source := os.Getenv("STEWARD_TEST_CODING_DEVELOPMENT_IMAGE"), os.Getenv("STEWARD_TEST_CODING_SOURCE")
	if image == "" || source == "" {
		t.Skip("requires explicit prepared image and exact fresh source inventory")
	}
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE", image)
	raw, err := os.ReadFile(filepath.Join(source, "coding.project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document contract.ProjectDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	s, r, a, _ := queuedCodingProjectSource(t, &document, source, "coding.project.json")
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	result, receipt := projectEvidenceReceipt(t, s)
	if receipt.Outcome != "passed" || len(receipt.Evidence) != 1 || len(result.ArtifactRefs) != 1 || len(readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[0])) == 0 {
		t.Fatal("exact candidate did not build and execute its own binary", result, receipt)
	}
	if receipt.DocumentChanged || receipt.Image != image || receipt.EnvironmentSHA256 == "" || receipt.CandidateSHA256 == "" {
		t.Fatal("build provenance unavailable or rebound", receipt)
	}
	var authorDir string
	if err := s.db.QueryRow(`SELECT directory FROM task_code_environment`).Scan(&authorDir); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(codingTestInventory(t, source), codingTestInventory(t, authorDir)) {
		t.Fatal("imported candidate differs from the original source inventory")
	}
	if folder := os.Getenv("STEWARD_CODING_DEVELOPMENT_EVIDENCE"); folder != "" {
		folder = filepath.Join(folder, "steward-build")
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.WriteFile(filepath.Join(folder, "receipt.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "bridge-version.txt"), []byte(readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[0])), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Compare relative names, original bytes and executable bits independently of the import scanner.
type codingTestFile struct {
	hash       [32]byte
	executable bool
}

func codingTestInventory(t *testing.T, directory string) map[string]codingTestFile {
	t.Helper()
	files := make(map[string]codingTestFile)
	root := os.DirFS(directory)
	err := fs.WalkDir(root, ".", func(name string, item fs.DirEntry, err error) error {
		if err != nil || item.IsDir() {
			return err
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("unexpected candidate entry %s", name)
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		files[name] = codingTestFile{sha256.Sum256(data), info.Mode().Perm()&0111 != 0}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCodingDevelopmentParallelPortDatabaseAndBrowserProfiles(t *testing.T) {
	image := os.Getenv("STEWARD_TEST_CODING_DEVELOPMENT_IMAGE")
	if image == "" {
		t.Skip("requires explicit prepared Go/Node/browser development image")
	}
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE", image)
	source, err := filepath.Abs("../../../tests/fixtures/coding-development")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	type work struct {
		store             *Store
		actor             agent.Actor
		marker, directory string
	}
	var jobs []work
	for _, marker := range []string{"parallel-alpha", "parallel-beta"} {
		var document contract.ProjectDocument
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		document.Checks[0].Command = "touch .state/verifying; while ! test -f .state/release; do sleep 0.01; done; " + strings.Replace(document.Checks[0].Command, "candidate-alpha", marker, 1)
		s, r, a, _ := queuedCodingProjectSource(t, &document, source, "")
		approveCodingCheck(t, s, r)
		jobs = append(jobs, work{store: s, actor: a, marker: marker})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	done := make(chan error, len(jobs))
	for _, job := range jobs {
		go func() { done <- job.actor.Drain(ctx) }()
	}
	received := 0
	defer func() {
		cancel()
		for received < len(jobs) {
			<-done
			received++
		}
	}()
	for i := range jobs {
		for {
			_ = jobs[i].store.db.QueryRow(`SELECT directory FROM coding_project_resource`).Scan(&jobs[i].directory)
			if jobs[i].directory != "" {
				if _, err := os.Stat(filepath.Join(jobs[i].directory, ".state/verifying")); err == nil {
					break
				}
			}
			select {
			case err := <-done:
				received++
				t.Fatal("a real environment ended before the shared overlap barrier", err)
			case <-ctx.Done():
				t.Fatal("two real services did not overlap", ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if jobs[0].directory == jobs[1].directory {
		t.Fatal("two environments shared a writable candidate")
	}
	// Both actual servers have passed doctor at the same port before either
	// browser flow proceeds. This release is a concurrency barrier, not recovery.
	for _, job := range jobs {
		if err := os.WriteFile(filepath.Join(job.directory, ".state/release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range jobs {
		err := <-done
		received++
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range jobs {
		assertCodingDevelopmentResult(t, job.store, job.marker)
	}
}

func TestCodingDevelopmentActualSQLiteBrowserRecoveryAndRetainedArtifacts(t *testing.T) {
	image := os.Getenv("STEWARD_TEST_CODING_DEVELOPMENT_IMAGE")
	if image == "" {
		t.Skip("requires explicit prepared Go/Node/browser development image")
	}
	t.Setenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE", image)
	source, err := filepath.Abs("../../../tests/fixtures/coding-development")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document contract.ProjectDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	s, r, a, _ := queuedCodingProjectSource(t, &document, source, "")
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	assertCodingDevelopmentResult(t, s, "candidate-alpha")
}

func assertCodingDevelopmentResult(t *testing.T, s *Store, marker string) {
	t.Helper()
	result, receipt := projectEvidenceReceipt(t, s)
	if receipt.Outcome != "passed" || len(receipt.Evidence) != 2 || len(result.ArtifactRefs) != 2 || len(result.ImageRefs) != 1 {
		t.Fatal("actual database/browser lifecycle failed", result, receipt)
	}
	var assertions struct {
		Marker                 string            `json:"marker"`
		Port                   int               `json:"port"`
		InitialRows            []json.RawMessage `json:"initialRows"`
		PersistedRows          []string          `json:"persistedRows"`
		BrowserProfileRetained bool              `json:"browserProfileRetained"`
		Recovery               struct {
			OriginalPID  int `json:"originalPID"`
			RecoveredPID int `json:"recoveredPID"`
		} `json:"recovery"`
	}
	if err := json.Unmarshal([]byte(readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[1])), &assertions); err != nil {
		t.Fatal(err)
	}
	if assertions.Marker != marker || assertions.Port != 3000 || len(assertions.InitialRows) != 0 || len(assertions.PersistedRows) != 1 || assertions.PersistedRows[0] != assertions.Marker || !assertions.BrowserProfileRetained || assertions.Recovery.OriginalPID <= 0 || assertions.Recovery.RecoveredPID <= 0 || assertions.Recovery.OriginalPID == assertions.Recovery.RecoveredPID {
		t.Fatal("real persisted effects or original recovery not verified", assertions)
	}
	metadata, err := s.Artifacts().Describe(context.Background(), artifactOwner(t, s), result.ArtifactRefs[0])
	if err != nil || metadata.ByteSize < 1024 || metadata.MediaType != "image/png" {
		t.Fatal("actual nonempty browser screenshot unavailable", metadata, err)
	}
	if folder := os.Getenv("STEWARD_CODING_DEVELOPMENT_EVIDENCE"); folder != "" {
		folder = filepath.Join(folder, marker)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"flow.png", "assertions.json"} {
			if err := os.WriteFile(filepath.Join(folder, name), []byte(readArtifact(t, s, artifactOwner(t, s), result.ArtifactRefs[i])), 0600); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.WriteFile(filepath.Join(folder, "receipt.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND attempt_count=1`) != 1 {
		t.Fatal("browser flow repeated or environment unreleased")
	}
}
