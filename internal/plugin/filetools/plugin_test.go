package filetools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
)

func TestWindowPreservesNativePythonReadBoundaries(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	driver := environment.Local{Config: environment.Config{Root: root}}
	for name, data := range map[string][]byte{
		"empty":      {},
		"lines":      []byte("first\r\n第二行\nlast"),
		"separators": []byte("a\vb\fc\x1cd\x1de\x1ef\u0085g\u2028h\u2029i\rj\n"),
		"long":       []byte(strings.Repeat("字", 20000) + "\nnext\n"),
		"binary":     {0, 255, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, "input"), data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, q := range []struct {
				offset, limit int
				artifact      bool
			}{{1, 200, false}, {2, 1, false}, {99, 4, false}, {1, 200, true}} {
				args, _ := json.Marshal(map[string]any{"path": "input", "offset": q.offset, "limit": q.limit, "as_artifact": q.artifact})
				native := driver.Execute(context.Background(), "builtin__read", string(args))
				if native.Error != nil {
					t.Fatal(native.Error)
				}
				plan := selectWindow(data, contract.WindowFacts{Offset: q.offset, Limit: q.limit, OutputBytes: 48000, AsArtifact: q.artifact})
				if native.Output != nil {
					if closer, ok := native.Output.Reader.(io.Closer); ok {
						if err := closer.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if !plan.Artifact {
						t.Fatal("binary/artifact policy drift", plan)
					}
					continue
				}
				var read struct {
					Text      string `json:"text"`
					Next      int    `json:"next_offset"`
					Truncated bool   `json:"truncated"`
				}
				if json.Unmarshal([]byte(native.Text), &read) != nil || plan.Artifact || read.Text != string(data[plan.Start:plan.End]) || read.Next != plan.NextOffset || read.Truncated != (plan.End < len(data)) {
					t.Fatal("versioned window changed original read semantics", q, plan, read)
				}
			}
		})
	}
}

func TestWindowStreamsLargeLineCorpusAndClipsUTF8(t *testing.T) {
	data := bytes.Repeat([]byte("\n"), 8<<20)
	plan := selectWindow(data, contract.WindowFacts{Offset: len(data), Limit: 2000, OutputBytes: 48000})
	if plan.Start != len(data)-1 || plan.End != len(data) || plan.NextOffset != len(data)+1 {
		t.Fatal("large corpus lost exact continuation", plan)
	}
	plan = selectWindow([]byte("字字\n"), contract.WindowFacts{Offset: 1, Limit: 200, OutputBytes: 4})
	if plan.Start != 0 || plan.End != 3 || plan.NextOffset != 2 {
		t.Fatal("UTF8 clip or oversized-line continuation changed", plan)
	}
}
