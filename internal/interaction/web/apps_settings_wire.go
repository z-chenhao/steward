package web

import (
	"strconv"
	"strings"
)

// This existing private POST codec defines the explicit owner edit command,
// including original numeric aliases. Projection policy may not upgrade it to
// persistence. The cap-boundary save interpretation remains exact, not guessed.
func appSettingsCommand(action string, count int) (mode string, remove int, draft bool) {
	if count > 0 {
		if action == "add-override" && count < 128 {
			return "add", -1, true
		}
		if row, err := strconv.Atoi(strings.TrimPrefix(action, "remove-override-")); err == nil && row >= 0 && row < count {
			return "remove", row, true
		}
	}
	return "keep", -1, false
}
