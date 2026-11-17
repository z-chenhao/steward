package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/webhtml"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var errWebHTMLMethod = errors.New("managed HTML decoder unavailable")

// Two real consumers: original web_fetch and the no-script HTML path in
// fetch_content. Network authorization, source identity and publication remain
// with those accepted operations. The decoder receives only original HTML bytes.
func (t *tools) webHTMLContext(ctx context.Context, h rsi.Host, revision string, check func(context.Context) error) (context.Context, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), webhtml.Contract)
	if err != nil || !managed {
		return ctx, err
	}
	return webcontent.WithHTMLParser(ctx, func(c context.Context, data []byte) (string, string, error) {
		var output webhtml.Output
		var digest string
		written := false
		port := rsi.Resource{Contract: webhtml.OutputContract,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"text":{"type":"string"}},"required":["title","text"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
				if key != "decoded" || written || rsiplugin.DecodeResource(raw, &output) != nil || len(output.Title)+len(output.Text) > 2*rsiplugin.MaxInputBytes || !utf8.Valid(output.Title) || !utf8.Valid(output.Text) {
					return nil, rsi.ErrInvalid
				}
				written = true
				digest = rsiplugin.FileDigest(raw)
				return json.RawMessage(`{}`), nil
			}}
		raw, err := h.DriveService(c, revision, webhtml.Contract, json.RawMessage(`{}`), []rsi.InputData{{ID: "html", Data: data}}, check, []rsi.Resource{port})
		t.runs.host.boundary("after_web_html_method")
		if err == nil {
			err = check(c)
		}
		if err == nil {
			err = h.Store.CheckGraph(c, revision)
		}
		var plan webhtml.Plan
		if err == nil && (!written || rsiplugin.DecodeResult(raw, &plan) != nil || plan.Digest != digest || plan.TitleBytes != len(output.Title) || plan.TextBytes != len(output.Text)) {
			err = rsi.ErrInvalid
		}
		if err != nil {
			return "", "", errors.Join(errWebHTMLMethod, err)
		}
		return string(output.Title), string(output.Text), nil
	}), nil
}
