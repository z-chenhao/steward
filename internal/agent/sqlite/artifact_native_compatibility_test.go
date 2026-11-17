package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/tool"
)

type artifactNativeView struct {
	Alias            string `json:"alias"`
	Name             string `json:"name"`
	Media            string `json:"media_type"`
	Bytes            int64  `json:"bytes"`
	Hash             string `json:"hash"`
	RetentionSeconds int64  `json:"retention_seconds"`
	Version          int64  `json:"version"`
}

// The immutable oracle was captured through the real native Tool/SQLite path
// before this producer existed. This regression runs the new isolated producer
// through ordinary accepted provider exchanges; it does not regenerate answers
// from the candidate, or substitute a current fixture for archived Run identity.
func TestArtifactWorkbenchActualReadAndSearchMatchIndependentNativeOracle(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../plugin/artifactworkbench/testdata/native-read-search.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "86ff3db1510b5f8f6bca7384e544235eaf0930ba97619d28e5bb71a2a4dc0b53" {
		t.Fatal("independent native oracle changed")
	}
	var cases []struct {
		ID, Kind    string
		Input       json.RawMessage
		Error, Text string
		Refs        []string
		Matches     []artifactNativeView
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) != 39 {
		t.Fatal("native oracle incomplete", err)
	}
	seed := []llm.ToolCall{{ID: "seed", Name: artifacttools.Manage, Arguments: `{"operation":"save","name":"seed.txt","mediaType":"text/plain","text":"seed"}`}}
	store, runs, claim, executor, exchange := admitPolicyRun(t, artifacttools.Definitions(), seed, false, func(store *Store, runs *Runs) { installArtifactWorkbench(t, store, runs) })
	defer runs.Release(ctx, claim)
	// This direct Executor fixture deliberately pauses the Actor. Keep its real
	// claim alive with the ordinary Renew API while 25 isolated calls run; do
	// not widen the lease or edit an expired claim back into authority.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	joined := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				joined <- nil
				return
			case <-ticker.C:
				if err := runs.Renew(heartbeatCtx, claim); err != nil {
					if heartbeatCtx.Err() != nil {
						err = nil
					}
					joined <- err
					return
				}
			}
		}
	}()
	defer func() {
		stopHeartbeat()
		if err := <-joined; err != nil {
			t.Error("ordinary claim renewal failed", err)
		}
	}()
	executor.Concurrency = 1
	results, err := executor.Execute(ctx, exchange, seed)
	if err != nil || len(results) != 1 || results[0].Error != nil || len(results[0].ArtifactRefs) != 1 {
		t.Fatal(results, err)
	}
	refs := map[string]string{"seed": results[0].ArtifactRefs[0]}
	aliases := map[string]string{results[0].ArtifactRefs[0]: "seed"}
	scope := toolRunScope(t, store, claim.RunID())
	add := func(alias, name, media, body string, visible bool) {
		t.Helper()
		owner := scope
		if !visible {
			owner = artifactOwner(t, store)
		}
		input := artifact.Input{Name: name, MediaType: media}
		if owner.Owner {
			var err error
			input, err = store.PrepareArtifactInput(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
		}
		ref, err := store.Artifacts().Put(ctx, owner, input, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		refs[alias] = ref
		aliases[ref] = alias
	}
	add("unicode-text", "Évidence报告.md", "text/markdown", "hello 你好🚀\nlast", true)
	add("json-text", "state.json", "application/vnd.fixture+json; charset=utf-8", `{"ok":true}`, true)
	add("binary", "state.bin", "application/octet-stream", string([]byte{0, 255, 1}), true)
	add("invalid-utf8-text", "bad.txt", "text/plain", string([]byte{0xff, 0xfe}), true)
	add("empty-text", "empty.txt", "text/plain", "", true)
	add("percent-name", "100%_done.txt", "text/plain", "literal punctuation", true)
	add("greek-name", "ΣΑΜΟΣ.txt", "text/plain", "greek bytes", true)
	for i := 0; i < 55; i++ {
		alias := fmt.Sprintf("newer-%02d", i)
		add(alias, alias+".txt", "text/plain", "newer", true)
	}
	add("invisible-matching", "Évidence报告.md", "text/markdown", "inaccessible owner marker", false)
	var batch []llm.ToolCall
	var indices []int
	for index, c := range cases {
		var args []byte
		if c.Kind == "read" {
			var request struct {
				Alias  string
				Offset int64
				Limit  int
			}
			if err := json.Unmarshal(c.Input, &request); err != nil {
				t.Fatal(err)
			}
			args, err = json.Marshal(struct {
				Operation string `json:"operation"`
				Ref       string `json:"artifactRef"`
				Offset    int64  `json:"offset,omitempty"`
				Limit     int    `json:"limit,omitempty"`
			}{"read", tool.ObjectID(refs[request.Alias]), request.Offset, request.Limit})
		} else if c.Kind == "search" {
			args = c.Input
		} else {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		name := artifacttools.Search
		if c.Kind == "read" {
			name = artifacttools.Manage
		}
		batch = append(batch, llm.ToolCall{ID: c.ID, Name: name, Arguments: string(args)})
		indices = append(indices, index)
	}
	if len(batch) != 25 {
		t.Fatal("independent read/search cases missing", len(batch))
	}
	exchange = artifactCompatibilityExchange(t, store, runs, claim, batch)
	results, err = executor.Execute(ctx, exchange, batch)
	if err != nil || len(results) != len(batch) {
		t.Fatal(results, err)
	}
	for index, result := range results {
		c := cases[indices[index]]
		t.Run(c.ID, func(t *testing.T) {
			code := ""
			if result.Error != nil {
				code = result.Error.Code
			}
			if code != c.Error {
				t.Fatal("native error changed", code, c.Error)
			}
			if c.Kind == "read" {
				if result.Text != c.Text {
					t.Fatal("native bytes/range projection changed", result.Text, c.Text)
				}
				var actual []string
				for _, ref := range result.ArtifactRefs {
					actual = append(actual, aliases[ref])
				}
				if !reflect.DeepEqual(actual, c.Refs) {
					t.Fatal("native reference changed", actual, c.Refs)
				}
				return
			}
			var descriptors []artifact.Descriptor
			if err := json.Unmarshal([]byte(result.Text), &descriptors); err != nil {
				t.Fatal(err)
			}
			var actual []artifactNativeView
			for _, d := range descriptors {
				view := artifactNativeView{Alias: aliases[tool.ObjectRef("artifact", d.Ref)], Name: d.Name, Media: d.MediaType, Bytes: d.ByteSize, Hash: d.ContentHash, Version: d.Version}
				if d.ExpiresAt != nil {
					view.RetentionSeconds = int64(d.ExpiresAt.Sub(d.CreatedAt).Seconds())
				}
				actual = append(actual, view)
			}
			if len(actual) != len(c.Matches) {
				t.Fatal("native visible-result count changed", len(actual), len(c.Matches))
			}
			for i := range actual {
				if actual[i] != c.Matches[i] {
					t.Fatalf("native descriptor/order changed: %#v != %#v", actual[i], c.Matches[i])
				}
			}
		})
	}
	putArtifact(t, store, scope, "corpus changed after accepted receipt")
	replay, err := executor.Execute(ctx, exchange, batch)
	if err != nil || !reflect.DeepEqual(replay, results) {
		t.Fatal("accepted result reread mutable catalogue or bytes", replay, err)
	}
	if scalar(t, store, `SELECT count(*) FROM rsi_call WHERE instance_id='artifact-workbench'`) != 0 {
		t.Fatal("pure policies wrote opaque production calls")
	}
}

func artifactCompatibilityExchange(t *testing.T, store *Store, runs *Runs, claim agent.RunClaim, batch []llm.ToolCall) string {
	t.Helper()
	ctx := context.Background()
	run, err := runs.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	run.Checkpoint.Step++
	if err := runs.Checkpoint(ctx, claim, run.Version, run.Checkpoint); err != nil {
		t.Fatal(err)
	}
	run, err = runs.Load(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := runs.Generation(ctx, claim, run)
	if err != nil {
		t.Fatal(err)
	}
	provider := &loopProvider{store: store, respond: func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{ToolCalls: batch}, nil
	}}
	if _, err := provider.Generate(ctx, generation.Request, nil); err != nil {
		t.Fatal(err)
	}
	return generation.Request.GenerationID
}
