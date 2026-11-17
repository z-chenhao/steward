package webimage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const Contract = "steward.web-image/1"
const OutputContract = "steward.web-image-output/1"

type Request struct {
	MediaType string `json:"media_type"`
}
type Output struct {
	Data      []byte `json:"data"`
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Text      string `json:"text"`
}
type Plan struct {
	Digest    string `json:"digest"`
	ErrorCode string `json:"error_code"`
}

func Service(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input Request
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "image" {
		return rsiplugin.Result{}, errors.New("missing original image")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	output, err := normalize(data, input.MediaType)
	if err != nil {
		return rsiplugin.JSONResult(ctx, Plan{ErrorCode: "INVALID_IMAGE"})
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var ack struct{}
	if err = rsiplugin.CallResource(ctx, OutputContract, "decoded", output, &ack); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, Plan{Digest: rsiplugin.FileDigest(raw)})
}
