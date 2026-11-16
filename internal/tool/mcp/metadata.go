package mcp

// Presentation changes do not rebind the execution scope of a frozen MCP tool.
// Keep the original Config JSON shape for existing resource hashes.
func executionConfig(c Config) any {
	return struct {
		Name      string
		Command   string
		Args      []string
		Directory string
		Tools     map[string]string
	}{c.Name, c.Command, c.Args, c.Directory, c.Tools}
}
