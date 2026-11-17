package webcontent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/z-chenhao/assistant/internal/media"
	_ "golang.org/x/image/webp"
)

type Image struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type imageParserKey struct{}
type ImageParser func(context.Context, []byte, string) (*Image, string, error)

// WithImageParser binds only the original public-page image decoding operation.
func WithImageParser(ctx context.Context, parse ImageParser) context.Context {
	return context.WithValue(ctx, imageParserKey{}, parse)
}

// ImageSource validates allocation limits before releasing original bytes to a
// decoder. Detected GIF/WebP remain supported even if the response MIME differs.
func ImageSource(data []byte) (image.Config, string, error) {
	if len(data) > media.MaxImageBytes {
		return image.Config{}, "", errors.New("RESOURCE_LIMIT")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width) > media.MaxImagePixels/int64(cfg.Height) {
		return image.Config{}, "", errors.New("INVALID_IMAGE")
	}
	if format != "png" && format != "jpeg" && format != "gif" && format != "webp" {
		return image.Config{}, "", errors.New("INVALID_IMAGE")
	}
	return cfg, format, nil
}

// ParseImage never uses the historical recipe after a managed decoder failure.
func ParseImage(ctx context.Context, data []byte, kind string) (*Image, string, error) {
	_, format, err := ImageSource(data)
	if err != nil {
		return nil, "", err
	}
	if parse, ok := ctx.Value(imageParserKey{}).(ImageParser); ok {
		return parse(ctx, data, "image/"+format)
	}
	out, err := NormalizeImage(base64.StdEncoding.EncodeToString(data), kind)
	if err != nil {
		return nil, "", err
	}
	return out, fmt.Sprintf("Image fetched (%d x %d)", out.Width, out.Height), nil
}

// NormalizeImage is only the explicit historical/native decoder entry retained
// by pre-service/unbundled carriers and original-byte compatibility fixtures.
func NormalizeImage(data, kind string) (*Image, error) {
	return resizeImageLegacyV1(&Image{Data: data, MIMEType: kind})
}
