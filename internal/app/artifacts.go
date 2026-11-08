package app

import (
	"context"
	"io"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
)

// artifactStore is the local user control boundary consumed by App. Persistence
// and transaction-aware Tool operations remain private to the state owner.
type artifactStore interface {
	Put(context.Context, artifact.Scope, artifact.Input, io.Reader) (string, error)
	Open(context.Context, artifact.Scope, string) (io.ReadCloser, error)
	Describe(context.Context, artifact.Scope, string) (artifact.Descriptor, error)
	Delete(context.Context, artifact.Scope, string, int64) error
	Cleanup(context.Context, artifact.Scope, bool) (artifact.CleanupReport, error)
}

func (a *App) artifactOwner(ctx context.Context) (artifact.Scope, error) {
	d, e := a.store.Diagnostics(ctx)
	return artifact.Scope{UserID: a.store.OwnerID(), AgentID: d.AgentID, Owner: true}, e
}
func (a *App) SaveArtifact(ctx context.Context, in artifact.Input, src io.Reader) (artifact.Descriptor, error) {
	scope, err := a.artifactOwner(ctx)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	ref, err := a.artifacts.Put(ctx, scope, in, src)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return a.artifacts.Describe(ctx, scope, ref)
}
func (a *App) OpenArtifact(ctx context.Context, ref string) (io.ReadCloser, error) {
	scope, err := a.artifactOwner(ctx)
	if err != nil {
		return nil, err
	}
	return a.artifacts.Open(ctx, scope, ref)
}
func (a *App) DescribeArtifact(ctx context.Context, ref string) (artifact.Descriptor, error) {
	scope, err := a.artifactOwner(ctx)
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return a.artifacts.Describe(ctx, scope, ref)
}
func (a *App) DeleteArtifact(ctx context.Context, ref string, version int64) error {
	scope, err := a.artifactOwner(ctx)
	if err != nil {
		return err
	}
	return a.artifacts.Delete(ctx, scope, ref, version)
}
func (a *App) CleanupArtifacts(ctx context.Context, dry bool) (artifact.CleanupReport, error) {
	scope, err := a.artifactOwner(ctx)
	if err != nil {
		return artifact.CleanupReport{}, err
	}
	return a.artifacts.Cleanup(ctx, scope, dry)
}
func (a *App) SubmitParts(ctx context.Context, key string, parts []conversation.ContentPart) (agent.Receipt, error) {
	d, err := a.Diagnostics(ctx)
	if err != nil {
		return agent.Receipt{}, err
	}
	if !d.AgentIngressEnabled {
		return agent.Receipt{}, agent.ErrSetup
	}
	if key == "" || len(key) > 256 {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	return a.runtimeRuns().Submit(ctx, key, parts)
}
