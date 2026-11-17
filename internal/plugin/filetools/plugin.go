package filetools

import (
	"bytes"
	"context"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func readFile(ctx context.Context, c rsiplugin.Call, id string) ([]byte, error) {
	if len(c.Inputs) != 1 || c.Inputs[0].ID != id {
		return nil, errors.New("missing authorized file input")
	}
	return rsiplugin.ReadInput(ctx, c.Inputs[0])
}

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.1", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){
		contract.WindowContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.WindowFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			if f.Offset < 1 || f.Limit < 1 || f.Limit > 2000 || f.OutputBytes < 1 || f.OutputBytes > 48000 {
				return rsiplugin.Result{}, errors.New("invalid file window bounds")
			}
			data, err := readFile(ctx, c, f.InputID)
			if err != nil {
				return rsiplugin.Result{}, err
			}
			return rsiplugin.JSONResult(ctx, selectWindow(data, f))
		},
		contract.EditContract: func(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
			var f contract.EditFacts
			if err := rsiplugin.Decode(c.Input, &f); err != nil {
				return rsiplugin.Result{}, err
			}
			data, err := readFile(ctx, c, f.InputID)
			if err != nil {
				return rsiplugin.Result{}, err
			}
			if f.OldText == "" || !utf8.Valid(data) || bytes.Count(data, []byte(f.OldText)) != 1 {
				return rsiplugin.JSONResult(ctx, contract.EditSpan{ErrorCode: "INVALID_ARGUMENT"})
			}
			start := bytes.Index(data, []byte(f.OldText))
			return rsiplugin.JSONResult(ctx, contract.EditSpan{Start: &start})
		},
	}}
}

// Match the existing Python splitlines(keepends=True) semantics, including CRLF
// and Unicode separators. Stream through the file; an 8MiB newline-only input
// must not allocate a slice for eight million lines.
func lineEnd(data []byte, start int) int {
	for p := start; p < len(data); {
		r, size := utf8.DecodeRune(data[p:])
		p += size
		switch r {
		case '\r':
			if p < len(data) && data[p] == '\n' {
				p++
			}
			return p
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', '\u2028', '\u2029':
			return p
		}
	}
	return len(data)
}

func selectWindow(data []byte, f contract.WindowFacts) contract.Window {
	if f.AsArtifact || !utf8.Valid(data) {
		return contract.Window{Artifact: true}
	}
	start, line := 0, 1
	for line < f.Offset && start < len(data) {
		start = lineEnd(data, start)
		line++
	}
	result := contract.Window{Start: start, End: start, NextOffset: f.Offset}
	if line < f.Offset || start == len(data) {
		return result
	}
	for count := 0; count < f.Limit && result.End < len(data); count++ {
		end := lineEnd(data, result.End)
		if end-start > f.OutputBytes {
			if count == 0 {
				end = start + f.OutputBytes
				for end > start && !utf8.Valid(data[start:end]) {
					end--
				}
				result.End = end
				result.NextOffset++
			}
			break
		}
		result.End = end
		result.NextOffset++
	}
	return result
}
