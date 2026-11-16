package contextcompact

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed summary-request.md
var summaryInstructions string

func instructions(target int) string {
	return strings.Replace(strings.TrimSpace(summaryInstructions), "{{target_bytes}}", strconv.Itoa(target), 1) + "\n"
}
