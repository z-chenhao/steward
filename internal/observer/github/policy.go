package github

import (
	"context"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
)

// Method belongs to an accepted observation, independently of the currently
// selected graph. It is stored separately from the original public source JSON.
type Method struct {
	GraphRevision string          `json:"graph_revision"`
	Recipe        contract.Recipe `json:"recipe"`
}
type ReadStep func(context.Context, Config, Ticket, contract.Request, []byte) (contract.Plan, error)

func ValidRecipe(r contract.Recipe) bool {
	return r.ObservationBytes > 0 && r.ObservationBytes <= 65536 && r.GoalBytes > 0 && r.GoalBytes <= 65536 && len(r.Notice) <= 512 && len(r.Method) <= 2048 && utf8.ValidString(r.Notice) && utf8.ValidString(r.Method)
}

// Preview is just UTF-8 byte projection. The owning version supplies the limits
// and wording; identity/authority are never inferred from its returned text.
func Preview(text string, limit int, suffix string) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + suffix
}
