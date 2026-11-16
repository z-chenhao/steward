package agent

import (
	"context"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// PluginHooks is an optional consumer port. Loop never loads plugin code or
// accepts persistence/authorization decisions from a hook implementation.
type PluginHooks interface {
	PluginHook(context.Context, RunClaim, rsiplugin.HookInput) (rsiplugin.HookOutput, error)
}
type RunRegistry interface {
	RegistryForRun(context.Context, RunClaim, *tool.Registry) (*tool.Registry, error)
}

func (l *agentLoop) pluginHook(run Run, stage rsiplugin.Stage, text, reference string) (rsiplugin.HookOutput, error) {
	p, ok := l.options.Store.(PluginHooks)
	if !ok {
		return rsiplugin.HookOutput{}, nil
	}
	return p.PluginHook(l.execution, l.claim, rsiplugin.HookInput{Stage: stage, Execution: rsiplugin.Execution{RunID: run.ID, Role: string(run.Kind), Step: run.Checkpoint.Step}, Text: text, Reference: reference})
}
func (l *agentLoop) projectTool(run Run, callID string, result tool.Result) (tool.Result, error) {
	out, err := l.pluginHook(run, rsiplugin.ProjectToolResult, result.ModelText(), callID)
	if err != nil {
		return result, err
	}
	// Unknown effects retain their complete original representation. Projection
	// never changes the stored result, errors, loading receipts or evidence refs.
	if out.Text != nil && result.Error == nil {
		result.Text = *out.Text
	}
	return result, nil
}
