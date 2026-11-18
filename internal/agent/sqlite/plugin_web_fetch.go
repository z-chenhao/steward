package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (t *tools) executeWebFetch(ctx context.Context, inv tool.Invocation, run agent.Run, args string) (tool.Result, error) {
	var input struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(args), &input) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if t.runs.plugins == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return tool.Result{}, err
	}
	if revision == "" {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	check := func(c context.Context) error {
		return t.runs.evidenceReadTx(c, func(tx *sql.Tx) error {
			_, name, original, e := t.acceptedBuiltin(c, tx, inv)
			if e != nil {
				return e
			}
			if name != webaccess.FetchName || original != args {
				return tool.ErrApprovalDenied
			}
			if e = t.runs.pluginDispatchFence(c, tx, t.claim.RunID()); e != nil {
				return e
			}
			if t.runs.webResourceIdentity(run, name) != runWebResource(run, name) {
				return rsi.ErrRevoked
			}
			return nil
		})
	}
	ctx, err = t.webHTMLContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	ctx, err = t.webRouteContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	var original webaccess.Page
	var code string
	read := false
	port := rsi.Resource{Contract: webaccess.FetchPageContract, InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), OutputSchema: webPageReplySchema(), Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
		var empty struct{}
		if key != "page" || rsiplugin.DecodeResource(raw, &empty) != nil || read {
			return nil, tool.ErrApprovalDenied
		}
		if err := check(c); err != nil {
			return nil, err
		}
		if t.runs.webFetch == nil {
			return nil, rsi.ErrNotFound
		}
		page, e := t.runs.webFetch(c, input.URL)
		if errors.Is(e, webcontent.ErrRouteUnavailable) || errors.Is(e, errWebHTMLMethod) {
			return nil, e
		}
		original = page
		read = true
		code = webPageErrorCode(e)
		value := page
		value.Image = nil
		return json.Marshal(webaccess.PageReply{Page: value, ErrorCode: code})
	}}
	raw, err := h.DriveService(ctx, revision, webaccess.FetchContract, json.RawMessage(`{}`), nil, check, []rsi.Resource{port})
	t.runs.host.boundary("after_web_fetch_method")
	if e := t.runs.evidenceReadTx(context.WithoutCancel(ctx), func(tx *sql.Tx) error { return t.fence(context.WithoutCancel(ctx), tx) }); e != nil {
		return tool.Result{}, e
	}
	// Public page GET is the original safe operation: this port cannot spend a
	// model budget. The broker's conservative Unknown is not a fee/effect fact.
	if err != nil || !read {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	var plan webaccess.FetchPlan
	if rsiplugin.DecodeResult(raw, &plan) != nil || plan.ErrorCode != code {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if code != "" {
		return tool.Failure(code), nil
	}
	preview, ok := webOriginalRange(original.Text, 0, plan.TextBytes)
	if !ok {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	page := original
	page.Text = preview
	return tool.Result{HostFence: webPublicationReceipt{Name: webaccess.FetchName, Revision: revision}, Output: &tool.BinaryOutput{Name: "web-page.txt", MediaType: "text/plain", Reader: strings.NewReader(original.Text), ResultJSON: func(ref string) string {
		value := struct {
			webaccess.Page
			Artifact   string `json:"artifact_ref"`
			Bytes      int    `json:"total_bytes"`
			NextOffset int    `json:"next_offset"`
			More       bool   `json:"has_more"`
		}{page, ref, len(original.Text), plan.TextBytes, plan.TextBytes < len(original.Text)}
		b, _ := json.Marshal(value)
		return string(b)
	}}}, nil
}
