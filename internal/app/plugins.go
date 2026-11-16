package app

import (
	"context"
	"sync"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/media/ocr"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/driver"
	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Plugins is an operator control port. It is never registered as a model tool.
func (a *App) Plugins() rsi.Host {
	var once sync.Once
	var computer *rsi.Compute
	load := func(ctx context.Context) *rsi.Compute {
		once.Do(func() { computer = driver.New(ocr.Discover(ctx)) })
		return computer
	}
	return rsi.Host{Catalog: rsi.NewCatalog(a.store.DataRoot()), Store: a.store.Plugins(), Compute: &rsi.Compute{
		Identity: func(ctx context.Context) (string, error) { return load(ctx).Identity(ctx) },
		Invoke: func(ctx context.Context, in rsiplugin.ComputeRequest, source rsi.InputData) (rsi.Computation, error) {
			return load(ctx).Invoke(ctx, in, source)
		},
	}}
}

func (a *App) WorkPlugins(ctx context.Context) (int, error) {
	return a.store.Plugins().Work(ctx, a.Plugins().Catalog)
}

func (a *App) PluginCalls(ctx context.Context, run string) ([]rsi.CallStatus, error) {
	return a.store.Plugins().Calls(ctx, run)
}
func (a *App) RetryPluginCall(ctx context.Context, run, instance, key string) error {
	return a.store.Plugins().RetryCall(ctx, run, instance, key)
}

func (a *App) RetryPluginEvent(ctx context.Context, id string) error {
	return a.store.Plugins().RetryEvent(ctx, id)
}

// CaptureWebParser is an owner control operation; config source authorization
// stays in app composition and no source path is sent to a candidate.
func (a *App) CaptureWebParser(ctx context.Context, cfg config.Config, module []byte) (webextract.Config, error) {
	source := webAccessConfig(cfg)
	resource, err := a.Plugins().CaptureWebParser(ctx, source.ExtractCommand, module)
	if err != nil {
		return webextract.Config{}, err
	}
	return webextract.Config{RuntimeRevision: resource.Digest(), SourceIdentity: source.ParserSourceIdentity()}, nil
}
