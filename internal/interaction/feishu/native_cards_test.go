package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat"
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	"os"
	"testing"
)

func TestNativeCardsMatchCapturedV1(t *testing.T) {
	type input struct {
		Name      string `json:"name"`
		Text      string `json:"text"`
		Sequence  int64  `json:"sequence"`
		Intent    string `json:"intent"`
		Workspace string `json:"workspace"`
		Feedback  string `json:"feedback,omitempty"`
		ReviewURL string `json:"review_url,omitempty"`
		Artifact  string `json:"artifact,omitempty"`
	}
	type sample struct {
		Input            input          `json:"input"`
		Card             map[string]any `json:"card"`
		IncludeArtifacts bool           `json:"include_artifacts"`
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/feishu-native-v1/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []sample
	if err = json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 8 {
		t.Fatal("incomplete native capture", len(samples))
	}
	for _, sample := range samples {
		c := sample.Input

		e := delivery.Envelope{Request: delivery.Request{Intent: c.Intent}, Message: conversation.Message{Sequence: c.Sequence, Content: conversation.Text(c.Text)}}
		if c.Artifact != "" {
			e.Message.Content = append(e.Message.Content, conversation.ContentPart{Type: "file", ArtifactRef: c.Artifact})
		}
		client := fixtureClient()
		client.Config.WorkspaceURL = c.Workspace
		adapter := Adapter{Client: client, ProjectCard: func(_ context.Context, f contract.CardFacts) (contract.CardProjection, error) {
			return feishuchat.ProjectCard(f), nil
		}}
		p, err := adapter.prepareCard(context.Background(), e, "om_origin", c.Feedback, c.ReviewURL)
		if err != nil {
			t.Fatal(c.Name, err)
		}
		got, err := json.Marshal(p.Transport.Card)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(sample.Card)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) || p.Transport.IncludeArtifacts != sample.IncludeArtifacts {
			t.Errorf("native card changed: %s", c.Name)
		}
	}
}
