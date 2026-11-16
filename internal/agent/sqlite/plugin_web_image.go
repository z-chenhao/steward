package sqlite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/media"
	"github.com/z-chenhao/assistant/internal/plugin/webimage"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var errWebImageMethod = errors.New("managed image decoder unavailable")

// The original page owner supplies only authorized source bytes. Conversion is
// plugin business; original network/provenance, codec allocation limits and
// accepted image Artifact transactions remain owner mechanisms.
func (t *tools) webImageContext(ctx context.Context, h rsi.Host, revision string, check func(context.Context) error) (context.Context, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), webimage.Contract)
	if err != nil || !managed {
		return ctx, err
	}
	return webcontent.WithImageParser(ctx, func(c context.Context, data []byte, kind string) (*webcontent.Image, string, error) {
		var output webimage.Output
		var digest string
		written := false
		port := rsi.Resource{Contract: webimage.OutputContract,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"data":{"type":"string","maxLength":11184812},"media_type":{"type":"string","enum":["image/jpeg","image/png"]},"width":{"type":"integer","minimum":1},"height":{"type":"integer","minimum":1},"text":{"type":"string","maxLength":8388608}},"required":["data","media_type","width","height","text"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
				if key != "decoded" || written || rsiplugin.DecodeResource(raw, &output) != nil || len(output.Data) > media.MaxImageBytes || !utf8.ValidString(output.Text) {
					return nil, rsi.ErrInvalid
				}
				cfg, format, err := image.DecodeConfig(bytes.NewReader(output.Data))
				if err != nil || output.MediaType != "image/"+format || cfg.Width != output.Width || cfg.Height != output.Height {
					return nil, rsi.ErrInvalid
				}
				// Validate the entire encoded image, not just a forgeable header.
				if _, err = media.DecodeImage(output.MediaType, output.Data); err != nil {
					return nil, rsi.ErrInvalid
				}
				written = true
				digest = rsiplugin.FileDigest(raw)
				return json.RawMessage(`{}`), nil
			}}
		input, _ := json.Marshal(webimage.Request{MediaType: kind})
		raw, err := h.DriveService(c, revision, webimage.Contract, input, []rsi.InputData{{ID: "image", Data: data}}, check, []rsi.Resource{port})
		t.runs.host.boundary("after_web_image_method")
		if err == nil {
			err = check(c)
		}
		if err == nil {
			err = h.Store.CheckGraph(c, revision)
		}
		var plan webimage.Plan
		if err == nil && rsiplugin.DecodeResult(raw, &plan) != nil {
			err = rsi.ErrInvalid
		}
		if err == nil && !written && plan.Digest == "" && plan.ErrorCode == "INVALID_IMAGE" {
			return nil, "", errors.New(plan.ErrorCode)
		}
		if err == nil && (!written || plan.Digest != digest || plan.ErrorCode != "") {
			err = rsi.ErrInvalid
		}
		if err != nil {
			return nil, "", errors.Join(errWebImageMethod, err)
		}
		return &webcontent.Image{Data: base64.StdEncoding.EncodeToString(output.Data), MIMEType: output.MediaType, Width: output.Width, Height: output.Height}, output.Text, nil
	}), nil
}
