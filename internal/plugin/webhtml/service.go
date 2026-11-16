package webhtml

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const Contract = "steward.web-html/1"
const OutputContract = "steward.web-html-output/1"

// Bytes avoid JSON string escaping shrinking the original 8 MiB HTML support.
// This output is untrusted derived text, never source URLs or provenance.
type Output struct {
	Title []byte `json:"title"`
	Text  []byte `json:"text"`
}
type Plan struct {
	Digest     string `json:"digest"`
	TitleBytes int    `json:"title_bytes"`
	TextBytes  int    `json:"text_bytes"`
}

func Service(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "html" {
		return rsiplugin.Result{}, errors.New("missing original HTML")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	if !utf8.Valid(data) {
		return rsiplugin.Result{}, errors.New("invalid HTML encoding")
	}
	title, text := parseHTML(string(data))
	output := Output{Title: []byte(title), Text: []byte(text)}
	raw, err := json.Marshal(output)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var ack struct{}
	if err = rsiplugin.CallResource(ctx, OutputContract, "decoded", output, &ack); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, Plan{Digest: rsiplugin.FileDigest(raw), TitleBytes: len(title), TextBytes: len(text)})
}
