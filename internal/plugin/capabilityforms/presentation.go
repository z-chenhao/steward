package capabilityforms

import (
	"fmt"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"path/filepath"
	"strings"
)

func ProjectPresentation(f contract.PresentationFacts) contract.Presentation {
	out := contract.Presentation{Descriptions: []contract.Description{}, Copy: contract.Copy{
		Automatic:                "Steward manages the default. You can choose custom settings or turn it off.",
		MemoryKeyUnavailable:     "The saved Memory key is unavailable in local credential files. Restore the original key before restarting. Existing keys are never replaced.",
		ExecutionManaged:         "Files and commands use the Computer workspace. Open Computer to verify its connection.",
		VerificationExpired:      "Verification expired. Test this candidate again before saving.",
		ProbeAutomatic:           "Steward will manage this capability automatically. Save and restart to apply the default.",
		ProbeExecutionConflict:   "File tools use Computer. Configure and test that backend instead.",
		ProbeDisabled:            "This candidate disables the capability. Save and restart to apply it; existing work keeps its original capabilities.",
		ProbeMemoryFailed:        "Memory key unavailable. Provide the referenced 32-byte key in SecretStore or the process environment.",
		ProbeMemoryReady:         "The referenced 32-byte encryption key is available. No memory was read or written.",
		ProbeEmbeddingFailed:     "Embedding connection failed. Check the endpoint, model, dimensions and credential reference; redirects and invalid vectors are rejected.",
		ProbeEmbeddingReady:      "Embedding endpoint returned a valid {{dimensions}}-dimensional vector for a fixed diagnostic sentence.",
		ProbeSkillsFailed:        "Skill catalog unavailable. Prepare a readable catalog with valid SKILL.md files and bounded resources at the configured root.",
		ProbeSkillsReady:         "The actual Skill catalog and its resources passed validation ({{tools}} discovery/read tools).",
		ProbeMCPFailed:           "MCP discovery failed. Check absolute executable/private directory paths, the kernel sandbox, protocol and allowlisted tool names.",
		ProbeMCPReady:            "{{providers}} isolated provider(s) advertised {{tools}} allowed tools. No advertised tool was invoked.",
		ProbeExecutionFailed:     "Workspace diagnostic failed. Check private directory permissions, Python 3, bash, kernel sandbox permissions and read-only toolchain paths.",
		ProbeExecutionInvalid:    "The isolated diagnostic command did not complete successfully.",
		ProbeExecutionReady:      "A fixed diagnostic command completed in the configured kernel sandbox. No user command or network access was used.",
		ProbeJavaScriptMissing:   "Computer requires the JavaScript runtime configuration.",
		ProbeJavaScriptFailed:    "JavaScript runtime unavailable. Check the absolute Node/sidecar paths and install the pinned runtime dependencies.",
		ProbeComputerStoreFailed: "Computer store unavailable. Provision the private Lume store and sealed base before testing.",
		ProbeComputerFailed:      "Computer connection failed. Check the Lume endpoint/version, sealed base compatibility/integrity and Guest credential reference. The diagnostic does not boot or modify a VM.",
		ProbeComputerReady:       "Lume version, sealed base and credential availability verified. Guest runtime identity is checked when a Run first opens its Computer.",
		ProbeUnknown:             "Capability not available.",
	}}
	for _, id := range []string{"memory", "embedding", "mcp", "skills", "execution", "computer"} {
		out.Descriptions = append(out.Descriptions, describe(id, f.Root))
	}
	return out
}
func describe(id, root string) contract.Description {
	v := contract.Description{ID: id}
	parent := filepath.Dir(root)
	switch id {
	case "memory":
		v.Title = "Memory"
		v.Description = "Remember useful details across conversations."
		v.Help = "Automatic mode stores a private encryption key in Steward’s local credential files. Custom mode uses your existing 32-byte key reference. Keep the original key for existing memories and backups."
		v.Example = "encryption_key_ref: env:STEWARD_MEMORY_KEY\n"
	case "embedding":
		v.MemoryChild = true
		v.Title = "Semantic search"
		v.Description = "Choose how Memory finds relevant information."
		v.Help = "Automatic mode uses local text search without a service. Custom mode connects your embedding model; encrypted memory requires a literal loopback endpoint. Changing an existing vector identity requires an offline index rebuild."
		v.Example = "provider: openai_compatible\nendpoint: http://127.0.0.1:11434/v1\nmodel: nomic-embed-text\ndimensions: 768\n"
	case "mcp":
		v.Title = "MCP tools"
		v.Description = "Connect allowlisted tools from isolated local stdio providers."
		v.Help = "Use absolute executable and private state paths. Testing starts the configured providers in the existing kernel sandbox and checks tool discovery; it never invokes an advertised tool. MCP tools always run through Code Mode; no exposure modes or per-tool exposure settings are needed. Credentials in command arguments are not supported."
		v.Example = fmt.Sprintf("- name: local_catalog\n  command: /absolute/path/to/mcp-server\n  args: []\n  directory: %s\n  tools: {lookup: read_only}\n", filepath.Join(parent, "steward-mcp"))
	case "skills":
		v.Title = "Skills"
		v.Description = "Discover curated, versioned methods and their resources."
		v.Help = "Prepare the catalog outside application data and execution workspaces. Testing validates the actual catalog and its bounded resources. An empty valid catalog is allowed."
		v.Example = fmt.Sprintf("root: %s\nscope: agent\n", filepath.Join(parent, "steward-skills"))
	case "execution":
		v.Title = "Files and commands"
		v.Description = "Read, edit, write and run commands in a private isolated workspace."
		v.Help = "Use a separate private directory and optional read-only toolchains. Testing creates the workspace if needed and runs only a fixed diagnostic command in the kernel sandbox. Disable Computer first to select this backend. An empty setting restores the default local workspace."
		v.Example = fmt.Sprintf("root: %s\ntoolchain_roots: []\n", root+".computer")
	case "computer":
		v.Title = "Computer"
		v.Description = "Use a private macOS desktop, prepared automatically on first use."
		v.Help = "Automatic mode downloads and prepares a private macOS desktop on first use on Apple silicon. Custom mode connects your provisioned Lume store and sealed base. Custom testing checks the control plane without booting a VM."
		v.Example = "root: /absolute/private/computer-store\nendpoint: http://127.0.0.1:7777\ncompatibility:\n  provider_version: '0.6.1'\n  guest_build: YOUR_BUILD\n  driver_version: YOUR_DRIVER_VERSION\n  driver_sha256: YOUR_DRIVER_SHA256\n  driver_team: YOUR_DRIVER_TEAM\n  driver_bundle: com.trycua.driver\n  architecture: arm64\nguest:\n  user: lume\n  port: 22\n  password_ref: env:STEWARD_GUEST_PASSWORD\n  host_key_sha256: SHA256:YOUR_PINNED_HOST_KEY\nmanifest:\n  version: 1\n"
	}
	if id == "computer" {
		raw := v.Example
		lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
		for i := range lines {
			lines[i] = "  " + lines[i]
		}
		v.Example = "computer:\n" + strings.Join(lines, "\n") + "\njavascript:\n  command: /absolute/path/to/node\n  script: /absolute/path/to/runtime/javascript/src/sidecar.mjs\n"
	}
	return v
}
