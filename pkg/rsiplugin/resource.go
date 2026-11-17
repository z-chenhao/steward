package rsiplugin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Resource transfers are separate from pure service inputs/results. The first
// consumer is Web Access's existing 24 MiB evidence and bounded model requests.
// A descriptor is invocation-local bytes, never a Host path or stored object ID.
const MaxResourceBytes = 32 << 20

type ResourceCall struct {
	Contract string          `json:"contract"`
	Key      string          `json:"key"`
	Input    json.RawMessage `json:"input,omitempty"`
	Data     *Input          `json:"data,omitempty"`
}
type ResourceReply struct {
	Data Input `json:"data"`
}
type resourceClientKey struct{}
type resourceClient struct {
	mu       *sync.Mutex
	exchange func(context.Context, string, any) (json.RawMessage, error)
}

// CallResource requests only resources supplied by the current domain owner.
// It grants no network, model, filesystem, credential or general tool access.
// The owner validates the exact accepted intent and owns all effect receipts.
// Reusing a key requires identical bytes; it never starts the effect again.
func CallResource(ctx context.Context, contract, key string, input, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	client, ok := ctx.Value(resourceClientKey{}).(resourceClient)
	if !ok || contract == "" || key == "" || len(key) > 128 {
		return errors.New("no resource context or invalid resource")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := marshalFrame(input)
	if err != nil || len(raw) > MaxResourceBytes {
		return errors.New("resource input byte limit")
	}
	call := ResourceCall{Contract: contract, Key: key, Input: raw}
	if len(raw) > 64<<10 {
		for offset := 0; offset < len(raw); offset += InputChunkBytes {
			end := min(len(raw), offset+InputChunkBytes)
			if _, err = client.exchange(ctx, "write_resource", ResultChunk{Offset: int64(offset), Data: raw[offset:end]}); err != nil {
				return err
			}
		}
		call.Input = nil
		call.Data = &Input{ID: "resource-input", Size: int64(len(raw)), Digest: FileDigest(raw)}
	}
	encoded, err := client.exchange(ctx, "resource", call)
	if err != nil {
		return err
	}
	var reply ResourceReply
	if err = Decode(encoded, &reply); err != nil {
		return err
	}
	if reply.Data.ID == "" || reply.Data.Size < 0 || reply.Data.Size > MaxResourceBytes {
		return errors.New("invalid resource descriptor")
	}
	data := make([]byte, 0, int(reply.Data.Size))
	for int64(len(data)) < reply.Data.Size {
		encoded, err = client.exchange(ctx, "read_resource", InputRead{ID: reply.Data.ID, Offset: int64(len(data))})
		if err != nil {
			return err
		}
		var chunk InputChunk
		if err = Decode(encoded, &chunk); err != nil || len(chunk.Data) == 0 || len(chunk.Data) > InputChunkBytes || int64(len(chunk.Data)) > reply.Data.Size-int64(len(data)) {
			return errors.New("invalid resource chunk")
		}
		data = append(data, chunk.Data...)
	}
	if FileDigest(data) != reply.Data.Digest {
		return errors.New("resource content changed")
	}
	return DecodeResource(data, output)
}

// DecodeResource retains strict duplicate/unknown-field and nesting checks.
func DecodeResource(raw []byte, output any) error {
	return decodeBounded(raw, output, MaxResourceBytes)
}
