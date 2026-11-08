package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/bridge"
	"github.com/z-chenhao/assistant/internal/memory"
)

func TestMemoryRetryNativeBridgePeer(t *testing.T) {
	for _, arg := range os.Args[1:] {
		if arg == "-test.run=^TestMemoryRetryNativeBridgePeer$" {
			if err := (&bridge.Server{}).Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}

func TestSemanticRuntimeNativeBridgeRetryUsesFreshSessionAndReplaysAcceptedResult(t *testing.T) {
	for _, alwaysInvalid := range []bool{false, true} {
		t.Run(fmt.Sprint(alwaysInvalid), func(t *testing.T) {
			s, r, _, a, _ := semanticRuntime(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				n := calls.Add(1)
				var body struct {
					Messages []struct {
						Role    string
						Content string
					}
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				text := `{"facts":[]}`
				if n == 1 || alwaysInvalid {
					text = `not a valid memory proposal`
				}
				delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", delta)
			}))
			defer server.Close()
			input := modelInput()
			input.Endpoint = server.URL + "/v1"
			activateLoop(t, s, input)
			command, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var sessionIDs []string
			client, err := bridge.OpenLocal(context.Background(), command, "-test.run=^TestMemoryRetryNativeBridgePeer$", func(context.Context, llm.Snapshot) (llm.Connection, error) {
				return llm.Connection{Endpoint: server.URL + "/v1"}, nil
			}, func(ctx context.Context, req llm.GenerationRequest, _ llm.Connection, result llm.GenerationResult, latency time.Duration, cause error) error {
				sessionIDs = append(sessionIDs, req.SessionID)
				if req.SessionID != req.RunID {
					t.Error("transport session differs from run")
				}
				return s.Models().RecordUsageAnd(ctx, req, result, latency, cause, r.AcceptMemoryGeneration)
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			r.ConfigureSemanticMemory(client)
			mustSubmit(t, a, "bad-first", "我喜欢无糖绿茶")
			// Ordinary later evidence should never remain behind a poisoned head.
			mustSubmit(t, a, "later", "给我调研 grokbot 赚钱路线")
			if err := r.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := r.CurateMemory(context.Background()); !errors.Is(err, memory.ErrProposal) {
				t.Fatal("first proposal should fail validation", err)
			}
			maxAttempts := 2
			if alwaysInvalid {
				maxAttempts = 3
			}
			for i := 1; i < maxAttempts; i++ {
				err := r.CurateMemory(context.Background())
				if alwaysInvalid && i < 2 {
					if !errors.Is(err, memory.ErrProposal) {
						t.Fatal("bounded invalid retry", err)
					}
				} else if err != nil {
					t.Fatal("retry reused a disposed bridge session", err)
				}
			}
			if int(calls.Load()) != maxAttempts {
				t.Fatal("retry never reached actual HTTP peer", calls.Load())
			}
			if scalar(t, s, `SELECT count(*) FROM provider_usage WHERE error_code='GENERATION_CANCELLED'`) != 0 {
				t.Fatal("fresh retry cancelled before dispatch")
			}
			wantReceipts := 2
			if alwaysInvalid {
				wantReceipts = 1
			}
			if scalar(t, s, `SELECT count(*) FROM memory_semantic_receipt`) != wantReceipts {
				t.Fatal("later evidence blocked")
			}
			if alwaysInvalid && scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE failed_at IS NOT NULL`) != 1 {
				t.Fatal("invalid proposal not quarantined")
			}
			if !alwaysInvalid {
				if _, err := s.db.Exec(`DELETE FROM memory_semantic_receipt`); err != nil {
					t.Fatal(err)
				}
				if err := r.CurateMemory(context.Background()); err != nil {
					t.Fatal(err)
				}
				if int(calls.Load()) != maxAttempts {
					t.Fatal("accepted result was generated again")
				}
			}
			rows, err := s.db.Query(`SELECT request_sealed FROM memory_generation`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			sessions := map[string]bool{}
			for _, id := range sessionIDs {
				if sessions[id] {
					t.Fatal("transport session reused", id)
				}
				sessions[id] = true
			}
			for rows.Next() {
				var sealed string
				rows.Scan(&sealed)
				if strings.Contains(sealed, "我喜欢") {
					t.Fatal("private input was unsealed")
				}
			}
		})
	}
}
