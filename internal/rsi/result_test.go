package rsi

import (
	"testing"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestResultTransferRejectsForgeryAndIncompleteBytes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		change func(*rsiplugin.Result)
	}{
		{"digest", []byte(`{"text":"value"}`), func(r *rsiplugin.Result) { r.OutputData.Digest = rsiplugin.FileDigest(nil) }},
		{"size", []byte(`{"text":"value"}`), func(r *rsiplugin.Result) { r.OutputData.Size++ }},
		{"identity", []byte(`{"text":"value"}`), func(r *rsiplugin.Result) { r.OutputData.ID = "input" }},
		{"ambiguous inline", []byte(`{"text":"value"}`), func(r *rsiplugin.Result) { r.Output = []byte(`{}`) }},
		{"duplicate keys", []byte(`{"text":1,"text":2}`), func(*rsiplugin.Result) {}},
		{"truncated JSON", []byte(`{"text":`), func(*rsiplugin.Result) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := resultTransfer{}
			if err := s.append(rsiplugin.ResultChunk{Data: tc.data}); err != nil {
				t.Fatal(err)
			}
			r := rsiplugin.Result{OutputData: &rsiplugin.Input{ID: "result", Size: int64(len(tc.data)), Digest: rsiplugin.FileDigest(tc.data)}}
			tc.change(&r)
			if s.finish(&r) == nil {
				t.Fatal("invalid streamed result accepted")
			}
		})
	}
	s := resultTransfer{}
	if s.append(rsiplugin.ResultChunk{Offset: 1, Data: []byte(`{}`)}) == nil || s.append(rsiplugin.ResultChunk{}) == nil || s.append(rsiplugin.ResultChunk{Data: make([]byte, rsiplugin.InputChunkBytes+1)}) == nil {
		t.Fatal("invalid chunk accepted")
	}
	if err := s.append(rsiplugin.ResultChunk{Data: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if s.append(rsiplugin.ResultChunk{Data: []byte(`{}`)}) == nil || s.finish(nil) == nil || s.finish(&rsiplugin.Result{Output: []byte(`{}`)}) == nil {
		t.Fatal("overlap or orphan transfer accepted")
	}
}
