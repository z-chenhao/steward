package setup

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/recall/embedding"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"github.com/z-chenhao/assistant/internal/tool/mcp"
)

func (m *Manager) probe(ctx context.Context, c config.Config, id string) probeResult {
	if c.Automatic(id) {
		return probeResult{State: "ready", Code: "ProbeAutomatic"}
	}
	if id == "execution" && c.Computer != nil {
		return probeResult{State: "unavailable", Code: "ProbeExecutionConflict"}
	}
	if resource(c, id) == nil {
		return probeResult{State: "ready", Code: "ProbeDisabled"}
	}
	switch id {
	case "memory":
		if _, e := (memory.Vault{Secrets: m.secrets, Ref: c.Memory.EncryptionKeyRef}).Digest(ctx, "Steward memory connection test"); e != nil {
			return probeResult{State: "unavailable", Code: "ProbeMemoryFailed"}
		}
		return probeResult{State: "ready", Code: "ProbeMemoryReady"}
	case "embedding":
		v, e := (embedding.Client{Config: *c.Embedding, Secrets: m.secrets}).Embed(ctx, "Steward embedding connection test")
		if e != nil {
			return probeResult{State: "unavailable", Code: "ProbeEmbeddingFailed"}
		}
		return probeResult{State: "ready", Code: "ProbeEmbeddingReady", Dimensions: len(v)}
	case "skills":
		defs, e := skill.Definitions(*c.Skills)
		if e != nil {
			return probeResult{State: "unavailable", Code: "ProbeSkillsFailed"}
		}
		return probeResult{State: "ready", Code: "ProbeSkillsReady", Tools: len(defs)}
	case "mcp":
		total := 0
		for _, server := range c.MCP {
			defs, e := mcp.Definitions(ctx, server)
			if e != nil {
				return probeResult{State: "unavailable", Code: "ProbeMCPFailed"}
			}
			total += len(defs)
		}
		return probeResult{State: "ready", Code: "ProbeMCPReady", Providers: len(c.MCP), Tools: total}
	case "execution":
		r := (environment.Local{Config: c.ExecutionConfig()}).Execute(ctx, "builtin__bash", `{"command":"printf steward-connection-test","timeout":5}`)
		if r.Error != nil {
			return probeResult{State: "unavailable", Code: "ProbeExecutionFailed"}
		}
		var result struct {
			ExitCode int    `json:"exit_code"`
			Text     string `json:"output"`
		}
		if json.Unmarshal([]byte(r.ModelText()), &result) != nil || result.ExitCode != 0 || result.Text != "steward-connection-test" {
			return probeResult{State: "unavailable", Code: "ProbeExecutionInvalid"}
		}
		return probeResult{State: "ready", Code: "ProbeExecutionReady"}
	case "computer":
		if c.JavaScript == nil {
			return probeResult{State: "unavailable", Code: "ProbeJavaScriptMissing"}
		}
		if e := (&codemode.Sidecar{Command: c.JavaScript.Command, Script: c.JavaScript.Script}).Check(ctx); e != nil {
			return probeResult{State: "unavailable", Code: "ProbeJavaScriptFailed"}
		}
		p, e := macosvm.New(*c.Computer, macosvm.SSHGuest{Config: c.Computer.Guest, Secrets: m.secrets})
		if e != nil {
			return probeResult{State: "unavailable", Code: "ProbeComputerStoreFailed"}
		}
		defer p.Close()
		if e = p.Probe(ctx); e != nil {
			return probeResult{State: "unavailable", Code: "ProbeComputerFailed"}
		}
		return probeResult{State: "ready", Code: "ProbeComputerReady"}
	}
	return probeResult{State: "unavailable", Code: "ProbeUnknown"}
}

type probeResult struct {
	State, Code                  string
	Dimensions, Providers, Tools int
}
