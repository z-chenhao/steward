package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/interaction"
)

func TestArtifactWorkbenchActualMediaKindsMatchIndependentNativeOracle(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../plugin/artifactworkbench/testdata/native-read-search.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "86ff3db1510b5f8f6bca7384e544235eaf0930ba97619d28e5bb71a2a4dc0b53" {
		t.Fatal("independent native oracle changed")
	}
	var cases []struct{ ID, Kind, Input, Text string }
	// Other cases have object-valued inputs; keep the original sealed bytes and
	// decode only the content-kind records rather than altering the oracle.
	var records []json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil || len(records) != 39 {
		t.Fatal("incomplete independent oracle", err)
	}
	for _, record := range records {
		var kind struct{ Kind string }
		if err := json.Unmarshal(record, &kind); err != nil {
			t.Fatal(err)
		}
		if kind.Kind == "content_kind" {
			var row struct{ ID, Kind, Input, Text string }
			if err := json.Unmarshal(record, &row); err != nil {
				t.Fatal(err)
			}
			cases = append(cases, row)
		}
	}
	if len(cases) != 14 {
		t.Fatal("missing original media cases")
	}
	s, _, _ := artifactOwnerFixture(t)
	stored, rejected := 0, 0
	for _, row := range cases {
		t.Run(row.ID, func(t *testing.T) {
			input, err := s.PrepareArtifactInput(ctx, artifact.Input{Name: row.ID, MediaType: row.Input})
			if err != nil || input.Creation == nil || input.Creation.Kind(row.Input) != row.Text {
				t.Fatal("actual accepted recipe changed original classification", input, err)
			}
			before := scalar(t, s, `SELECT count(*) FROM artifact`)
			ref, err := s.Artifacts().Put(ctx, artifactOwner(t, s), input, strings.NewReader("original bytes"))
			// Actual archived29 accepts the opaque token in media-12; only
			// the empty media-13 is rejected. Expected behavior must not be
			// derived from the current validator under test.
			if row.ID == "media-13" {
				if !errors.Is(err, artifact.ErrInvalid) || ref != "" || scalar(t, s, `SELECT count(*) FROM artifact`) != before {
					t.Fatal("invalid MIME crossed the original upload boundary", err)
				}
				rejected++
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.Artifacts().Describe(ctx, artifactOwner(t, s), ref)
			if err != nil || d.SemanticKind != row.Text || d.MethodGraph != input.Creation.GraphRevision || d.ContentType() != row.Text || readArtifact(t, s, artifactOwner(t, s), ref) != "original bytes" {
				t.Fatal("stored media lost original type/bytes", d, err)
			}
			stored++
		})
	}
	if stored != 13 || rejected != 1 {
		t.Fatal("original creation boundary changed", stored, rejected)
	}
	t.Logf("original media cases: %d persisted, %d empty MIME rejected", stored, rejected)
}

func TestArtifactWorkbenchActualChannelImportRetainsRecipeAndOriginalDedup(t *testing.T) {
	ctx := context.Background()
	s, _, host := artifactOwnerFixture(t)
	for _, channel := range []string{"web", "feishu"} {
		t.Run(channel, func(t *testing.T) {
			if _, err := s.BindChannel(ctx, channel, "owner", "destination", true, 0); err != nil {
				t.Fatal(err)
			}
			event := interaction.InboundEvent{Channel: channel, Subject: "owner", Destination: "destination", SourceID: "original"}
			ref, err := s.ImportChannelArtifact(ctx, event, "resource", artifact.Input{Name: "picture.png", MediaType: "image/png"}, strings.NewReader("original transport bytes"))
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.Artifacts().Describe(ctx, artifactOwner(t, s), ref)
			if err != nil || d.MethodGraph == "" || d.SemanticKind != "image" || readArtifact(t, s, artifactOwner(t, s), ref) != "original transport bytes" {
				t.Fatal("actual channel bypassed the managed recipe", d, err)
			}
			// A retained idempotent reference does not need to rerun a candidate.
			if err := host.Store.Stop(ctx, true); err != nil {
				t.Fatal(err)
			}
			again, err := s.ImportChannelArtifact(ctx, event, "resource", artifact.Input{Name: "ignored", MediaType: "text/plain"}, strings.NewReader("not a new accepted file"))
			if err != nil || again != ref {
				t.Fatal("retained channel identity depended on a candidate", again, err)
			}
			event.SourceID = "new"
			if _, err := s.ImportChannelArtifact(ctx, event, "resource", artifact.Input{Name: "blocked", MediaType: "text/plain"}, strings.NewReader("blocked")); !errors.Is(err, artifact.ErrUnavailable) {
				t.Fatal("new channel import fell back after stop", err)
			}
			if err := host.Store.Stop(ctx, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
