package webcontent

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"testing"
)

func TestWebAccessImageDecoderKeepsOriginalScopeAndFailsClosed(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 3, 4))); err != nil {
		t.Fatal(err)
	}
	original := source.Bytes()
	kind := "image/unreliable-header"
	calls := 0
	sentinel := errors.New("managed decoder failed")
	ctx := WithImageParser(context.Background(), func(_ context.Context, data []byte, mime string) (*Image, string, error) {
		calls++
		if !bytes.Equal(data, original) || mime != "image/png" {
			t.Fatal("changed original bytes or actual codec", mime)
		}
		return nil, "", sentinel
	})
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, kind, string(original)), nil })}
	if _, err := fetchContent(ctx, Config{}, "https://example.com/image", "readable", client); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatal("decoder failure fell back", err, calls)
	}
	for _, mode := range []string{"raw", "answer"} {
		if _, err := fetchContent(ctx, Config{}, "https://example.com/image", mode, client); err == nil || err.Error() != "WEB_UNSUPPORTED_CONTENT" || calls != 1 {
			t.Fatal(mode, err, calls)
		}
	}
	if _, _, err := ParseImage(ctx, make([]byte, (8<<20)+1), "image/png"); err == nil || err.Error() != "RESOURCE_LIMIT" || calls != 1 {
		t.Fatal(err, calls)
	}
	if _, _, err := ParseImage(ctx, []byte("not image"), "image/png"); err == nil || err.Error() != "INVALID_IMAGE" || calls != 1 {
		t.Fatal(err, calls)
	}
	// A legal PNG header can claim an excessive allocation even before IDAT exists.
	huge := append([]byte{}, original...)
	huge[16], huge[17], huge[18], huge[19] = 1, 0, 0, 0
	// Recompute the IHDR checksum so pixel validation, rather than malformed CRC,
	// is the reason this declared allocation is rejected.
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	if _, _, err := ParseImage(ctx, huge, "image/png"); err == nil || calls != 1 {
		t.Fatal("unbounded pixels released to plugin", err, calls)
	}
	ctx = WithImageParser(context.Background(), func(context.Context, []byte, string) (*Image, string, error) { return nil, "", context.Canceled })
	if _, err := fetchContent(ctx, Config{}, "https://example.com/image", "readable", client); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
