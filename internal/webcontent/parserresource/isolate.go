package parserresource

import (
	"context"
	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"os/exec"
	"path/filepath"
)

func isolated(ctx context.Context, launcher, root, work string) (*exec.Cmd, func(), error) {
	return sandbox.PureCommand(ctx, launcher, []string{"-web-parser-exec", root}, filepath.Join(root, "node"), root, work)
}
