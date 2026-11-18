package web

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type previewCore struct {
	*updatesCore
	d       artifact.Descriptor
	body    []byte
	revoked bool
}

func (c *previewCore) DescribeArtifact(context.Context, string) (artifact.Descriptor, error) {
	return c.d, nil
}
func (c *previewCore) OpenArtifact(context.Context, string) (io.ReadCloser, error) {
	if c.revoked {
		return nil, errors.New("revoked")
	}
	return io.NopCloser(bytes.NewReader(c.body)), nil
}

func TestArtifactReviewPreviewAndOriginalDownload(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	core := &previewCore{updatesCore: &updatesCore{allowed: true, status: "idle"}}
	s := &Server{Core: core, WebWorkbench: currentWorkbenchReader(t)}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path+"?ref=artifact:original&workbench_revision=sha256:"+strings.Repeat("b", 64), nil)
		r.RemoteAddr = "127.0.0.1:4321"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, c := range []struct {
		name, media, want string
		body              []byte
	}{
		{"notes.html", "text/html", "text/plain; charset=utf-8", []byte("<h1>meeting notes</h1>")},
		{"diagram.png", "image/png", "image/png", picture.Bytes()},
		{"report.pdf", "application/pdf", "", []byte("%PDF-1.5")},
		{"incorrect.png", "image/png", "", []byte("wrong format")},
	} {
		core.d = artifact.Descriptor{Name: c.name, MediaType: c.media, ByteSize: int64(len(c.body))}
		core.body = c.body
		w := request("/artifact/preview")
		if c.want == "" {
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatal(c.name, w.Code)
			}
		} else if w.Code != 200 || w.Header().Get("Content-Type") != c.want || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") || !bytes.Equal(w.Body.Bytes(), c.body) {
			t.Fatal(c.name, w.Code, w.Header())
		}
		w = request("/artifact")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || !bytes.Equal(w.Body.Bytes(), c.body) {
			t.Fatal("original download changed", c.name, w.Code)
		}
	}
	workbench := s.WebWorkbench
	s.WebWorkbench = nil
	if w := request("/artifact/preview"); w.Code != 404 {
		t.Fatal("preview served without its owning workbench", w.Code)
	}
	s.WebWorkbench = workbench
	core.revoked = true
	if w := request("/artifact/preview"); w.Code != 404 {
		t.Fatal("revoked file previewed", w.Code)
	}
}
