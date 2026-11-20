package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// A form source already travels through native and enhanced Web submission and
// is frozen with the original user input. Keep its independent retry nonce and
// commit only to the selected reference/revision. This narrows ordinary memory
// operations; it never grants permission or discloses the selected record.
const correctionPrefix = "memory-correction:"

func CorrectionSource(nonce, ref string, revision int64) string {
	if nonce == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(nonce + "\n" + ref + "\n" + strconv.FormatInt(revision, 10)))
	return correctionPrefix + hex.EncodeToString(hash[:]) + ":" + nonce
}

func IsCorrectionSource(source string) bool { return strings.HasPrefix(source, correctionPrefix) }

func MatchesCorrection(source string, m Mutation) bool {
	if !IsCorrectionSource(source) {
		return true
	}
	_, nonce, ok := strings.Cut(strings.TrimPrefix(source, correctionPrefix), ":")
	if !ok || nonce == "" {
		return false
	}
	switch m.Operation {
	case "read", "list":
		return true
	case "write":
		return m.MemoryRef != "" && m.Revision > 0 && source == CorrectionSource(nonce, m.MemoryRef, m.Revision)
	case "remember":
		return m.Supersedes != "" && source == CorrectionSource(nonce, m.Supersedes, 0)
	}
	return false
}
