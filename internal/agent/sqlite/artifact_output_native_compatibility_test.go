package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

type artifactOutputNativeObject struct {
	Name             string `json:"name"`
	Media            string `json:"media_type"`
	Bytes            int64  `json:"bytes"`
	Hash             string `json:"hash"`
	RetentionSeconds int64  `json:"retention_seconds"`
	Visibility       string `json:"visibility"`
}
type artifactOutputNativeCase struct {
	ID           string                       `json:"id"`
	Kind         string                       `json:"kind"`
	Policy       artifact.Policy              `json:"policy"`
	SourceBodies [][]byte                     `json:"source_bodies,omitempty"`
	Receipt      string                       `json:"receipt,omitempty"`
	Objects      []artifactOutputNativeObject `json:"objects,omitempty"`
	ReplayExact  bool                         `json:"replay_exact,omitempty"`
	Eligible     []string                     `json:"eligible,omitempty"`
	Deleted      []string                     `json:"deleted,omitempty"`
	Orphans      int                          `json:"orphan_objects"`
	Dry          bool                         `json:"dry_run,omitempty"`
}

func TestArtifactWorkbenchActualOutputAndCleanupMatchIndependentNativeOracle(t *testing.T) {
	// Read only the sealed pre-plugin native oracle. This test never rewrites it.
	original, err := os.ReadFile("../../plugin/artifactworkbench/testdata/native-output-cleanup.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	if hex.EncodeToString(sum[:]) != "0c9c98d38861536905741070f44d1eb5b6322561ed0f69d2e342b1bb893783f7" {
		t.Fatal("independent native oracle changed")
	}
	var expected []artifactOutputNativeCase
	if json.Unmarshal(original, &expected) != nil || len(expected) != 14 {
		t.Fatal("incomplete independent oracle")
	}
	ctx := context.Background()
	cases := []artifactOutputNativeCase{{ID: "original-default-policy", Kind: "defaults", Policy: actualArtifactDefaults(t)}}
	for _, id := range []string{"inline-boundary", "overflow-one-byte", "unicode-overflow", "explicit-empty", "explicit-binary", "explicit-json-receipt", "two-attachments"} {
		t.Run(id, func(t *testing.T) {
			bodies := [][]byte{}
			def := tool.Definition{Spec: tool.Spec{Name: "mcp__fixture__artifact_output", Version: 1, InputSchema: `{"type":"object","additionalProperties":false}`, Permission: "read_only", ReplayPolicy: "safe", TimeoutMS: 10000, OutputBytes: 1024, Provider: "fixture", Risk: "low", ConflictPolicy: "none"}}
			def.Execute = func(context.Context, string) tool.Result {
				switch id {
				case "inline-boundary":
					v := strings.Repeat("x", 1024)
					bodies = [][]byte{[]byte(v)}
					return tool.Result{Text: v}
				case "overflow-one-byte":
					v := strings.Repeat("x", 1025)
					bodies = [][]byte{[]byte(v)}
					return tool.Result{Text: v}
				case "unicode-overflow":
					v := strings.Repeat("你🙂x", 600)
					bodies = [][]byte{[]byte(v)}
					return tool.Result{Text: v}
				case "explicit-empty":
					bodies = [][]byte{{}}
					return tool.Result{Output: &tool.BinaryOutput{Name: "empty.bin", MediaType: "application/octet-stream", Reader: strings.NewReader("")}}
				case "explicit-binary":
					v := []byte{0, 255, 1, 128, 13, 10}
					bodies = [][]byte{v}
					return tool.Result{Output: &tool.BinaryOutput{Name: "原件.bin", MediaType: "application/octet-stream", Reader: strings.NewReader(string(v))}}
				case "explicit-json-receipt":
					v := `{"ready":true}`
					bodies = [][]byte{[]byte(v)}
					return tool.Result{Output: &tool.BinaryOutput{Name: "receipt.json", MediaType: "application/json", Reader: strings.NewReader(v), ResultJSON: func(ref string) string { return fmt.Sprintf(`{"file_id":%q,"status":"ready"}`, tool.ObjectID(ref)) }}}
				default:
					bodies = [][]byte{[]byte("one"), []byte("二号")}
					return tool.Result{Text: "two outputs ready", Attachments: []tool.AttachmentOutput{{BinaryOutput: tool.BinaryOutput{Name: "one.txt", MediaType: "text/plain", Reader: strings.NewReader("one")}}, {BinaryOutput: tool.BinaryOutput{Name: "two.txt", MediaType: "text/plain", Reader: strings.NewReader("二号")}}}}
				}
			}
			calls := []llm.ToolCall{{ID: "original-output", Name: def.Spec.Name, Arguments: `{}`}}
			s, r, claim, executor, exchange := admitPolicyRun(t, []tool.Definition{def}, calls, false, func(s *Store, r *Runs) { installArtifactWorkbench(t, s, r) })
			defer r.Release(ctx, claim)
			out, e := executor.Execute(ctx, exchange, calls)
			if e != nil || len(out) != 1 || out[0].Error != nil {
				t.Fatal(out, e)
			}
			row := artifactOutputNativeCase{ID: id, Kind: "output", Policy: s.Artifacts().Policy, SourceBodies: bodies, Receipt: out[0].Text, Objects: []artifactOutputNativeObject{}}
			for i, ref := range out[0].ArtifactRefs {
				d, e := s.Artifacts().Describe(ctx, artifactOwner(t, s), ref)
				if e != nil {
					t.Fatal(e)
				}
				if d.MethodGraph == "" || d.SemanticKind == "" {
					t.Fatal("managed output fell back to native creation", d)
				}
				actual := []byte(readArtifact(t, s, artifactOwner(t, s), ref))
				if !reflect.DeepEqual(actual, bodies[i]) {
					t.Fatal("native output lost source bytes")
				}
				var ttl int64
				var visibility string
				if e = s.db.QueryRow(`SELECT (expires_at-created_at)/1000000,visibility FROM artifact WHERE artifact_ref=?`, ref).Scan(&ttl, &visibility); e != nil {
					t.Fatal(e)
				}
				row.Objects = append(row.Objects, artifactOutputNativeObject{d.Name, d.MediaType, d.ByteSize, d.ContentHash, ttl, visibility})
				row.Receipt = strings.ReplaceAll(strings.ReplaceAll(row.Receipt, ref, "@output"), tool.ObjectID(ref), "@output")
			}
			again, e := executor.Execute(ctx, exchange, calls)
			if e != nil || !reflect.DeepEqual(out, again) {
				t.Fatal("native completed output replay changed", e)
			}
			row.ReplayExact = true
			cases = append(cases, row)
		})
	}
	t.Run("explicit-owner-cleanup", func(t *testing.T) {
		s, _, _ := artifactOwnerFixture(t)
		owner := artifactOwner(t, s)
		store := s.Artifacts()
		aliases := map[string]string{}
		refs := []string{}
		for _, name := range []string{"oldest-expired", "pinned-expired", "newer-expired"} {
			ref := createOwnerArtifact(t, s, name, "text/plain", []byte(name))
			aliases[ref] = name
			refs = append(refs, ref)
		}
		if e := s.Transaction(ctx, func(tx *sql.Tx) error {
			return store.PinTx(ctx, tx, owner, refs[1], "conversation", "original-message")
		}); e != nil {
			t.Fatal(e)
		}
		if e := store.Delete(ctx, owner, refs[1], 1); !errors.Is(e, artifact.ErrReferenced) {
			t.Fatal("native explicit delete ignored original reference", e)
		}
		// Controlled-clock fixture, like the owning native retention regression.
		// This never changes a real user root or a production mutation mechanism.
		if _, e := s.db.Exec(`DROP TRIGGER artifact_immutable`); e != nil {
			t.Fatal(e)
		}
		for i, ref := range refs {
			if _, e := s.db.Exec(`UPDATE artifact SET expires_at=? WHERE artifact_ref=?`, i+1, ref); e != nil {
				t.Fatal(e)
			}
		}
		emit := func(id string, dry bool) {
			report, e := s.CleanupArtifacts(ctx, dry)
			if e != nil {
				t.Fatal(e)
			}
			policy := store.Policy
			if store.CleanupBatch != nil {
				policy.CleanupBatch = *store.CleanupBatch
			}
			row := artifactOutputNativeCase{ID: id, Kind: "cleanup", Policy: policy, Eligible: []string{}, Deleted: []string{}, Orphans: report.Objects, Dry: report.DryRun}
			for _, ref := range report.Artifacts {
				if aliases[ref] == "" {
					t.Fatal("foreign cleanup source")
				}
				row.Eligible = append(row.Eligible, aliases[ref])
			}
			rows, e := s.db.Query(`SELECT artifact_ref FROM artifact WHERE deleted_at IS NOT NULL ORDER BY name`)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var ref string
				if e = rows.Scan(&ref); e != nil {
					t.Fatal(e)
				}
				row.Deleted = append(row.Deleted, aliases[ref])
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
			cases = append(cases, row)
		}
		emit("dry-default", true)
		batch := 1
		store.CleanupBatch = &batch
		emit("dry-one", true)
		emit("apply-one", false)
		emit("dry-after-first", true)
		emit("apply-second", false)
		emit("dry-completed", true)
		var live int
		if e := s.db.QueryRow(`SELECT count(*) FROM artifact WHERE artifact_ref=? AND deleted_at IS NULL`, refs[1]).Scan(&live); e != nil || live != 1 {
			t.Fatal("native cleanup deleted referenced source", e)
		}
	})
	if t.Failed() {
		return
	}
	if len(cases) != 14 {
		t.Fatal("incomplete native output/lifecycle capture", len(cases))
	}
	for i, row := range cases {
		a, e := json.Marshal(row)
		if e != nil {
			t.Fatal(e)
		}
		b, e := json.Marshal(expected[i])
		if e != nil {
			t.Fatal(e)
		}
		if string(a) != string(b) {
			t.Fatalf("actual managed %s differs from independent native oracle:\n%s\n%s", row.ID, a, b)
		}
	}
}

// Observe defaults through persisted creation and an actual 101-object cleanup
// selection. Comparing a native default constant alone would miss a Host bypass.
func actualArtifactDefaults(t *testing.T) artifact.Policy {
	t.Helper()
	ctx := context.Background()
	s, _, _ := artifactOwnerFixture(t)
	input, e := s.PrepareArtifactInput(ctx, artifact.Input{Name: "default.txt", MediaType: "text/plain"})
	if e != nil {
		t.Fatal(e)
	}
	owner := artifactOwner(t, s)
	var first string
	for i := 0; i < 101; i++ {
		input.Name = fmt.Sprintf("default-%03d.txt", i)
		ref, e := s.Artifacts().Put(ctx, owner, input, strings.NewReader("same original bytes"))
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			first = ref
		}
	}
	policy := s.Artifacts().Policy
	if e := s.db.QueryRow(`SELECT (expires_at-created_at)/86400000000 FROM artifact WHERE artifact_ref=?`, first).Scan(&policy.RetentionDays); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`DROP TRIGGER artifact_immutable; UPDATE artifact SET created_at=1,expires_at=2`); e != nil {
		t.Fatal(e)
	}
	report, e := s.CleanupArtifacts(ctx, true)
	if e != nil || !report.DryRun {
		t.Fatal(report, e)
	}
	policy.CleanupBatch = len(report.Artifacts)
	if scalar(t, s, `SELECT count(*) FROM artifact WHERE deleted_at IS NULL`) != 101 || s.Artifacts().CleanupBatch != nil || s.Artifacts().RetentionDays != nil {
		t.Fatal("default selection mutated original objects or filled an operator choice")
	}
	return policy
}
