package contract

import (
	"strconv"
	"strings"
)

// Only the named release families before a contribution existed may omit it.
// Unknown and later versions must provide complete, validated display data.
func displayRequired(version string, firstMinor int) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 || parts[0] != "1" {
		return true
	}
	minor, err := strconv.Atoi(parts[1])
	return err != nil || minor < 0 || minor >= firstMinor
}
