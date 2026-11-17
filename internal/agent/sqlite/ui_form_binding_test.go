package sqlite

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestUIWebFormBindingPreservesDuplicatePositionsAndFrozenRun(t *testing.T) {
	for _, managed := range []bool{false, true} {
		name := "historical-native"
		if managed {
			name = "managed"
		}
		t.Run(name, func(t *testing.T) {
			s, r, a, p := childFixture(t, ui.ShowDefinition())
			var host rsi.Host
			if managed {
				host = installUIRuntimePlugin(t, s, r)
			}
			component := `{"name":"steward.form","props":{"title":"Answer","fields":[{"name":"answer","label":"Answer","type":"text","required":true}]}}`
			first := `{"title":"First","fallback":"First","components":[` + component + `,` + component + `]}`
			second := `{"title":"Second","fallback":"Second","components":[` + component + `]}`
			var step atomic.Int32
			p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
				if step.Add(1) == 1 {
					return llm.GenerationResult{ToolCalls: []llm.ToolCall{{ID: "first", Name: ui.ShowName, Arguments: first}, {ID: "second", Name: ui.ShowName, Arguments: second}}}, nil
				}
				return llm.GenerationResult{Text: "Please answer."}, nil
			}
			mustSubmit(t, a, "duplicate-forms", "Ask three independently identified questions")
			mustDrain(t, a)
			// Exercise the original selection immediately after the real owner
			// action; no fixture select/update repairs this retained binding.
			originalGraph := ""
			if managed {
				status, err := host.Store.Status(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				originalGraph = status.Revision
				if _, err = host.Store.Deactivate(context.Background(), "disable-after-form-delivery", status.Revision); err != nil {
					t.Fatal(err)
				}
			}
			var raw, runID string
			if err := s.db.QueryRow(`SELECT c.message_json,w.run_id FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id LIMIT 1`).Scan(&raw, &runID); err != nil {
				t.Fatal(err)
			}
			var m conversation.Message
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatal(err)
			}
			projection, err := s.WebProjection(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			var spec ui.Spec
			if err = json.Unmarshal(projection, &spec); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			forms := 0
			for _, c := range spec.Components {
				if c.FormID == "" {
					continue
				}
				forms++
				source, err := s.WebFormBinding(context.Background(), m, c.FormID)
				if err != nil {
					t.Fatal(err)
				}
				if source.Managed != managed || source.RunID != runID || source.Component.FormID != c.FormID || source.Component.WidgetRef == "" || seen[source.Component.WidgetRef] {
					t.Fatalf("lost owning position: %+v", source)
				}
				seen[source.Component.WidgetRef] = true
				if managed && (source.GraphRevision == "" || source.GraphRevision != originalGraph) {
					t.Fatal("lost frozen graph after deactivation", source.GraphRevision, originalGraph)
				}
				// Binding does not mutate the original form's identity or answer.
				if source.Component.Submitted || source.Component.Response != "" {
					t.Fatal("binding invented acceptance")
				}
			}
			if forms != 3 || len(seen) != 3 {
				t.Fatal("identical forms conflated", forms, seen)
			}
			if _, err = s.WebFormBinding(context.Background(), m, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
				t.Fatal("unknown form accepted")
			}
			m.Role = "user"
			if _, err = s.WebFormBinding(context.Background(), m, spec.Components[len(spec.Components)-1].FormID); err == nil {
				t.Fatal("non-assistant accepted")
			}
		})
	}
}
