package rsiplugin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestInputChunksAreBoundedAndContentAddressed(t *testing.T) {
	data := bytes.Repeat([]byte("合同🧭"), 90000)
	input := Input{ID: "document", Size: int64(len(data)), Digest: FileDigest(data)}
	reads := 0
	ctx := context.WithValue(context.Background(), inputClientKey{}, inputClient(func(_ context.Context, in InputRead) (InputChunk, error) {
		reads++
		if in.ID != "document" || in.Offset < 0 || in.Offset >= int64(len(data)) {
			return InputChunk{}, errors.New("outside input")
		}
		return InputChunk{Data: data[in.Offset:min(int64(len(data)), in.Offset+InputChunkBytes)]}, nil
	}))
	got, err := ReadInput(ctx, input)
	if err != nil || !bytes.Equal(got, data) || reads < 2 {
		t.Fatal("chunked input lost bytes", len(got), reads, err)
	}
	input.Digest = FileDigest([]byte("other"))
	if _, err = ReadInput(ctx, input); err == nil {
		t.Fatal("different content identity accepted")
	}
	input.ID = "private-file"
	if _, err = ReadInput(ctx, input); err == nil {
		t.Fatal("undeclared input accepted")
	}
}

func TestInputRejectsTruncationAndOversizedChunks(t *testing.T) {
	for _, data := range [][]byte{nil, make([]byte, InputChunkBytes+1)} {
		ctx := context.WithValue(context.Background(), inputClientKey{}, inputClient(func(context.Context, InputRead) (InputChunk, error) { return InputChunk{Data: data}, nil }))
		if _, err := ReadInput(ctx, Input{ID: "document", Size: InputChunkBytes + 1, Digest: FileDigest(data)}); err == nil {
			t.Fatal("invalid chunk accepted")
		}
	}
	if _, err := ReadInput(context.Background(), Input{ID: "document", Size: MaxInputBytes + 1}); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestInputStreamPreservesLargeIdentityAndDoesNotWidenOrdinaryRead(t *testing.T) {
	data := bytes.Repeat([]byte("合同🧭"), 900000)
	if len(data) <= MaxInputBytes {
		t.Fatal("fixture must exceed ordinary input limit")
	}
	input := Input{ID: "private-order", Size: int64(len(data)), Digest: FileDigest(data)}
	reads := 0
	ctx := context.WithValue(context.Background(), inputClientKey{}, inputClient(func(_ context.Context, in InputRead) (InputChunk, error) {
		reads++
		if in.ID != "private-order" || in.Offset < 0 || in.Offset >= int64(len(data)) {
			return InputChunk{}, errors.New("out of scope")
		}
		return InputChunk{Data: data[in.Offset:min(int64(len(data)), in.Offset+InputChunkBytes)]}, nil
	}))
	if _, err := ReadInput(ctx, input); err == nil || reads != 0 {
		t.Fatal("ordinary input cap widened", reads, err)
	}
	var output bytes.Buffer
	if err := ReadInputTo(ctx, input, &output); err != nil || !bytes.Equal(output.Bytes(), data) || reads < 32 {
		t.Fatal("large source changed", output.Len(), reads, err)
	}
	input.Digest = FileDigest(nil)
	if err := ReadInputTo(ctx, input, io.Discard); err == nil {
		t.Fatal("changed identity accepted")
	}
	input.Digest = FileDigest(data)
	if err := ReadInputTo(ctx, input, shortInputWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short writer lost bytes", err)
	}
	input.ID = "undeclared"
	if err := ReadInputTo(ctx, input, io.Discard); err == nil {
		t.Fatal("undeclared source accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ReadInputTo(canceled, input, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

type shortInputWriter struct{}

func (shortInputWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
