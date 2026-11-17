package rsi

import (
	"context"
	"github.com/z-chenhao/assistant/internal/computer/sandbox"
	"os/exec"
)

func isolatedCommand(ctx context.Context, launcher, entry, root, work string) (*exec.Cmd, func(), error) {
	return sandbox.PureCommand(ctx, launcher, []string{"-rsi-plugin-exec", entry}, entry, root, work)
}
