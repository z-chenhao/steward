package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// PrepareModelShortcut binds CLI identity facts to the current owning recipe.
// Explicit metadata JSON is operator authority and never enters this default path.
func (a *App) PrepareModelShortcut(ctx context.Context, input llm.ProfileInput) (llm.ProfileInput, error) {
	if err := input.ValidateIdentity(); err != nil {
		return input, err
	}
	if a.prepareFeaturePlugins != nil {
		if err := a.prepareFeaturePlugins(ctx); err != nil {
			return input, err
		}
	}
	return a.prepareModelShortcutWithHost(ctx, a.Plugins(), input)
}
func (a *App) prepareModelShortcutWithHost(ctx context.Context, host rsi.Host, input llm.ProfileInput) (llm.ProfileInput, error) {
	facts := contract.ConnectionFacts{Preset: contract.CLIShorthand}
	defaults, managed, err := a.readModelConnectionWithHost(ctx, host, facts)
	if err != nil {
		return input, err
	}
	if !managed {
		defaults = legacyv1.Connection(facts)
	}
	if defaults.EndpointOverride != "" && defaults.EndpointOverride != input.Endpoint {
		return input, llm.ErrConflict
	}
	input.WireAPI = defaults.WireAPI
	input.ContextWindow, input.MaxOutputTokens = defaults.ContextWindow, defaults.MaxOutputTokens
	input.Compatibility = llm.Compatibility{ToolResultFormat: "openai"}
	input.Capabilities = llm.Capabilities{TextInput: true, ImageInput: defaults.ImageInput}
	return input, input.Validate()
}
