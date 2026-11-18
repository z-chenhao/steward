// Package media owns bounded content decoding, independent of storage and models.
package media

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

const MaxImageBytes = 8 << 20
const MaxImagePixels = 32 << 20

var ErrInvalid = errors.New("invalid image")
var ErrLimit = errors.New("image limit exceeded")

// DecodeImage is the original PNG/JPEG decoder used by OCR and tool outputs.
func DecodeImage(kind string, data []byte) (image.Image, error) {
	return decodeImage(kind, data, false)
}

// DecodeModelImage also accepts a static WebP as a selected visual input.
func DecodeModelImage(kind string, data []byte) (image.Image, error) {
	return decodeImage(kind, data, true)
}

func decodeImage(kind string, data []byte, allowWebP bool) (image.Image, error) {
	if len(data) > MaxImageBytes {
		return nil, ErrLimit
	}
	if kind != "image/png" && kind != "image/jpeg" && !(allowWebP && kind == "image/webp") {
		return nil, ErrInvalid
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return nil, ErrInvalid
	}
	if kind == "image/webp" {
		if format != "webp" || (len(data) > 20 && string(data[12:16]) == "VP8X" && data[20]&2 != 0) {
			return nil, ErrInvalid
		}
	} else if format != "jpeg" && format != "png" {
		return nil, ErrInvalid
	}
	if int64(cfg.Width) > MaxImagePixels/int64(cfg.Height) {
		return nil, ErrLimit
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	return img, nil
}
