package web

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"net/http"
	"strings"
)

// Bundled, pinned frontend components and Steward artwork and self-hosted fonts. No CDN or second server.
//
//go:embed assets/client.js assets/diagrams.js assets/appearance.js assets/widget.css assets/widget-runtime.js assets/preview-runtime.js assets/style.css assets/licenses.txt assets/brand/* assets/apps/*
var assets embed.FS

// This fixed allowlist is shared by requests; no user path reaches the filesystem.
var publicAssets = map[string]struct{ path, contentType string }{
	"/style.css":                         {"assets/style.css", "text/css"},
	"/appearance.js":                     {"assets/appearance.js", "text/javascript"},
	"/app.js":                            {"assets/client.js", "text/javascript"},
	"/diagrams.js":                       {"assets/diagrams.js", "text/javascript"},
	"/licenses.txt":                      {"assets/licenses.txt", "text/plain; charset=utf-8"},
	"/brand/ghost-ivory.png":             {"assets/brand/ghost-ivory.png", "image/png"},
	"/brand/ghost-charcoal.png":          {"assets/brand/ghost-charcoal.png", "image/png"},
	"/brand/ghost-charcoal-96.webp":      {"assets/brand/ghost-charcoal-96.webp", "image/webp"},
	"/brand/ghost-ivory-96.webp":         {"assets/brand/ghost-ivory-96.webp", "image/webp"},
	"/brand/ghost-charcoal-256.webp":     {"assets/brand/ghost-charcoal-256.webp", "image/webp"},
	"/brand/ghost-ivory-256.webp":        {"assets/brand/ghost-ivory-256.webp", "image/webp"},
	"/brand/Michroma-Regular.ttf":        {"assets/brand/Michroma-Regular.ttf", "font/ttf"},
	"/brand/JetBrainsMono-Regular.woff2": {"assets/brand/JetBrainsMono-Regular.woff2", "font/woff2"},
	"/brand/SourceSerif4-Roman.woff2":    {"assets/brand/SourceSerif4-Roman.woff2", "font/woff2"},
	"/brand/InterVariable.woff2":         {"assets/brand/InterVariable.woff2", "font/woff2"},
	"/favicon.png":                       {"assets/brand/ghost-charcoal.png", "image/png"},
}

// Font URLs identify the shipped bytes, so a new build cannot reuse stale fonts.
// Keep this digest in sync with the font URLs emitted by web/build.mjs.
func fontURL(path string) (string, error) {
	body, err := assets.ReadFile("assets" + path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?v=%x", path, sha256.Sum256(body)), nil
}

func writeAsset(w http.ResponseWriter, r *http.Request, body []byte, contentType string) {
	if strings.HasPrefix(contentType, "font/") && r.URL.Query().Get("v") == fmt.Sprintf("%x", sha256.Sum256(body)) {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

// serveAsset runs after request guards and before workspace authentication.
// Unknown paths continue through the existing authenticated router.
func serveAsset(w http.ResponseWriter, r *http.Request) bool {
	// Only shipped character subsets are public font assets; no filesystem fallback.
	if strings.HasPrefix(r.URL.Path, "/brand/SourceHanSansSC-") && strings.HasSuffix(r.URL.Path, ".woff2") {
		name := strings.TrimPrefix(r.URL.Path, "/brand/")
		if strings.Contains(name, "/") {
			fail(w, 404)
			return true
		}
		body, err := assets.ReadFile("assets/brand/" + name)
		if err != nil {
			fail(w, 404)
			return true
		}
		writeAsset(w, r, body, "font/woff2")
		return true
	}
	if file, ok := publicAssets[r.URL.Path]; ok {
		body, err := assets.ReadFile(file.path)
		if err != nil {
			fail(w, 500)
			return true
		}
		writeAsset(w, r, body, file.contentType)
		return true
	}
	return false
}
