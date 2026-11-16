// Package webimage owns the experimental Web Access image conversion recipe.
package webimage

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/media"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
)

func normalize(data []byte, kind string) (Output, error) {
	if len(data) > media.MaxImageBytes {
		return Output{}, errors.New("RESOURCE_LIMIT")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width) > media.MaxImagePixels/int64(cfg.Height) {
		return Output{}, errors.New("INVALID_IMAGE")
	}
	if kind != "image/"+format || (format != "png" && format != "jpeg" && format != "gif" && format != "webp") {
		return Output{}, errors.New("INVALID_IMAGE")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Output{}, errors.New("INVALID_IMAGE")
	}
	scale := math.Min(1, math.Min(2000/float64(cfg.Width), 2000/float64(cfg.Height)))
	width, height := max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var output bytes.Buffer
	if err = jpeg.Encode(&output, dst, &jpeg.Options{Quality: 85}); err != nil {
		return Output{}, err
	}
	return Output{Data: output.Bytes(), MediaType: "image/jpeg", Width: width, Height: height, Text: fmt.Sprintf("Image fetched (%d x %d)", width, height)}, nil
}
