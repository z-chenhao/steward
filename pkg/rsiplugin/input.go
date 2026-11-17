package rsiplugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

const MaxInputBytes = 8 << 20
const InputChunkBytes = 256 << 10

// Input describes immutable, explicitly supplied bytes for one invocation.
// It never identifies a filesystem path or an object in the Host's stores.
type Input struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type InputRead struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
}
type InputChunk struct {
	Data []byte `json:"data"`
}
type inputClientKey struct{}
type inputClient func(context.Context, InputRead) (InputChunk, error)

// ReadInput receives only the input attached to this invocation. Chunks stay
// below the normal frame limit; the complete content must match its identity.
func ReadInput(ctx context.Context, in Input) ([]byte, error) {
	if in.ID == "" || in.Size < 0 || in.Size > MaxInputBytes {
		return nil, errors.New("invalid input")
	}
	var data bytes.Buffer
	if err := ReadInputTo(ctx, in, &data); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

// ReadInputTo streams one Host-supplied immutable input through bounded frames.
// The owner must explicitly authorize a larger source; this helper cannot widen
// a descriptor or the ordinary ReadInput limit. Bytes may reach the writer
// before final digest validation: callers must discard partial results on error.
func ReadInputTo(ctx context.Context, in Input, output io.Writer) error {
	if in.ID == "" || in.Size < 0 || output == nil {
		return errors.New("invalid input")
	}
	client, ok := ctx.Value(inputClientKey{}).(inputClient)
	if !ok {
		return errors.New("no input context")
	}
	hash := sha256.New()
	for offset := int64(0); offset < in.Size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk, err := client(ctx, InputRead{ID: in.ID, Offset: offset})
		if err != nil {
			return err
		}
		if len(chunk.Data) == 0 || len(chunk.Data) > InputChunkBytes || int64(len(chunk.Data)) > in.Size-offset {
			return errors.New("invalid input chunk")
		}
		if _, err := hash.Write(chunk.Data); err != nil {
			return err
		}
		n, err := output.Write(chunk.Data)
		if err != nil {
			return err
		}
		if n != len(chunk.Data) {
			return io.ErrShortWrite
		}
		offset += int64(n)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != in.Digest {
		return errors.New("input content changed")
	}
	return nil
}
