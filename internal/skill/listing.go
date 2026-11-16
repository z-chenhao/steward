package skill

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/tool"
)

type Query struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func ParseQuery(raw string) (Query, bool) {
	var q Query
	if !tool.Validate(listSchema, raw) || json.Unmarshal([]byte(raw), &q) != nil {
		return q, false
	}
	if q.Limit == 0 {
		q.Limit = 8
	}
	return q, true
}

// ListFrozen preserves the native policy for historical Runs; callers supply
// their already frozen metadata, never a newly loaded catalog.
func ListFrozen(entries []Summary, args string) tool.Result { return skillListing(entries, args) }

// ResourceArguments validates the existing view vocabulary without granting a
// file read or exposing the private catalog implementation.
func ResourceArguments(raw string) (name, path string, ok bool) {
	q, ok := parseView(raw)
	return q.Name, q.Path, ok
}
