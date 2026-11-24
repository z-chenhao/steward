package app

import (
	"context"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
)

func (a *App) prepareBuiltinPlugins(ctx context.Context, cfg config.Config) error {
	a.pluginBootstrapMu.Lock()
	defer a.pluginBootstrapMu.Unlock()
	host := a.Plugins()
	status, err := host.Store.Status(ctx)
	if err != nil {
		return err
	}
	// Existing selections (including deactivation and stop) are independent of
	// the current distribution. EnsureDefault repeats this check before writing.
	if status.Epoch != 0 || status.Stopped {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	d, err := builtin.ShippedDirectories(executable)
	if err != nil || d.DOCX == "" {
		return err
	}
	value, err := prepareWebDefault(ctx, host, cfg, d.Extract)
	if err != nil {
		return err
	}
	return builtin.EnsureDefault(ctx, host, d, value)
}
