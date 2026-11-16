package webaccess

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

// Original web_fetch interpretation for pre-fetch-service Runs and explicit
// unbundled carriers. Remove only when those retained executions/references no
// longer need the exact original4096 preview; never a managed failure fallback.
func (p Plugin) fetchLegacy(ctx context.Context, args string) (tool.Result, error) {
	var a struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	fetch := p.Fetch
	if fetch == nil {
		fetch = webcontent.Fetch
	}
	page, e := fetch(ctx, a.URL)
	if e != nil {
		return tool.Failure(e.Error()), nil
	}
	full := page.Text
	if len(page.Text) > 4096 {
		n := 4096
		for !utf8.ValidString(page.Text[:n]) {
			n--
		}
		page.Text = page.Text[:n]
	}
	return tool.Result{Output: &tool.BinaryOutput{Name: "web-page.txt", MediaType: "text/plain", Reader: io.NopCloser(strings.NewReader(full)), ResultJSON: func(ref string) string {
		b, _ := json.Marshal(struct {
			webcontent.Page
			Artifact   string `json:"artifact_ref"`
			Bytes      int    `json:"total_bytes"`
			NextOffset int    `json:"next_offset"`
			More       bool   `json:"has_more"`
		}{page, ref, len(full), len(page.Text), len(full) > len(page.Text)})
		return string(b)
	}}}, nil
}
