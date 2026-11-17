package environment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"github.com/z-chenhao/assistant/internal/tool"
)

// Local is a persistent private workspace inside the platform kernel sandbox.
// Nothing dispatches on the Host if the sandbox is absent or unsupported.
type Local struct{ Config Config }

// Check is a read-only dependency check. Namespace availability is proved when
// dispatching; a binary being installed does not imply permission to run it.
func Check() error {
	if _, err := os.Stat("/bin/bash"); err != nil {
		return err
	}
	if _, err := sandbox.Python3(context.Background()); err != nil {
		return err
	}
	return sandbox.Check()
}

func (l Local) Execute(ctx context.Context, name, args string) tool.Result {
	if failure := l.prepareWorkspace(); failure.Error != nil {
		return failure
	}
	request, err := Request(l.Config.Root, name, args)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	raw, failure := l.runProgram(ctx, Source, request, name == "builtin__read")
	if failure.Error != nil {
		return failure
	}
	return Response(raw)
}

func (l Local) prepareWorkspace() tool.Result {
	if l.Config.Validate() != nil {
		return tool.Failure("PERMISSION_DENIED")
	}
	root := l.Config.Root
	if os.MkdirAll(root, 0700) != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != filepath.Clean(root) {
		return tool.Failure("PERMISSION_DENIED")
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return tool.Failure("PERMISSION_DENIED")
	}

	return tool.Result{}
}

// Callers supply a fixed Host program. No plugin can choose executable
// source, interpreter, workspace, mounts, environment or toolchain roots.
func (l Local) runProgram(ctx context.Context, source string, request []byte, readOnly bool) ([]byte, tool.Result) {
	root := l.Config.Root
	cmd, err := sandbox.PythonCommand(ctx, []string{"-I", "-c", source}, l.Config.ToolchainRoots, root)
	if err != nil {
		return nil, tool.Failure("PROVIDER_UNAVAILABLE")
	}
	paths := []string{}
	for _, p := range l.Config.ToolchainRoots {
		paths = append(paths, filepath.Join(p, "bin"))
	}
	paths = append(paths, "/usr/bin", "/bin")
	cmd.Env = []string{"PATH=" + strings.Join(paths, ":"), "HOME=" + root, "LANG=C.UTF-8"}
	cmd.Dir, cmd.Stdin = root, bytes.NewReader(request)
	output := sandbox.NewOutput(12 << 20)
	cmd.Stdout, cmd.Stderr = output, nil
	started, err := sandbox.Run(cmd, syscall.SIGTERM)
	if !started {
		return nil, tool.Failure("PROVIDER_UNAVAILABLE")
	}
	if err != nil || output.Overflow() {
		// The transport may have executed before it died; never dispatch again.
		if !readOnly {
			return nil, tool.Failure("UNKNOWN_SIDE_EFFECT")
		}
		return nil, tool.Failure("PROVIDER_UNAVAILABLE")
	}
	return output.Bytes(), tool.Result{}
}

func Request(root, name, arguments string) ([]byte, error) {
	if !IsTool(name) || !json.Valid([]byte(arguments)) {
		return nil, os.ErrInvalid
	}
	return json.Marshal(map[string]any{"root": root, "method": strings.TrimPrefix(name, "builtin__"), "args": json.RawMessage(arguments)})
}

func Response(raw []byte) tool.Result {
	var result struct {
		Value json.RawMessage `json:"value"`
		Error string          `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || (result.Error == "" && len(result.Value) == 0) {
		return tool.Failure("UNKNOWN_SIDE_EFFECT")
	}
	if result.Error != "" {
		return tool.Normalize(tool.Failure(result.Error))
	}
	var file struct {
		Path string  `json:"path"`
		Data *string `json:"artifact_data"`
	}
	if json.Unmarshal(result.Value, &file) == nil && file.Data != nil {
		data, err := base64.StdEncoding.DecodeString(*file.Data)
		if err != nil || len(data) > 8<<20 {
			return tool.Failure("RESOURCE_LIMIT")
		}
		return tool.Result{Output: &tool.BinaryOutput{Name: filepath.Base(file.Path), MediaType: "application/octet-stream", Reader: io.NopCloser(bytes.NewReader(data)), ResultJSON: func(ref string) string {
			raw, _ := json.Marshal(map[string]string{"path": file.Path, "artifactRef": ref})
			return string(raw)
		}}}
	}
	return tool.Result{Text: string(result.Value)}
}
