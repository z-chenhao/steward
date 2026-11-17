// Package setup owns user configuration editing and explicit readiness probes.
// It is independent of Agent execution, model capability discovery and Run state.
package setup

import (
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/config"
	"go.yaml.in/yaml/v3"
)

type computerSettings struct {
	Computer   *macosvm.Config    `yaml:"computer"`
	JavaScript *config.JavaScript `yaml:"javascript"`
}

var kinds = []string{"memory", "embedding", "mcp", "skills", "execution", "computer"}

func resource(c config.Config, id string) any {
	if c.Automatic(id) {
		return map[string]string{"mode": "automatic"}
	}
	switch id {
	case "memory":
		if c.Memory != nil {
			return c.Memory
		}
	case "embedding":
		if c.Embedding != nil {
			return c.Embedding
		}
	case "mcp":
		if len(c.MCP) > 0 {
			return c.MCP
		}
	case "skills":
		if c.Skills != nil {
			return c.Skills
		}
	case "execution":
		if c.Computer == nil {
			return c.ExecutionConfig()
		}
	case "computer":
		if c.Computer != nil {
			return computerSettings{c.Computer, c.JavaScript}
		}
	}
	return nil
}
func encode(v any) string {
	if v == nil {
		return ""
	}
	raw, _ := yaml.Marshal(v)
	return string(raw)
}
