package rsiplugin

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestChunkedResultPreservesEscapedTextAndBudgets(t *testing.T) {
	var raw []byte
	ctx := context.WithValue(context.Background(), resultClientKey{}, resultClient(func(_ context.Context, chunk ResultChunk) error {
		if chunk.Offset != int64(len(raw)) || len(chunk.Data) > InputChunkBytes {
			t.Fatal("unbounded result chunk")
		}
		raw = append(raw, chunk.Data...)
		return nil
	}))
	value := strings.Repeat("\x00", 200000)
	result, err := JSONResult(ctx, value)
	if err != nil || result.OutputData == nil || result.OutputData.Digest != FileDigest(raw) || result.OutputData.Size != int64(len(raw)) {
		t.Fatal(result, err)
	}
	var decoded string
	if DecodeResult(raw, &decoded) != nil || decoded != value || Decode(raw, &decoded) == nil {
		t.Fatal("result widened the transport frame or lost text")
	}
	if _, err = JSONResult(ctx, strings.Repeat("x", MaxResultBytes)); err == nil {
		t.Fatal("result cap bypassed")
	}
	inline, err := JSONResult(context.Background(), "small")
	if err != nil || !bytes.Equal(inline.Output, []byte(`"small"`)) || inline.OutputData != nil {
		t.Fatal(inline, err)
	}
}
