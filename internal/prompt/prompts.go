// Package prompt owns built-in instruction text embedded in the executable.
// Callers own selection, message boundaries and frozen Run context; this package
// never reads configuration or files from the runtime working directory.
package prompt

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed system/safety.md
var safety string

//go:embed system/main.md
var main string

//go:embed system/main-execution.md
var mainExecution string

//go:embed system/main-safety.md
var mainSafety string

//go:embed system/identity.md
var identity string

//go:embed system/worker.md
var worker string

//go:embed system/execution.md
var execution string

//go:embed system/progress.md
var progress string

//go:embed capability/environment.md
var environment string

//go:embed capability/codemode.md
var codeMode string

//go:embed capability/delegation.md
var delegation string

//go:embed capability/artifacts.md
var artifacts string

//go:embed capability/memory.md
var memory string

//go:embed capability/computer.md
var computer string

//go:embed summary/working-state.md
var summarySystem string

//go:embed summary/working-state-request.md
var summaryInstructions string

// Remove only the Markdown file's terminal LF. Internal whitespace is model
// input and must not be normalized during packaging.
func text(value string) string { return strings.TrimSuffix(value, "\n") }

func Safety() string        { return text(safety) }
func MainExecution() string { return text(mainExecution) }
func MainSafety() string    { return text(mainSafety) }
func Main() string          { return text(main) }
func Identity() string      { return text(identity) }
func Worker() string        { return text(worker) }
func Progress() string      { return text(progress) }
func Execution() string     { return text(execution) }
func Environment() string   { return text(environment) }
func CodeMode() string      { return text(codeMode) }
func Delegation() string    { return text(delegation) }
func Artifacts() string     { return text(artifacts) }
func Memory() string        { return text(memory) }
func Computer() string      { return text(computer) }
func SummarySystem() string { return text(summarySystem) }

func SummaryInstructions(targetBytes int) string {
	return strings.Replace(text(summaryInstructions), "{{target_bytes}}", strconv.Itoa(targetBytes), 1) + "\n"
}
