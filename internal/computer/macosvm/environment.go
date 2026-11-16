package macosvm

import (
	"bytes"
	"context"
	"time"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/tool"
)

// WorkingDirectory describes the Guest workspace without starting a Computer
// or exposing Host paths and connection configuration.
func (p *Provider) WorkingDirectory() string {
	if p.managed != nil {
		return "/Users/" + managedGuestUser + "/StewardWorkspace"
	}
	return "/Users/" + p.config.Guest.User + "/StewardWorkspace"
}

func (p *Provider) EnvironmentResourceHash() string { return environment.ResourceHash(p.config) }

// ExecuteEnvironment runs only in the authenticated, epoch-bound Guest. Its
// working files are visible to that same Computer's apps and browser.
func (p *Provider) ExecuteEnvironment(ctx context.Context, instance computer.Instance, name, arguments string) tool.Result {
	g, ip, err := p.useGuest(ctx, instance)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	root := "/Users/" + g.Config.User + "/StewardWorkspace"
	request, err := environment.Request(root, name, arguments)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	// No credentials or model text are interpolated into shell source.
	output, err := g.commandInputFor(ctx, ip, "/usr/bin/python3 -I -c "+quote(environment.Source), false, bytes.NewReader(request), 300*time.Second)
	if err != nil {
		if name != "builtin__read" {
			return tool.Failure("UNKNOWN_SIDE_EFFECT")
		}
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	return environment.Response([]byte(output))
}

// SnapshotEnvironmentFile reads through the same authenticated Guest and
// instance epoch as native file tools. It exposes neither SSH nor a Host path
// to feature implementations; no new script-callable Tool is registered.
func (p *Provider) SnapshotEnvironmentFile(ctx context.Context, instance computer.Instance, path string) (environment.FileSnapshot, *tool.Error) {
	g, ip, err := p.useGuest(ctx, instance)
	if err != nil {
		return environment.FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	root := "/Users/" + g.Config.User + "/StewardWorkspace"
	request, err := environment.FileSnapshotRequest(root, path)
	if err != nil {
		return environment.FileSnapshot{}, tool.Failure("INVALID_ARGUMENT").Error
	}
	program, err := environment.FileSnapshotProgram()
	if err != nil {
		return environment.FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	output, err := g.commandInputFor(ctx, ip, "/usr/bin/python3 -I -c "+quote(program), false, bytes.NewReader(request), 300*time.Second)
	if err != nil {
		return environment.FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	return environment.DecodeFileSnapshot([]byte(output))
}

func (p *Provider) ApplyEnvironmentFile(ctx context.Context, instance computer.Instance, change environment.FileChange) tool.Result {
	g, ip, err := p.useGuest(ctx, instance)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	root := "/Users/" + g.Config.User + "/StewardWorkspace"
	request, err := environment.FileChangeRequest(root, change)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	program, err := environment.FileChangeProgram()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	output, err := g.commandInputFor(ctx, ip, "/usr/bin/python3 -I -c "+quote(program), false, bytes.NewReader(request), 300*time.Second)
	if err != nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	return environment.Response([]byte(output))
}

func (p *Provider) ExecuteEnvironmentShell(ctx context.Context, instance computer.Instance, command environment.ShellCommand) tool.Result {
	g, ip, err := p.useGuest(ctx, instance)
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	root := "/Users/" + g.Config.User + "/StewardWorkspace"
	request, err := environment.ShellRequest(root, command)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	program, err := environment.ShellProgram()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	output, err := g.commandInputFor(ctx, ip, "/usr/bin/python3 -I -c "+quote(program), false, bytes.NewReader(request), 300*time.Second)
	if err != nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	return environment.Response([]byte(output))
}
