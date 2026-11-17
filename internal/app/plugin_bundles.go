package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
)

func (a *App) prepareBuiltinPlugins(ctx context.Context, cfg config.Config) error {
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
	d, err := shippedPluginDirectories(executable)
	if err != nil || d.DOCX == "" {
		return err
	}
	value, err := prepareWebDefault(ctx, host, cfg, d.Extract)
	if err != nil {
		return err
	}
	return builtin.EnsureDefault(ctx, host, d, value)
}

func shippedPluginDirectories(executable string) (builtin.Directories, error) {
	d := builtin.Directories{}
	// Embedders without the distributed bundle keep their explicit native boundary.
	root := filepath.Join(filepath.Dir(executable), "plugins")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return d, nil
	} else if err != nil {
		return d, err
	}
	for _, p := range []struct {
		Name        string
		Destination *string
	}{
		{"docx-reader", &d.DOCX}, {"image-ocr", &d.OCR}, {"trace-inspector", &d.Trace}, {"context-compact", &d.Context}, {"recall", &d.Recall}, {"conversation", &d.Conversation}, {"memory-curator", &d.Curator}, {"evidence-reader", &d.Evidence}, {"skill-library", &d.Skills}, {"experience-learning", &d.Experience}, {"task-control", &d.Task}, {"task-progress", &d.Progress}, {"task-feedback", &d.Feedback}, {"delegation", &d.Delegation}, {"codemode", &d.CodeMode}, {"file-tools", &d.Files}, {"shell-tools", &d.Shell}, {"coding-workspace", &d.Coding}, {"computer-use", &d.Computer}, {"web-access", &d.Web}, {"web-extract", &d.Extract}, {"mcp-bridge", &d.MCP}, {"connected-apps", &d.Apps}, {"tool-discovery", &d.Discovery}, {"apps-directory", &d.AppsDirectory}, {"apps-accounts", &d.AppsAccounts}, {"apps-logos", &d.AppsLogos}, {"apps-interface", &d.AppsInterface}, {"github-pr-followup", &d.GitHub}, {"artifact-workbench", &d.Artifact}, {"intelligent-ui", &d.UI}, {"schedule", &d.Schedule}, {"memory-inspector", &d.MemoryInspector}, {"model-connections", &d.ModelConnections}, {"feishu-chat", &d.Feishu}, {"capability-settings", &d.CapabilityForms}, {"terminal-interface", &d.Terminal}, {"reference-pricing", &d.ReferencePricing}, {"web-workbench", &d.WebWorkbench}, {"service-watchdog", &d.ServiceWatchdog},
	} {
		directory := filepath.Join(root, p.Name)
		manifest, err := os.Stat(filepath.Join(directory, "steward.plugin.json"))
		if err != nil {
			return builtin.Directories{}, fmt.Errorf("incomplete feature distribution (%s): %w", p.Name, err)
		}
		if !manifest.Mode().IsRegular() {
			return builtin.Directories{}, fmt.Errorf("invalid feature manifest (%s)", p.Name)
		}
		*p.Destination = directory
	}
	return d, nil
}
