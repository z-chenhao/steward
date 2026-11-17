package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func artifactOwnerFixture(t *testing.T) (*Store, *Runs, rsi.Host) {
	t.Helper()
	store, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runs := store.Runs(nil, tool.FixtureRegistry())
	host := installArtifactWorkbench(t, store, runs)
	store.ConfigureArtifactHost(host.Catalog)
	if scalar(t, store, `SELECT count(*) FROM model_profile`) != 0 {
		t.Fatal("owner fixture unexpectedly initialized a model")
	}
	return store, runs, host
}

func createOwnerArtifact(t *testing.T, store *Store, name, media string, data []byte) string {
	t.Helper()
	ctx := context.Background()
	input, err := store.PrepareArtifactInput(ctx, artifact.Input{Name: name, MediaType: media})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Artifacts().Put(ctx, artifactOwner(t, store), input, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestArtifactWorkbenchOwnerSemanticFactSurvivesUpgradeRollbackAndColdRead(t *testing.T) {
	ctx := context.Background()
	variant := artifactChangedPackage(t, "1.2.0")
	store, _, host := artifactOwnerFixture(t)
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	old := createOwnerArtifact(t, store, "picture.png", "image/png", pixels.Bytes())
	describe := func(ref string) artifact.Descriptor {
		t.Helper()
		d, err := store.Artifacts().Describe(ctx, artifactOwner(t, store), ref)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := describe(old)
	if d.ContentType() != "image" || d.SemanticKind != "image" || d.MethodGraph != status.Revision {
		t.Fatal("new owner creation did not persist accepted type", d)
	}
	public, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "semantic_kind") || strings.Contains(string(public), "method_graph") || strings.Contains(string(public), status.Revision) {
		t.Fatal("private creation facts entered public descriptor", string(public))
	}
	policy := artifactOwnerPolicy("owner-media-v12")
	policy.Cases[0].Expected = json.RawMessage(`{"retention_days":30,"media_kinds":["audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)
	newGraph := activateArtifactPolicyPackage(t, host, variant, policy)
	current := createOwnerArtifact(t, store, "picture.png", "image/png", pixels.Bytes())
	if d := describe(current); d.ContentType() != "file" || d.SemanticKind != "file" || d.MediaType != "image/png" || d.MethodGraph != newGraph {
		t.Fatal("independent media policy not used", d)
	}
	if d := describe(old); d.ContentType() != "image" || d.MethodGraph != status.Revision {
		t.Fatal("old reference reinterpreted by current policy", d)
	}
	acceptArtifactOwnerGraph(t, host, status.Revision, artifactOwnerPolicy("owner-media-rollback"))
	rolledBack := createOwnerArtifact(t, store, "picture.png", "image/png", pixels.Bytes())
	if describe(rolledBack).ContentType() != "image" || describe(current).ContentType() != "file" {
		t.Fatal("rollback rewrote existing type facts")
	}
	if _, err := store.db.Exec(`UPDATE artifact SET semantic_kind='file' WHERE artifact_ref=?`, old); err == nil {
		t.Fatal("accepted semantic kind mutable")
	}
	if err := host.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareArtifactInput(ctx, artifact.Input{Name: "blocked.txt", MediaType: "text/plain"}); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("quarantine fell back to native", err)
	}
	root := store.DataRoot()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	owner := artifactOwner(t, cold)
	for ref, kind := range map[string]string{old: "image", current: "file", rolledBack: "image"} {
		d, err := cold.Artifacts().Describe(ctx, owner, ref)
		if err != nil || d.ContentType() != kind {
			t.Fatal("cold read depended on candidate health", d, err)
		}
		if readArtifact(t, cold, owner, ref) != pixels.String() {
			t.Fatal("kind change altered original image bytes")
		}
	}
	if err := cold.Artifacts().Delete(ctx, owner, old, 1); err != nil {
		t.Fatal("independent owner deletion required candidate", err)
	}
	if scalar(t, cold, `SELECT count(*) FROM model_profile`) != 0 {
		t.Fatal("owner creation/cold read initialized model")
	}
}

func TestArtifactWorkbenchOwnerCreationRechecksAcceptedFactsAfterStaging(t *testing.T) {
	ctx := context.Background()
	store, _, host := artifactOwnerFixture(t)
	owner := artifactOwner(t, store)
	input, err := store.PrepareArtifactInput(ctx, artifact.Input{Name: "original.txt", MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := store.Artifacts().Stage(ctx, input, strings.NewReader("original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	// Mutating a caller's pointer after staging cannot change the snapshot.
	input.Creation.RetentionDays = 1
	input.Creation.MediaKinds[0] = "file"
	var ref string
	if err := store.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		ref, err = store.Artifacts().CommitTx(ctx, tx, owner, stage, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if scalar(t, store, `SELECT (expires_at-created_at)/86400000000 FROM artifact WHERE artifact_ref=?`, ref) != 30 {
		t.Fatal("staging retained a mutable caller plan")
	}
	input, err = store.PrepareArtifactInput(ctx, artifact.Input{Name: "blocked.txt", MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.Artifacts().Stage(ctx, input, strings.NewReader("not published"))
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Store.Quarantine(ctx, status.Revision); err != nil {
		t.Fatal(err)
	}
	if err := store.Transaction(ctx, func(tx *sql.Tx) error { _, err := store.Artifacts().CommitTx(ctx, tx, owner, stale, ""); return err }); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("stale graph created metadata", err)
	}
	if scalar(t, store, `SELECT count(*) FROM artifact`) != 1 {
		t.Fatal("rejected publication left metadata")
	}
	if _, err := store.Artifacts().Put(ctx, owner, artifact.Input{Name: "bypass.txt", MediaType: "text/plain"}, strings.NewReader("bypass")); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("raw store bypassed managed owner boundary", err)
	}
}

func TestArtifactWorkbenchCleanupActualSelectionAndAuthority(t *testing.T) {
	ctx := context.Background()
	store, _, host := artifactOwnerFixture(t)
	owner := artifactOwner(t, store)
	batch := 2
	store.Artifacts().CleanupBatch = &batch
	refs := []string{}
	for _, name := range []string{"latest.txt", "oldest.txt", "middle.txt", "pinned.txt"} {
		refs = append(refs, createOwnerArtifact(t, store, name, "text/plain", []byte(name)))
	}
	if err := store.Transaction(ctx, func(tx *sql.Tx) error {
		return store.Artifacts().PinTx(ctx, tx, owner, refs[3], "fixture", "protected")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER artifact_immutable`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour).UnixMicro()
	for i, order := range []int64{30, 1, 2, 0} {
		if _, err := store.db.Exec(`UPDATE artifact SET created_at=?,expires_at=? WHERE artifact_ref=?`, now-int64(time.Hour.Microseconds()), now+order, refs[i]); err != nil {
			t.Fatal(err)
		}
	}
	report, err := store.CleanupArtifacts(ctx, true)
	if err != nil || !report.DryRun || !reflect.DeepEqual(report.Artifacts, []string{refs[1], refs[2]}) || scalar(t, store, `SELECT count(*) FROM artifact WHERE deleted_at IS NOT NULL`) != 0 {
		t.Fatal("actual cleanup order/window or dry-run changed", report, err)
	}
	report, err = store.CleanupArtifacts(ctx, false)
	if err != nil || !reflect.DeepEqual(report.Artifacts, []string{refs[1], refs[2]}) || report.Objects != 2 {
		t.Fatal("selected cleanup not applied/recovered", report, err)
	}
	if scalar(t, store, `SELECT count(*) FROM artifact WHERE artifact_ref=? AND deleted_at IS NULL`, refs[3]) != 1 {
		t.Fatal("policy deleted pinned file")
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Store.Deactivate(ctx, "owner-stop-cleanup", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CleanupArtifacts(ctx, true); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("deactivated cleanup used native", err)
	}
	if _, err := store.Artifacts().CleanupLegacy(ctx, owner, true); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("direct legacy cleanup bypassed boundary", err)
	}
	if _, err := store.Artifacts().Recover(ctx); err != nil {
		t.Fatal("low-level recovery depended on candidate", err)
	}
}

func TestArtifactWorkbenchCleanupRevalidatesWholeSetBeforeDeletion(t *testing.T) {
	ctx := context.Background()
	store, _, _ := artifactOwnerFixture(t)
	owner := artifactOwner(t, store)
	first := createOwnerArtifact(t, store, "first.txt", "text/plain", []byte("first"))
	second := createOwnerArtifact(t, store, "second.txt", "text/plain", []byte("second"))
	if _, err := store.db.Exec(`DROP TRIGGER artifact_immutable; UPDATE artifact SET created_at=1,expires_at=2`); err != nil {
		t.Fatal(err)
	}
	store.MainBoundary = func(name string) {
		if name == "before_artifact_cleanup_commit" {
			if err := store.Artifacts().Delete(ctx, owner, first, 1); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := store.CleanupArtifacts(ctx, false); !errors.Is(err, artifact.ErrConflict) {
		t.Fatal("stale selection was accepted", err)
	}
	if scalar(t, store, `SELECT count(*) FROM artifact WHERE artifact_ref=? AND deleted_at IS NULL`, second) != 1 {
		t.Fatal("partial set deleted before validation completed")
	}
}

func TestArtifactWorkbenchOwnerLegacyEvidenceIsMigrationOnlyAndActivationClosesIt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migrations[:68]); err != nil {
		t.Fatal(err)
	}
	if err := (&Store{db: db}).bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	// A component-level original carrier, not a substitute for the archived29
	// stock binary system proof. Preserve its exact existing execution fact.
	if _, err := db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) SELECT 'old-owner',agent_id,agent_id,'main','queued','old-cap','old-model','{"messages":[{"text":"original"}]}',0,1,1,1 FROM agent_config WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.ConfigureArtifactHost(rsi.NewCatalog(root))
	if scalar(t, store, `SELECT run_count FROM legacy_artifact_owner`) != 1 {
		t.Fatal("actual prior consumer not recorded")
	}
	input, err := store.PrepareArtifactInput(ctx, artifact.Input{Name: "legacy.png", MediaType: "image/png"})
	if err != nil || input.Creation != nil {
		t.Fatal("prior owner acquired a new recipe", err)
	}
	ref, err := store.Artifacts().Put(ctx, artifactOwner(t, store), input, strings.NewReader("original immutable bytes"))
	if err != nil {
		t.Fatal(err)
	}
	runs := store.Runs(nil, tool.FixtureRegistry())
	host := installArtifactWorkbench(t, store, runs)
	if _, err := store.Artifacts().Put(ctx, artifactOwner(t, store), artifact.Input{Name: "bypass", MediaType: "text/plain"}, strings.NewReader("blocked")); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("activation retained native owner fallback", err)
	}
	status, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Store.Deactivate(ctx, "legacy-owner-disable", status.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareArtifactInput(ctx, input); !errors.Is(err, artifact.ErrUnavailable) {
		t.Fatal("deactivation revived old entitlement", err)
	}
	d, err := store.Artifacts().Describe(ctx, artifactOwner(t, store), ref)
	if err != nil || d.MethodGraph != "" || d.SemanticKind != "" || d.ContentType() != "image" {
		t.Fatal("original NULL reference reinterpreted", d, err)
	}
	if _, err := store.db.Exec(`INSERT OR REPLACE INTO legacy_artifact_owner VALUES(1,1,0,0)`); err == nil {
		t.Fatal("new callers forged historical owner boundary")
	}
}

func TestArtifactWorkbenchCleanupActualIndependentOrderingUpgrade(t *testing.T) {
	ctx := context.Background()
	variant := artifactChangedPackage(t, "1.3.0")
	store, _, host := artifactOwnerFixture(t)
	first := createOwnerArtifact(t, store, "earlier.txt", "text/plain", []byte("earlier"))
	last := createOwnerArtifact(t, store, "later.txt", "text/plain", []byte("later"))
	if _, err := store.db.Exec(`DROP TRIGGER artifact_immutable; UPDATE artifact SET created_at=1,expires_at=2; UPDATE artifact SET expires_at=3 WHERE artifact_ref=?`, last); err != nil {
		t.Fatal(err)
	}
	batch := 1
	store.Artifacts().CleanupBatch = &batch
	before, err := store.CleanupArtifacts(ctx, true)
	if err != nil || !reflect.DeepEqual(before.Artifacts, []string{first}) {
		t.Fatal(before, err)
	}
	policy := artifactOwnerPolicy("cleanup-reversed-v13")
	policy.Cases[3].Expected = json.RawMessage(`{"indices":[0,2],"limit":2}`)
	activateArtifactPolicyPackage(t, host, variant, policy)
	after, err := store.CleanupArtifacts(ctx, true)
	if err != nil || !reflect.DeepEqual(after.Artifacts, []string{last}) {
		t.Fatal("actual ordering stayed in native Host", after, err)
	}
	if scalar(t, store, `SELECT count(*) FROM artifact WHERE deleted_at IS NOT NULL`) != 0 {
		t.Fatal("policy update mutated old file facts")
	}
}
