package rsi

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// InputData is supplied by a domain adapter after authorization, never from a
// plugin-supplied path. The first real consumer is the bounded DOCX reader.
type InputData struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}
type inputScope struct {
	descriptor         []rsiplugin.Input
	data               []byte
	computed           []byte
	computedDescriptor *rsiplugin.Input
	computedOutput     json.RawMessage
	computedAttempted  bool
}

func newInputScope(inputs []InputData) (inputScope, error) {
	if len(inputs) == 0 {
		return inputScope{}, nil
	}
	if len(inputs) != 1 || !identifier.MatchString(inputs[0].ID) || len(inputs[0].Data) > rsiplugin.MaxInputBytes {
		return inputScope{}, ErrInvalid
	}
	return newImmutableInputScope(inputs[0])
}

// Only explicit owner-side streaming reads use this constructor. Ordinary
// plugin calls, verification inputs and effect drives retain MaxInputBytes.
func newImmutableInputScope(input InputData) (inputScope, error) {
	if !identifier.MatchString(input.ID) {
		return inputScope{}, ErrInvalid
	}
	data := bytes.Clone(input.Data)
	return inputScope{descriptor: []rsiplugin.Input{{ID: input.ID, Size: int64(len(data)), Digest: rsiplugin.FileDigest(data)}}, data: data}, nil
}

func (s *inputScope) read(ctx context.Context, in rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
	if err := ctx.Err(); err != nil {
		return rsiplugin.InputChunk{}, err
	}
	data := s.data
	if s.computedDescriptor != nil && in.ID == s.computedDescriptor.ID {
		data = s.computed
	} else if len(s.descriptor) != 1 || in.ID != s.descriptor[0].ID {
		return rsiplugin.InputChunk{}, ErrNotFound
	}
	if in.Offset < 0 || in.Offset >= int64(len(data)) {
		return rsiplugin.InputChunk{}, ErrNotFound
	}
	end := min(int64(len(data)), in.Offset+rsiplugin.InputChunkBytes)
	return rsiplugin.InputChunk{Data: data[in.Offset:end]}, nil
}
