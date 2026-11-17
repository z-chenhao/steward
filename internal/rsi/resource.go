package rsi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Resource is an invocation-local domain port. The owner supplies closed
// schemas and enforces accepted intent, credentials, spending and replay rules.
// Candidate manifests cannot register ports or select a different resource owner.
type Resource struct {
	Contract     string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Invoke       func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

const resourceTrafficBytes = 128 << 20

type resourceValue struct {
	input  []byte
	output []byte
	data   rsiplugin.Input
}
type resourceScope struct {
	ports      map[string]Resource
	check      func(context.Context) error
	pending    []byte
	values     map[string]resourceValue
	data       map[string][]byte
	traffic    int
	reads      int
	dispatched int
}

func newResourceScope(resources []Resource, check func(context.Context) error) (*resourceScope, error) {
	if check == nil || len(resources) == 0 || len(resources) > 8 {
		return nil, ErrInvalid
	}
	s := &resourceScope{ports: map[string]Resource{}, check: check, values: map[string]resourceValue{}, data: map[string][]byte{}}
	for _, port := range resources {
		if !validContract(port.Contract) || port.Invoke == nil || s.ports[port.Contract].Invoke != nil || !tool.SchemaSupported(string(port.InputSchema)) || !tool.SchemaSupported(string(port.OutputSchema)) {
			return nil, ErrInvalid
		}
		port.InputSchema = bytes.Clone(port.InputSchema)
		port.OutputSchema = bytes.Clone(port.OutputSchema)
		s.ports[port.Contract] = port
	}
	return s, nil
}
func (s *resourceScope) write(ctx context.Context, chunk rsiplugin.ResultChunk) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if chunk.Offset != int64(len(s.pending)) || len(chunk.Data) == 0 || len(chunk.Data) > rsiplugin.InputChunkBytes || len(s.pending)+len(chunk.Data) > rsiplugin.MaxResourceBytes || s.traffic+len(chunk.Data) > resourceTrafficBytes {
		return ErrInvalid
	}
	s.traffic += len(chunk.Data)
	s.pending = append(s.pending, chunk.Data...)
	return nil
}
func (s *resourceScope) invoke(ctx context.Context, call rsiplugin.ResourceCall) (rsiplugin.ResourceReply, error) {
	var reply rsiplugin.ResourceReply
	if err := s.check(ctx); err != nil {
		return reply, err
	}
	port, found := s.ports[call.Contract]
	if !found || call.Key == "" || len(call.Key) > 128 {
		return reply, ErrRevoked
	}
	raw := []byte(call.Input)
	if call.Data != nil {
		d := call.Data
		if len(raw) != 0 || d.ID != "resource-input" || d.Size != int64(len(s.pending)) || d.Digest != rsiplugin.FileDigest(s.pending) {
			return reply, ErrInvalid
		}
		raw = s.pending
		s.pending = nil
	} else {
		if len(s.pending) != 0 || len(raw) > 64<<10 || s.traffic+len(raw) > resourceTrafficBytes {
			return reply, ErrInvalid
		}
		s.traffic += len(raw)
	}
	if !tool.ValidateBounded(string(port.InputSchema), string(raw), rsiplugin.MaxResourceBytes) {
		return reply, ErrInvalid
	}
	key := call.Contract + "\x00" + call.Key
	if previous, ok := s.values[key]; ok {
		if !bytes.Equal(previous.input, raw) {
			return reply, ErrConflict
		}
		return rsiplugin.ResourceReply{Data: previous.data}, nil
	}
	if len(s.values) >= 64 {
		return reply, ErrInvalid
	}
	// Mark before invoking. An ambiguous failure ends the process; it is never
	// retried by this broker. The enclosing accepted Tool owns durable recovery.
	s.dispatched++
	output, err := port.Invoke(ctx, call.Key, bytes.Clone(raw))
	if err != nil {
		return reply, err
	}
	if err = s.check(ctx); err != nil {
		return reply, err
	}
	if len(output) > rsiplugin.MaxResourceBytes || s.traffic+len(output) > resourceTrafficBytes || !tool.ValidateBounded(string(port.OutputSchema), string(output), rsiplugin.MaxResourceBytes) {
		return reply, ErrInvalid
	}
	s.traffic += len(output)
	data := rsiplugin.Input{ID: fmt.Sprintf("resource-%d", len(s.values)+1), Size: int64(len(output)), Digest: rsiplugin.FileDigest(output)}
	value := resourceValue{input: bytes.Clone(raw), output: bytes.Clone(output), data: data}
	s.values[key] = value
	s.data[data.ID] = value.output
	return rsiplugin.ResourceReply{Data: data}, nil
}
func (s *resourceScope) read(ctx context.Context, request rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
	if err := s.check(ctx); err != nil {
		return rsiplugin.InputChunk{}, err
	}
	data, ok := s.data[request.ID]
	if !ok || request.Offset < 0 || request.Offset >= int64(len(data)) || s.reads >= 512 {
		return rsiplugin.InputChunk{}, ErrInvalid
	}
	s.reads++
	end := min(int64(len(data)), request.Offset+rsiplugin.InputChunkBytes)
	return rsiplugin.InputChunk{Data: data[request.Offset:end]}, nil
}

// DriveService runs business code under the already accepted domain operation.
// It creates no RSI pending/effect ledger or production Run of its own. The
// caller must own the original Tool's authorization, effect and recovery record.
// Pure ReadService and ordinary plugin calls never acquire these ports.
func (h Host) DriveService(ctx context.Context, revision, contract string, input json.RawMessage, inputs []InputData, check func(context.Context) error, ports []Resource) (json.RawMessage, error) {
	fence := func(c context.Context) error {
		if check == nil {
			return ErrRevoked
		}
		if err := check(c); err != nil {
			return err
		}
		return h.Store.CheckGraph(c, revision)
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return nil, ErrInvalid
	}
	if err := fence(ctx); err != nil {
		return nil, err
	}
	b, err := h.ServiceInstance(ctx, revision, contract)
	if err != nil {
		return nil, err
	}
	p, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return nil, err
	}
	if !hasRequest(p.Manifest, "resource.call") || hasRequest(p.Manifest, "state.run.write") || hasRequest(p.Manifest, "evolution.propose") {
		return nil, ErrRevoked
	}
	service, ok := findService(p.Manifest, contract)
	if !ok || !tool.Validate(string(service.InputSchema), string(input)) {
		return nil, ErrInvalid
	}
	scope, err := newInputScope(inputs)
	if err != nil || len(inputs) > 0 && !hasRequest(p.Manifest, "input.read") {
		return nil, ErrInvalid
	}
	resources, err := newResourceScope(ports, fence)
	if err != nil {
		return nil, err
	}
	runner := Runner{Catalog: h.Catalog, resources: resources, ReadInput: func(c context.Context, in rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
		if err := fence(c); err != nil {
			return rsiplugin.InputChunk{}, err
		}
		return scope.read(c, in)
	}}
	var output rsiplugin.Result
	err = runner.Invoke(ctx, b, "call", rsiplugin.Call{OperationID: "domain-drive", Execution: rsiplugin.Execution{GraphRevision: revision}, Contract: contract, Input: input, Inputs: scope.descriptor, State: rsiplugin.State{Value: p.Manifest.State.Initial}}, &output)
	if authority := fence(ctx); authority != nil {
		if resources.dispatched > 0 {
			return nil, errors.Join(ErrUnknown, authority)
		}
		return nil, authority
	}
	if err == nil && (len(resources.pending) != 0 || output.State != nil || !tool.ValidateBounded(string(service.OutputSchema), string(output.Output), rsiplugin.MaxResultBytes)) {
		err = ErrInvalid
	}
	if err != nil {
		if resources.dispatched > 0 {
			return nil, errors.Join(ErrUnknown, err)
		}
		return nil, err
	}
	return output.Output, nil
}
