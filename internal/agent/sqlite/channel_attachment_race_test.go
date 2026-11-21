package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/interaction"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentChannelArtifactCommitsOneQuotaEntry(t *testing.T) {
	s := openModels(t)
	ctx := context.Background()
	if _, err := s.BindChannel(ctx, "web", "owner", "destination", true, 0); err != nil {
		t.Fatal(err)
	}
	event := interaction.InboundEvent{Channel: "web", Subject: "owner", Destination: "destination", SourceID: "same"}
	input := artifact.Input{Name: "note.txt", MediaType: "text/plain"}
	next := make(chan struct{})
	arrived := make(chan struct{})
	var visits atomic.Int32
	s.MainBoundary = func(name string) {
		if name == "artifact_temp_created" && visits.Add(1) == 1 {
			close(arrived)
			<-next
		}
	}
	errchan := make(chan error, 1)
	go func() {
		_, err := s.ImportChannelArtifact(ctx, event, "key", input, strings.NewReader("note"))
		errchan <- err
	}()
	select {
	case <-arrived:
	case err := <-errchan:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	_, err := s.ImportChannelArtifact(ctx, event, "key", input, strings.NewReader("note"))
	close(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-errchan; err != nil {
		t.Fatal(err)
	}
	resources := scalar(t, s, "SELECT count(*) FROM channel_resource")
	blobs := scalar(t, s, "SELECT count(*) FROM artifact WHERE deleted_at IS NULL")
	if resources != 1 || blobs != 1 {
		t.Fatal(resources, blobs)
	}
}
