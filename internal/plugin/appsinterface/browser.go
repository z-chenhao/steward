package appsinterface

import (
	"context"
	_ "embed"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// These bytes are part of the sealed owning package, not the shared Web build.
//
//go:embed view/browser-worker.js
var browserWorker string

//go:embed view/apps.css
var browserCSS string

func compileBrowserProfile(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	locale, err := presentationLocale(call)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.BrowserProfile{Profile: localizedProfile(locale), View: contract.BrowserView{Worker: browserWorker, CSS: browserCSS}})
}
