package computeruse

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func browserUploadParameters(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	facts, args, err := readPlaywrightArguments(ctx, call)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, normalizeBrowserUpload(facts, args))
}

func normalizeBrowserUpload(facts contract.ActionFacts, args []any) contract.ActionParameters {
	if facts.Method != "playwright.upload" {
		return contract.ActionParameters{Failure: "NOT_FOUND"}
	}
	p := normalizePlaywright(facts, args)
	if p.Failure == "" {
		p.NameFromSource = true
		p.MediaType = "application/octet-stream"
	}
	return p
}
