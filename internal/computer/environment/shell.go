package environment

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/z-chenhao/assistant/internal/tool"
)

type ShellLimits struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	OutputBytes    int `json:"output_bytes"`
}

// ShellCommand carries only the original approved command and accepted smaller
// limits. Pure recipe implementations never receive this driver request.
type ShellCommand struct {
	Command string
	Limits  ShellLimits
}

//go:embed shell.py
var shellProgram string

func ShellProgram() (string, error) { return nativeDriverProgram(shellProgram) }

func ShellDriverFingerprint() (string, error) {
	program, err := ShellProgram()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("steward-shell-driver/1:%x", sha256.Sum256([]byte(program))), nil
}

func ShellRequest(root string, command ShellCommand) ([]byte, error) {
	return json.Marshal(struct {
		Root    string      `json:"root"`
		Command string      `json:"command"`
		Limits  ShellLimits `json:"limits"`
	}{root, command.Command, command.Limits})
}

func (l Local) ExecuteShell(ctx context.Context, command ShellCommand) tool.Result {
	if failure := l.prepareWorkspace(); failure.Error != nil {
		return failure
	}
	program, err := ShellProgram()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	request, err := ShellRequest(l.Config.Root, command)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	raw, failure := l.runProgram(ctx, program, request, false)
	if failure.Error != nil {
		return failure
	}
	return Response(raw)
}
