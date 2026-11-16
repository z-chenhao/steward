package webaccess

import (
	"context"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const FetchContract = "steward.web-fetch/1"
const FetchPageContract = "steward.web-fetch-page/1"

type FetchPlan struct {
	TextBytes int    `json:"text_bytes"`
	ErrorCode string `json:"error_code"`
}

func fetchService(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input struct{}
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	var page PageReply
	if err := rsiplugin.CallResource(ctx, FetchPageContract, "page", struct{}{}, &page); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, FetchPlan{TextBytes: len(Preview(page.Page.Text, 4096)), ErrorCode: page.ErrorCode})
}
