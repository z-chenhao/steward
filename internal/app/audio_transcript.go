package app

import (
	"context"

	agentsqlite "github.com/z-chenhao/assistant/internal/agent/sqlite"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func (a *App) EnableAudioTranscript(ctx context.Context, source string) (rsi.Status, error) {
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return rsi.Status{}, err
		}
	}
	return a.store.EnableAudioTranscript(ctx, a.Plugins(), source)
}
func (a *App) TranscribeLocalAudio(ctx context.Context, ref, model string) (agentsqlite.AudioTranscript, error) {
	return a.runtimeRuns().TranscribeLocalAudio(ctx, a.Plugins(), ref, model)
}
