// Package docxreader owns the independently built DOCX feature implementation.
// The Host retains artifact authorization, paging, receipts and privacy fences.
package docxreader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/media/docx"
	"github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Contract: extract}}
}
func extract(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.Request
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != request.InputID {
		return rsiplugin.Result{}, errors.New("missing document input")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	text, err := docx.Text(ctx, bytes.NewReader(data))
	response := contract.Response{Text: text}
	switch {
	case errors.Is(err, docx.ErrInvalid):
		response.ErrorCode = "INVALID_ARGUMENT"
	case errors.Is(err, docx.ErrLimit):
		response.ErrorCode = "RESOURCE_LIMIT"
	case err != nil:
		return rsiplugin.Result{}, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(response); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.Result{Output: bytes.TrimSuffix(out.Bytes(), []byte("\n"))}, nil
}
