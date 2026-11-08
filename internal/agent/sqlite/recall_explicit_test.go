package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/internal/tool"
)

type explicitRecallProbe struct {
	mu      sync.Mutex
	queries []recall.Query
	hits    []recall.Hit
}

func (p *explicitRecallProbe) Search(_ context.Context, _ recall.Scope, q recall.Query) ([]recall.Hit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, q)
	return p.hits, nil
}
func TestRecallExplicitV2BoundsSourcesAndPreservesLegacyResults(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			def := recall.Definition(recall.UnconfiguredIdentity)
			if version < 4 {
				def.Spec = def.PreviousSpecs[version-1]
				def.PreviousSpecs = nil
			}
			calls := []llm.ToolCall{{ID: "search", Name: recall.ToolName, Arguments: `{"query":"workshop","limit":5}`}}
			_, r, c, executor, exchange := admitPolicy(t, []tool.Definition{def}, calls)
			// The current task's originating input is never new historical evidence.
			source := "current-original"
			run, err := r.Load(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			run.Checkpoint.Messages = append(run.Checkpoint.Messages, agent.AgentMessage{Type: "user", MessageID: source, Content: conversation.Text("workshop current instruction")})
			if err = r.Checkpoint(context.Background(), c, run.Version, run.Checkpoint); err != nil {
				t.Fatal(err)
			}

			text := strings.Repeat("background ", 1500) + "workshop was held on April 18. " + strings.Repeat("tail ", 300)
			p := &explicitRecallProbe{hits: []recall.Hit{{SourceType: "conversation", SourceRef: "conversation:" + source, Content: "workshop current instruction", OccurredAt: time.Now()}, {SourceType: "conversation", SourceRef: "conversation:past", Content: text, OccurredAt: time.Now()}}}
			r.ConfigureRecall(recall.Service{Memory: p, Conversation: p})
			results, err := executor.Execute(context.Background(), exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error != nil {
				t.Fatal(results, err)
			}
			var hits []recall.Hit
			if json.Unmarshal([]byte(results[0].Text), &hits) != nil {
				t.Fatal(results)
			}
			if version >= 2 {
				if len(results[0].Text) > 8192 || len(hits) != 1 || hits[0].SourceRef != "conversation:past" || !strings.Contains(hits[0].Content, "April 18") || hits[0].Offset == 0 {
					t.Fatal("current input leaked or excerpt lost exact read navigation", hits)
				}
			} else if len(hits) != 2 || hits[1].Content != text {
				t.Fatal("v1 future/replayed surface changed", hits)
			}
			_ = r.Release(context.Background(), c)
		})
	}
}
