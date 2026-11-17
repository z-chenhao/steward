package rsi

import (
	"bytes"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type resultTransfer struct {
	data   []byte
	chunks int
}

func (s *resultTransfer) append(chunk rsiplugin.ResultChunk) error {
	if s.chunks >= 8 || chunk.Offset != int64(len(s.data)) || len(chunk.Data) == 0 || len(chunk.Data) > rsiplugin.InputChunkBytes || len(s.data)+len(chunk.Data) > rsiplugin.MaxResultBytes {
		return ErrInvalid
	}
	s.chunks++
	s.data = append(s.data, chunk.Data...)
	return nil
}
func (s *resultTransfer) finish(result *rsiplugin.Result) error {
	if result == nil {
		if s.chunks != 0 {
			return ErrInvalid
		}
		return nil
	}
	if result.OutputData == nil {
		if s.chunks != 0 {
			return ErrInvalid
		}
		return nil
	}
	d := result.OutputData
	if d.ID != "result" || d.Size != int64(len(s.data)) || d.Digest != rsiplugin.FileDigest(s.data) || len(result.Output) > 0 && string(result.Output) != "null" || rsiplugin.DecodeResult(s.data, new(any)) != nil {
		return ErrInvalid
	}
	result.Output = bytes.Clone(s.data)
	result.OutputData = nil
	return nil
}
