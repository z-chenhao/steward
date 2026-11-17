package rsiplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

type Request struct {
	Protocol string          `json:"protocol"`
	ID       string          `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
}
type Response struct {
	Protocol string          `json:"protocol"`
	ID       string          `json:"id"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// Serve speaks bounded request/response frames over stdin/stdout. Plugin logs
// belong on stderr. Host owns process cancellation, limits and OS isolation.
func Serve(ctx context.Context, in io.Reader, out io.Writer, p Plugin) error {
	reader := bufio.NewScanner(in)
	reader.Buffer(make([]byte, 4096), MaxFrameBytes+1)
	writer := json.NewEncoder(out)
	writer.SetEscapeHTML(false)
	var dependencyMu sync.Mutex
	dependencySequence := 0
	prepared, draining := false, false
	for reader.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req Request
		if err := Decode(reader.Bytes(), &req); err != nil {
			return err
		}
		if req.Protocol != Protocol || req.ID == "" {
			return errors.New("unsupported protocol")
		}
		var value any
		var err error
		exchange := func(callCtx context.Context, method string, input any) (json.RawMessage, error) {
			dependencyMu.Lock()
			defer dependencyMu.Unlock()
			if err := callCtx.Err(); err != nil {
				return nil, err
			}
			dependencySequence++
			id := fmt.Sprintf("dependency-%d", dependencySequence)
			params, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			if err = writer.Encode(Request{Protocol: Protocol, ID: id, Method: method, Params: params}); err != nil {
				return nil, err
			}
			if !reader.Scan() {
				return nil, io.ErrUnexpectedEOF
			}
			var reply Response
			if err = Decode(reader.Bytes(), &reply); err != nil {
				return nil, err
			}
			if reply.Protocol != Protocol || reply.ID != id || reply.Error != "" {
				return nil, errors.New("dependency failed")
			}
			return reply.Result, nil
		}
		callCtx := context.WithValue(ctx, dependencyClientKey{}, dependencyClient(func(c context.Context, in DependencyCall) (json.RawMessage, error) {
			return exchange(c, "dependency", in)
		}))
		callCtx = context.WithValue(callCtx, proposalClientKey{}, proposalClient(func(c context.Context, in Proposal) (ProposalReceipt, error) {
			raw, err := exchange(c, "propose", in)
			if err != nil {
				return ProposalReceipt{}, err
			}
			var out ProposalReceipt
			err = Decode(raw, &out)
			return out, err
		}))
		callCtx = context.WithValue(callCtx, inputClientKey{}, inputClient(func(c context.Context, in InputRead) (InputChunk, error) {
			raw, err := exchange(c, "read_input", in)
			if err != nil {
				return InputChunk{}, err
			}
			var out InputChunk
			err = Decode(raw, &out)
			return out, err
		}))
		callCtx = context.WithValue(callCtx, computeClientKey{}, computeClient(func(c context.Context, in ComputeRequest) (ComputeResult, error) {
			raw, err := exchange(c, "compute", in)
			if err != nil {
				return ComputeResult{}, err
			}
			var out ComputeResult
			err = Decode(raw, &out)
			return out, err
		}))
		callCtx = context.WithValue(callCtx, resourceClientKey{}, resourceClient{mu: &sync.Mutex{}, exchange: exchange})
		callCtx = context.WithValue(callCtx, resultClientKey{}, resultClient(func(c context.Context, in ResultChunk) error {
			_, err := exchange(c, "write_result", in)
			return err
		}))
		switch req.Method {
		case "describe":
			value = p.Describe()
		case "prepare":
			var v PrepareInput
			err = Decode(req.Params, &v)
			if err == nil && (prepared || v.Protocol != Protocol) {
				err = errors.New("invalid lifecycle")
			}
			if err == nil {
				err = p.Prepare(ctx, v)
				prepared = err == nil
			}
		case "call":
			var v Call
			err = Decode(req.Params, &v)
			if err == nil && (!prepared || draining) {
				err = errors.New("not serving")
			}
			if err == nil {
				value, err = p.Call(callCtx, v)
			}
		case "hook":
			var v HookCall
			err = Decode(req.Params, &v)
			if err == nil && (!prepared || draining || !v.Input.Stage.Valid()) {
				err = errors.New("not serving")
			}
			if err == nil {
				value, err = p.Hook(callCtx, v)
			}
		case "drain":
			if !prepared {
				err = errors.New("not prepared")
			} else {
				draining = true
				err = p.Drain(ctx)
			}
		case "dispose":
			err = p.Dispose(ctx)
		default:
			err = errors.New("unsupported method")
		}
		resp := Response{Protocol: Protocol, ID: req.ID}
		if err != nil {
			resp.Error = "PLUGIN_FAILED"
		} else {
			resp.Result, err = marshalFrame(value)
			if err != nil {
				return err
			}
		}
		raw, err := marshalFrame(resp)
		if err != nil {
			return err
		}
		if len(raw) > MaxFrameBytes {
			return errors.New("response limit")
		}
		if err = writer.Encode(resp); err != nil {
			return err
		}
		if req.Method == "dispose" {
			return nil
		}
	}
	return reader.Err()
}

// RPC frames carry JSON, never HTML. Avoid expanding bounded text sixfold
// merely because it contains XML punctuation; the byte frame cap still holds.
func marshalFrame(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
