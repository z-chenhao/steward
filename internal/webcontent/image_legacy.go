package webcontent

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"

	"github.com/z-chenhao/assistant/internal/media"
	"golang.org/x/image/draw"
)

// Frozen V1 converter for pre-image-service Runs and documented unbundled or previously selected nineteen-package
// carriers only. Managed decoders never fall back to it. Remove when those
// retained executions/carriers no longer depend on the original JPEG recipe.
func resizeImageLegacyV1(input *Image) (*Image, error) {
	if input == nil || len(input.Data) > base64.StdEncoding.EncodedLen(media.MaxImageBytes) {
		return nil, errors.New("RESOURCE_LIMIT")
	}
	data, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		return nil, errors.New("INVALID_IMAGE")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width) > media.MaxImagePixels/int64(cfg.Height) {
		return nil, errors.New("INVALID_IMAGE")
	}
	if format != "png" && format != "jpeg" && format != "gif" && format != "webp" {
		return nil, errors.New("INVALID_IMAGE")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("INVALID_IMAGE")
	}
	scale := math.Min(1, math.Min(2000/float64(cfg.Width), 2000/float64(cfg.Height)))
	width, height := max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err = jpeg.Encode(&output, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return &Image{Data: base64.StdEncoding.EncodeToString(output.Bytes()), MIMEType: "image/jpeg", Width: width, Height: height}, nil
}
