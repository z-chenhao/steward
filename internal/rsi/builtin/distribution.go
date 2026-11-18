package builtin

import (
	"fmt"
	"os"
	"path/filepath"

	artifact "github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	capabilityforms "github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	compact "github.com/z-chenhao/assistant/internal/plugin/contextcompact/contract"
	docx "github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	feishuchat "github.com/z-chenhao/assistant/internal/plugin/feishuchat/contract"
	github "github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
	ocr "github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	ui "github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	memoryinspector "github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	modelconnections "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	referencepricing "github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
	schedule "github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	servicewatchdog "github.com/z-chenhao/assistant/internal/plugin/servicewatchdog/contract"
	terminalinterface "github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	discovery "github.com/z-chenhao/assistant/internal/plugin/tooldiscovery/contract"
	webworkbench "github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
)

type bundledPlugin struct {
	Name, ID  string
	Directory *string
}

func bundledPlugins(d *Directories) []bundledPlugin {
	return []bundledPlugin{
		{"docx-reader", docx.PluginID, &d.DOCX},
		{"image-ocr", ocr.PluginID, &d.OCR},
		{"trace-inspector", "steward.trace-inspector", &d.Trace},
		{"context-compact", compact.PluginID, &d.Context},
		{"recall", "steward.recall", &d.Recall},
		{"conversation", "steward.conversation", &d.Conversation},
		{"memory-curator", "steward.memory-curator", &d.Curator},
		{"evidence-reader", "steward.evidence-reader", &d.Evidence},
		{"skill-library", "steward.skill-library", &d.Skills},
		{"meeting-preparation", "steward.meeting-preparation", &d.MeetingPreparation},
		{"experience-learning", "steward.experience-learning", &d.Experience},
		{"task-control", "steward.task-control", &d.Task},
		{"task-progress", "steward.task-progress", &d.Progress},
		{"task-feedback", "steward.task-feedback", &d.Feedback},
		{"delegation", "steward.delegation", &d.Delegation},
		{"codemode", "steward.codemode", &d.CodeMode},
		{"file-tools", "steward.file-tools", &d.Files},
		{"shell-tools", "steward.shell-tools", &d.Shell},
		{"coding-workspace", "steward.coding-workspace", &d.Coding},
		{"computer-use", "steward.computer-use", &d.Computer},
		{"web-access", "steward.web-access", &d.Web},
		{"web-extract", "steward.web-extract", &d.Extract},
		{"mcp-bridge", "steward.mcp-bridge", &d.MCP},
		{"connected-apps", "steward.connected-apps", &d.Apps},
		{"tool-discovery", discovery.PluginID, &d.Discovery},
		{"apps-directory", "steward.apps-directory", &d.AppsDirectory},
		{"apps-accounts", "steward.apps-accounts", &d.AppsAccounts},
		{"apps-logos", "steward.apps-logos", &d.AppsLogos},
		{"apps-interface", "steward.apps-interface", &d.AppsInterface},
		{"github-pr-followup", github.PluginID, &d.GitHub},
		{"artifact-workbench", artifact.PluginID, &d.Artifact},
		{"intelligent-ui", ui.PluginID, &d.UI},
		{"schedule", schedule.PluginID, &d.Schedule},
		{"memory-inspector", memoryinspector.PluginID, &d.MemoryInspector},
		{"model-connections", modelconnections.PluginID, &d.ModelConnections},
		{"feishu-chat", feishuchat.PluginID, &d.Feishu},
		{"capability-settings", capabilityforms.PluginID, &d.CapabilityForms},
		{"terminal-interface", terminalinterface.PluginID, &d.Terminal},
		{"reference-pricing", referencepricing.PluginID, &d.ReferencePricing},
		{"web-workbench", webworkbench.PluginID, &d.WebWorkbench},
		{"service-watchdog", servicewatchdog.PluginID, &d.ServiceWatchdog},
	}
}

func BundledPluginNames() []string {
	entries := bundledPlugins(&Directories{})
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func ShippedDirectories(executable string) (Directories, error) {
	var directories Directories
	root := filepath.Join(filepath.Dir(executable), "plugins")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return directories, nil
	} else if err != nil {
		return directories, err
	}
	for _, entry := range bundledPlugins(&directories) {
		directory := filepath.Join(root, entry.Name)
		manifest, err := os.Stat(filepath.Join(directory, "steward.plugin.json"))
		if err != nil {
			return Directories{}, fmt.Errorf("incomplete feature distribution (%s): %w", entry.Name, err)
		}
		if !manifest.Mode().IsRegular() {
			return Directories{}, fmt.Errorf("invalid feature manifest (%s)", entry.Name)
		}
		*entry.Directory = directory
	}
	return directories, nil
}
