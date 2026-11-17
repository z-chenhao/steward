// Package environment owns the four file/command tools and their isolated
// execution protocol. The Host supplies the environment; arguments cannot choose
// a backend, mount, credential, or Host working directory.
package environment

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/tool"
)

//go:embed operations.py
var Source string

type Config struct {
	Root             string `yaml:"root"`
	RepositorySource string `yaml:"repository_source,omitempty" json:"RepositorySource,omitempty"`
	// Optional operator-installed compilers/interpreters, mounted read-only.
	ToolchainRoots []string `yaml:"toolchain_roots"`
}

func (c Config) Validate() error {
	if !filepath.IsAbs(c.Root) || filepath.Clean(c.Root) == "/" {
		return computer.ErrInvalid
	}
	if c.RepositorySource != "" && (!filepath.IsAbs(c.RepositorySource) || filepath.Clean(c.RepositorySource) == "/") {
		return computer.ErrInvalid
	}
	for _, p := range c.ToolchainRoots {
		if !filepath.IsAbs(p) || filepath.Clean(p) == "/" {
			return computer.ErrInvalid
		}
	}
	return nil
}

// GuestDriver uses the very same instance as the desktop/browser adapter.
type GuestDriver interface {
	ExecuteEnvironment(context.Context, computer.Instance, string, string) tool.Result
	WorkingDirectory() string
}

func ResourceHash(configuration any) string {
	hash, _ := computer.Fingerprint(configuration)
	return fmt.Sprintf("%s:%x", hash, sha256.Sum256([]byte(Source)))
}

func IsTool(name string) bool {
	return name == "builtin__read" || name == "builtin__bash" || name == "builtin__edit" || name == "builtin__write"
}

func IsFileTool(name string) bool {
	return name == "builtin__read" || name == "builtin__edit" || name == "builtin__write"
}

func Definitions(hash string) []tool.Definition {
	var out []tool.Definition
	for _, entry := range []struct{ name, description, schema string }{
		{"read", "Read workspace text by line (offset starts at 1; default limit 200). Returns full-file SHA-256. Binary or as_artifact:true returns a scoped Artifact.", `{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":1024},"as_artifact":{"type":"boolean"},"offset":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}`},
		{"bash", "Run bash in the isolated workspace (default 60s). cd/export do not persist. Exact approval required. Check exit_code, timed_out and truncated.", `{"type":"object","properties":{"command":{"type":"string","minLength":1,"maxLength":16384},"timeout":{"type":"integer","minimum":1,"maximum":120}},"required":["command"],"additionalProperties":false}`},
		{"edit", "Replace one unique old_text in an existing file, preserving other content. Requires current before_hash from read and exact approval.", `{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":1024},"old_text":{"type":"string","minLength":1,"maxLength":65536},"new_text":{"type":"string","maxLength":65536},"before_hash":{"type":"string","minLength":64,"maxLength":64}},"required":["path","old_text","new_text","before_hash"],"additionalProperties":false}`},
		{"write", "Create a file and parent directories. Overwrite requires current before_hash from read. Exact approval required.", `{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":1024},"content":{"type":"string","maxLength":65536},"before_hash":{"type":"string","minLength":64,"maxLength":64}},"required":["path","content"],"additionalProperties":false}`},
	} {
		permission, risk, replay := "external_effect", "high", "never"
		if entry.name == "read" {
			permission, risk, replay = "read_only", "low", "safe"
		}
		out = append(out, tool.Definition{Host: true, Spec: tool.Spec{Name: "builtin__" + entry.name, Version: 1, Description: entry.description, InputSchema: entry.schema, ResourceHash: hash, Permission: permission, Risk: risk, ReplayPolicy: replay, TimeoutMS: 300000, OutputBytes: 512 << 10, Provider: "environment", ConflictPolicy: "execution_workspace"}, ConflictKeys: func(string) []string { return []string{"environment:" + hash, "computer:local:desktop"} }})
	}
	return out
}
