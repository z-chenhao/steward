package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShippedPluginDistributionRejectsPartialNativeFallback(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "steward")
	if d, err := shippedPluginDirectories(executable); err != nil || d.DOCX != "" {
		t.Fatal("explicit unbundled embedder boundary changed", d, err)
	}
	plugins := filepath.Join(root, "plugins")
	names := []string{"docx-reader", "image-ocr", "trace-inspector", "context-compact", "recall", "conversation", "memory-curator", "evidence-reader", "skill-library", "experience-learning", "task-control", "task-progress", "task-feedback", "delegation", "codemode", "file-tools", "shell-tools", "coding-workspace", "computer-use", "web-access", "web-extract", "mcp-bridge", "connected-apps", "tool-discovery", "apps-directory", "apps-accounts", "apps-logos", "apps-interface", "github-pr-followup", "artifact-workbench", "intelligent-ui", "schedule", "memory-inspector", "model-connections", "feishu-chat", "capability-settings", "terminal-interface", "reference-pricing", "web-workbench", "service-watchdog"}
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(plugins, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plugins, name, "steward.plugin.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := shippedPluginDirectories(executable); err != nil || d.Computer != filepath.Join(plugins, "computer-use") || d.MCP != filepath.Join(plugins, "mcp-bridge") || d.Apps != filepath.Join(plugins, "connected-apps") || d.Discovery != filepath.Join(plugins, "tool-discovery") || d.AppsDirectory != filepath.Join(plugins, "apps-directory") || d.AppsAccounts != filepath.Join(plugins, "apps-accounts") || d.AppsLogos != filepath.Join(plugins, "apps-logos") || d.AppsInterface != filepath.Join(plugins, "apps-interface") || d.GitHub != filepath.Join(plugins, "github-pr-followup") || d.Artifact != filepath.Join(plugins, "artifact-workbench") || d.UI != filepath.Join(plugins, "intelligent-ui") || d.Schedule != filepath.Join(plugins, "schedule") || d.MemoryInspector != filepath.Join(plugins, "memory-inspector") || d.ModelConnections != filepath.Join(plugins, "model-connections") || d.Feishu != filepath.Join(plugins, "feishu-chat") || d.CapabilityForms != filepath.Join(plugins, "capability-settings") || d.Terminal != filepath.Join(plugins, "terminal-interface") || d.ReferencePricing != filepath.Join(plugins, "reference-pricing") || d.WebWorkbench != filepath.Join(plugins, "web-workbench") || d.ServiceWatchdog != filepath.Join(plugins, "service-watchdog") {
		t.Fatal("complete distribution not found", d, err)
	}
	for _, name := range []string{"docx-reader", "shell-tools", "coding-workspace", "computer-use", "web-access", "web-extract", "mcp-bridge", "connected-apps", "tool-discovery", "apps-directory", "apps-accounts", "apps-logos", "apps-interface", "github-pr-followup", "artifact-workbench", "intelligent-ui", "schedule", "memory-inspector", "model-connections", "feishu-chat", "capability-settings", "terminal-interface", "reference-pricing", "web-workbench", "service-watchdog"} {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(plugins, name, "steward.plugin.json")
			if err := os.Rename(manifest, manifest+".missing"); err != nil {
				t.Fatal(err)
			}
			defer os.Rename(manifest+".missing", manifest)
			if d, err := shippedPluginDirectories(executable); err == nil || d.DOCX != "" {
				t.Fatal("damaged distribution admitted a partial native composition", d, err)
			}
		})
	}
}
