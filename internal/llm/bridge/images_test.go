package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/media"
)

func TestImagePreviewBoundsAndEnvelopeSelection(t *testing.T) {
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2000, 1))); err != nil {
		t.Fatal(err)
	}
	payload, err := previewImage("image/png", pixels.Bytes())
	if err != nil || payload.PreviewWidth != 640 || payload.PreviewHeight != 1 || payload.OriginalWidth != 2000 {
		t.Fatal(payload, err)
	}
	jpeg, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpeg))
	if err != nil || cfg.Width != payload.PreviewWidth || cfg.Height != payload.PreviewHeight {
		t.Fatal(cfg, err)
	}
	oversized := append([]byte(nil), pixels.Bytes()...)
	binary.BigEndian.PutUint32(oversized[16:20], 65536)
	binary.BigEndian.PutUint32(oversized[20:24], 65536)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	for _, tc := range []struct {
		media string
		data  []byte
	}{
		{"image/gif", pixels.Bytes()}, {"image/png", []byte("corrupt")}, {"image/png", make([]byte, media.MaxImageBytes+1)}, {"image/png", oversized},
	} {
		if _, err := previewImage(tc.media, tc.data); err != llm.ModelIncompatible {
			t.Fatal("unsafe image admitted", tc.media, len(tc.data), err)
		}
	}
	reads := 0
	r := llm.GenerationRequest{Snapshot: llm.Snapshot{Capabilities: llm.Capabilities{ImageInput: true}}, ResolveImage: func(context.Context, string) (string, []byte, error) {
		reads++
		return "image/png", pixels.Bytes(), nil
	}, Messages: []llm.Message{{Role: "user", ImageRefs: []string{"same", "same"}}}}
	images, err := resolveImages(context.Background(), r)
	if err != nil || len(images) != 1 || reads != 1 {
		t.Fatal("duplicate pixel reads", images, reads, err)
	}
	r.Messages[0].ImageRefs = nil
	for i := 0; i <= llm.MaxInputImages; i++ {
		r.Messages[0].ImageRefs = append(r.Messages[0].ImageRefs, fmt.Sprint(i))
	}
	if _, err := resolveImages(context.Background(), r); err != llm.ModelIncompatible {
		t.Fatal("unbounded images", err)
	}
}

// Generated from an 8 x 4 solid red RGB fixture with lossless cwebp.
const selectedWebP = "UklGRh4AAABXRUJQVlA4TBEAAAAvB8AAAAdQhXoXof+BiOh/AAA="

func TestStaticWebPUsesBoundedModelInputDecoder(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(selectedWebP)
	if err != nil {
		t.Fatal(err)
	}
	p, err := previewImage("image/webp", raw)
	if err != nil || p.MediaType != "image/jpeg" || p.OriginalWidth != 8 || p.OriginalHeight != 4 {
		t.Fatal(p, err)
	}
	preview, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(preview))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(3, 2).RGBA()
	if r < 4*g || r < 4*b {
		t.Fatal("lost the red pixels")
	}
	extended := append([]byte("RIFF0000WEBPVP8X"), 10, 0, 0, 0, 2, 0, 0, 0, 7, 0, 0, 3, 0, 0)
	animated := append(extended, raw[12:]...)
	binary.LittleEndian.PutUint32(animated[4:], uint32(len(animated)-8))
	if _, err = previewImage("image/webp", animated); err != llm.ModelIncompatible {
		t.Fatal("animated input accepted", err)
	}
	oversized := append([]byte(nil), animated...)
	oversized[20] = 0
	copy(oversized[24:30], []byte{255, 31, 0, 255, 31, 0})
	if _, err = media.DecodeModelImage("image/webp", oversized); err != media.ErrLimit {
		t.Fatal("pixel limit", err)
	}
	if _, err = media.DecodeImage("image/webp", raw); err != media.ErrInvalid {
		t.Fatal("changed the OCR format boundary", err)
	}
	if _, err = previewImage("image/png", raw); err != llm.ModelIncompatible {
		t.Fatal("accepted a disguised WebP")
	}
}

func TestExplicitScopedImageIsSentToNativeBridgeAndSanitizedObservation(t *testing.T) {
	ctx := context.Background()
	var pngBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.White)
	png.Encode(&pngBytes, img)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			t.Error("wire decode")
		}
		wire, _ := json.Marshal(v)
		if !strings.Contains(string(wire), "data:image/jpeg;base64,") {
			t.Error("selected image absent from actual provider request", string(wire))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"seen\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	endpoint := server.URL + "/v1"
	command, argument := nativeBridgeProcess(t)
	client, err := OpenLocal(ctx, command, argument, func(context.Context, llm.Snapshot) (llm.Connection, error) {
		return llm.Connection{Endpoint: endpoint}, nil
	}, func(_ context.Context, r llm.GenerationRequest, _ llm.Connection, _ llm.GenerationResult, _ time.Duration, _ error) error {
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "base64") || strings.Contains(string(raw), "media_type") {
			t.Error("binary entered persisted request")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	captured := make(chan llm.Dispatch, 16)
	client.SetDispatchObserver(func(_ llm.GenerationRequest, d llm.Dispatch) { captured <- d })
	hash, _ := llm.EndpointHash(endpoint)
	req := llm.GenerationRequest{OwnerToken: "host-observation-test", GenerationID: "image", RunID: "run", SessionID: "run", Snapshot: llm.Snapshot{SnapshotID: "s", ProviderKind: "openai_compatible", WireAPI: "openai_completions", EndpointHash: hash, ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 1024, Capabilities: llm.Capabilities{ImageInput: true}, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}}, Messages: []llm.Message{{Role: "user", Text: "selected image", ImageRefs: []string{"artifact:allowed"}}}, ResolveImage: func(_ context.Context, ref string) (string, []byte, error) {
		if ref != "artifact:allowed" {
			t.Fatal("unselected read", ref)
		}
		return "image/png", pngBytes.Bytes(), nil
	}}
	result, err := client.Generate(ctx, req, nil)
	if err != nil || result.Text != "seen" {
		t.Fatal(result, err)
	}
	webp, _ := base64.StdEncoding.DecodeString(selectedWebP)
	req.ResolveImage = func(_ context.Context, ref string) (string, []byte, error) {
		if ref != "artifact:allowed" {
			t.Fatal("wrong original reference", ref)
		}
		return "image/webp", webp, nil
	}
	if result, err = client.Generate(ctx, req, nil); err != nil || result.Text != "seen" {
		t.Fatal(result, err)
	}
	var wire []byte
	for len(captured) > 0 {
		d := <-captured
		if d.Phase == "started" {
			wire = d.Body
		}
	}
	if !bytes.Contains(wire, []byte("artifact:allowed")) || !bytes.Contains(wire, []byte("sha256")) || bytes.Contains(wire, []byte("base64")) || bytes.Contains(wire, []byte("data:image")) {
		t.Fatal("image observation not safely correlated", string(wire))
	}
	req.Snapshot.Capabilities.ImageInput = false
	req.ResolveImage = func(context.Context, string) (string, []byte, error) {
		t.Fatal("text-only model read image bytes")
		return "", nil, nil
	}
	images, err := resolveImages(ctx, req)
	if err != nil || images != nil {
		t.Fatal(images, err)
	}
	req.Snapshot.Capabilities.ImageInput = true
	req.ResolveImage = nil
	if _, err := resolveImages(ctx, req); err != llm.ModelIncompatible {
		t.Fatal("missing scope resolver accepted", err)
	}
}
