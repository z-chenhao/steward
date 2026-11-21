package web

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/interaction"
)

type approvalField struct{ Name, Value string }
type mailField struct {
	Name, Value, Full           string
	Missing, LiteralLabel, Wide bool
	Count                       int
}
type approvalView struct {
	ID, Hash, Status, Tool, Title, Raw, ReturnTo string
	Fields                                       []approvalField
	Mail                                         []mailField
	Pending                                      bool
}

func approvalFor(a interaction.Approval) approvalView {
	v := approvalView{ID: a.ID, Hash: a.Hash, Status: a.Status, Tool: a.Tool, Raw: a.Arguments, Title: "Review this action", Pending: a.Status == "pending"}
	if v.Mail = mailReviewFields(a); len(v.Mail) != 0 {
		v.Title = "Review email before sending"
		return v
	}
	if a.Tool == "account_spend" {
		v.Title = "Review model spending"
	} else if a.Tool == "builtin__write" || a.Tool == "builtin__edit" {
		v.Title = "Review a file change"
	} else if a.Tool == "builtin__bash" {
		v.Title = "Review a terminal command"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(a.Arguments), &fields) != nil {
		v.Fields = []approvalField{{Name: "Action", Value: a.Arguments}}
		return v
	}
	keys := []string{}
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if a.Tool == "account_spend" && (k == "batch_run_id" || k == "model_snapshot_id" || k == "task_epoch") {
			continue
		}
		value := string(fields[k])
		var str string
		if json.Unmarshal(fields[k], &str) == nil {
			value = str
		}
		v.Fields = append(v.Fields, approvalField{Name: strings.ReplaceAll(k, "_", " "), Value: value})
	}
	if a.Parameters != "" {
		var parameters map[string]json.RawMessage
		if json.Unmarshal([]byte(a.Parameters), &parameters) == nil {
			for _, field := range []struct{ key, label string }{{"button", "Mouse button"}, {"count", "Click count"}, {"direction", "Scroll direction"}, {"amount", "Scroll amount"}, {"timeout_ms", "Timeout (ms)"}, {"name", "Name"}, {"media_type", "Media type"}, {"failure", "Validation result"}} {
				if raw, ok := parameters[field.key]; ok {
					value := string(raw)
					var text string
					if json.Unmarshal(raw, &text) == nil {
						value = text
					}
					v.Fields = append(v.Fields, approvalField{Name: field.label, Value: value})
				}
			}
		}
	}
	return v
}
