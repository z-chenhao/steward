package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

type chartExportReader interface {
	WebChartExport(context.Context, string, []int) (ui.ChartExport, error)
}

type chartViewReader interface {
	WebChartView(context.Context, string) (ui.ChartView, error)
}

//go:embed view/chart-bridge.js
var chartPolicyBridge string

func (s *Server) chartView(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["ref"]) != 1 || query.Get("ref") == "" || len(query["format"]) > 1 {
		fail(w, 400)
		return
	}
	for key := range query {
		if key != "ref" && key != "format" && key != "rows" {
			fail(w, 400)
			return
		}
	}
	if query.Get("format") == "export" {
		rows, valid := tableExportIndices(query)
		if !valid {
			fail(w, 400)
			return
		}
		reader, ok := s.Core.(chartExportReader)
		if !ok {
			fail(w, 503)
			return
		}
		data, err := reader.WebChartExport(r.Context(), query.Get("ref"), rows)
		if err != nil {
			fail(w, 404)
			return
		}
		jsonResult(w, data)
		return
	}
	if query.Get("format") != "" || len(query["rows"]) > 0 {
		fail(w, 400)
		return
	}
	reader, ok := s.Core.(chartViewReader)
	if !ok {
		fail(w, 503)
		return
	}
	view, err := reader.WebChartView(r.Context(), query.Get("ref"))
	if err != nil {
		fail(w, 404)
		return
	}
	renderChartPolicyFrame(w, r, view, false)
}

func renderChartPolicyFrame(w http.ResponseWriter, r *http.Request, view ui.ChartView, readonly bool) {
	var err error
	if view.Worker == "" || len(view.Worker) > 16<<10 || !json.Valid(view.Props) {
		fail(w, 503)
		return
	}
	nonceBytes := make([]byte, 24)
	if _, err = rand.Read(nonceBytes); err != nil {
		fail(w, 500)
		return
	}
	nonce := base64.RawStdEncoding.EncodeToString(nonceBytes)
	display, _ := json.Marshal(struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Unit        string `json:"unit"`
	}{view.Title, view.Description, view.Unit})
	program, _ := json.Marshal(view.Worker)
	facts, _ := json.Marshal(struct {
		Name  string          `json:"name"`
		Props json.RawMessage `json:"props"`
	}{view.Name, view.Props})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'nonce-"+nonce+"'; worker-src blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	_, _ = fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Chart policy</title><script nonce="`+nonce+`">(`+chartPolicyBridge+`)(`+string(program)+`,`+string(facts)+`,`+string(display)+`,`+fmt.Sprint(readonly)+`);</script>`)
}
