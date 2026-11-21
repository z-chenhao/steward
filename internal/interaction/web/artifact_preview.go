package web

import (
	"bytes"
	"image"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/media"
)

func (s *Server) artifactPreviewAllowed(r *http.Request) bool {
	ref := r.URL.Query().Get("workbench_revision")
	if ref == "" || !validModelReader(ref) || s.WebWorkbench == nil {
		return false
	}
	p, revision, managed, err := s.WebWorkbench(r.Context(), ref)
	return err == nil && managed && revision == ref && p.Policy.ArtifactPreview
}

// Only passive text and size-bounded raster images may be opened inline.
// Original bytes remain available separately through the ordinary download.
func artifactPreviewType(d artifact.Descriptor) string {
	kind, _, err := mime.ParseMediaType(d.MediaType)
	if err != nil || d.ByteSize < 0 {
		return ""
	}
	switch {
	case strings.HasPrefix(kind, "text/") || kind == "application/json" || strings.HasSuffix(kind, "+json"):
		if d.ByteSize <= 1<<20 {
			return "text/plain; charset=utf-8"
		}
	case kind == "image/png" || kind == "image/jpeg":
		if d.ByteSize <= media.MaxImageBytes {
			return kind
		}
	}
	return ""
}

func serveArtifactPreview(w http.ResponseWriter, d artifact.Descriptor, src io.Reader) {
	kind := artifactPreviewType(d)
	if kind == "" {
		fail(w, http.StatusUnsupportedMediaType)
		return
	}
	if strings.HasPrefix(kind, "image/") {
		data, err := io.ReadAll(io.LimitReader(src, d.ByteSize))
		cfg, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || int64(len(data)) != d.ByteSize || decodeErr != nil || "image/"+format != kind || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > media.MaxImagePixels/int64(cfg.Height) {
			fail(w, http.StatusUnsupportedMediaType)
			return
		}
		src = bytes.NewReader(data)
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": d.Name}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	_, _ = io.Copy(w, io.LimitReader(src, d.ByteSize))
}
