package rsiplugin

import (
	"context"
	"errors"
)

const MaxResultBytes = 2 << 20

type ResultChunk struct {
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
}
type resultClientKey struct{}
type resultClient func(context.Context, ResultChunk) error

// JSONResult preserves a bounded feature result without requiring it to fit a
// single transport frame. Bytes stay invocation-local until Host validates the
// completed digest, service schema and domain fence and commits the result.
func JSONResult(ctx context.Context, value any) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	raw, err := marshalFrame(value)
	if err != nil {
		return Result{}, err
	}
	if len(raw) > MaxResultBytes {
		return Result{}, errors.New("result byte limit")
	}
	if len(raw) <= 64<<10 {
		return Result{Output: raw}, nil
	}
	client, ok := ctx.Value(resultClientKey{}).(resultClient)
	if !ok {
		return Result{}, errors.New("no result transfer context")
	}
	for offset := 0; offset < len(raw); offset += InputChunkBytes {
		end := min(len(raw), offset+InputChunkBytes)
		if err = client(ctx, ResultChunk{Offset: int64(offset), Data: raw[offset:end]}); err != nil {
			return Result{}, err
		}
	}
	return Result{OutputData: &Input{ID: "result", Size: int64(len(raw)), Digest: FileDigest(raw)}}, nil
}

// DecodeResult applies strict JSON validation to the completed byte result;
// transport frames and plugin-supplied tool arguments retain their own limits.
func DecodeResult(raw []byte, target any) error { return decodeBounded(raw, target, MaxResultBytes) }
