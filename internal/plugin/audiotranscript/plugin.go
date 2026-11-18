// Package audiotranscript interprets the pinned whisper.cpp JSON format.
package audiotranscript

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/audiotranscript/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: transcript}}
}

func transcript(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
	var req contract.Request
	if rsiplugin.Decode(c.Input, &req) != nil || req.DurationMS < 1 || req.DurationMS > contract.MaxDurationMS {
		return rsiplugin.Result{}, errors.New("invalid recording duration")
	}
	if req.Operation == "plan" && len(c.Inputs) == 0 {
		return rsiplugin.JSONResult(ctx, contract.Result{Decoder: "whisper-tiny-en-cpu"})
	}
	if req.Operation != "segments" || len(c.Inputs) != 1 || c.Inputs[0].ID != "inference" || c.Inputs[0].Size > 1<<20 {
		return rsiplugin.Result{}, errors.New("invalid recognition input")
	}
	raw, err := rsiplugin.ReadInput(ctx, c.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, parse(raw, req.DurationMS))
}
func parse(raw []byte, duration int64) contract.Result {
	invalid := contract.Result{Error: "incompatible_output"}
	var report struct {
		Transcription *[]struct {
			Offsets struct{ From, To *int64 }
			Text    string
		} `json:"transcription"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &report) != nil || report.Transcription == nil || len(*report.Transcription) > 2048 {
		return invalid
	}
	var out contract.Result
	var last int64
	for _, segment := range *report.Transcription {
		from, to := segment.Offsets.From, segment.Offsets.To
		if from == nil || to == nil || *from < last || *to <= *from || *from >= duration || *to > duration+20 {
			return invalid
		}
		last = min(*to, duration)
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		if len(text) > 8192 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return invalid
		}
		out.Segments = append(out.Segments, contract.Segment{StartMS: *from, EndMS: last, Text: text})
	}
	if len(out.Segments) == 0 {
		out.Error = "no_speech"
	}
	return out
}
