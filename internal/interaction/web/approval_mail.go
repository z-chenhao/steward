package web

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// The plugin supplies the operation and argument keys. Only the local owner view
// reads the accepted values. Unknown provider fields remain visible verbatim.
func mailReviewFields(a interaction.Approval) []mailField {
	layout := a.MailReview
	if layout == nil || layout.Account == "" || len(layout.Recipe.Fields) == 0 {
		return nil
	}
	var call struct {
		Slug      string `json:"tool_slug"`
		Version   string `json:"version"`
		Hash      string `json:"descriptor_hash"`
		Arguments string `json:"arguments_json"`
	}
	if rsiplugin.Decode([]byte(a.Arguments), &call) != nil || call.Slug != layout.Recipe.Operation || call.Version == "" || call.Hash == "" {
		return nil
	}
	var args map[string]json.RawMessage
	if rsiplugin.Decode([]byte(call.Arguments), &args) != nil || args == nil {
		return nil
	}
	groups := map[string][]json.RawMessage{}
	seen := map[string]bool{}
	for _, field := range layout.Recipe.Fields {
		if field.Key == "" || seen[field.Key] {
			return nil
		}
		seen[field.Key] = true
		raw, ok := args[field.Key]
		if !ok {
			continue
		}
		groups[field.Role] = append(groups[field.Role], raw)
	}
	fields := []mailField{{Name: "Authorized account", Value: layout.Account}}
	for _, group := range []struct{ role, label string }{
		{"from", "Send-as address"}, {"to", "Email recipients"}, {"cc", "Email CC"}, {"bcc", "Email BCC"},
		{"subject", "Email subject"}, {"attachments", "Email attachments"}, {"body", "Email body"},
	} {
		values := groups[group.role]
		delete(groups, group.role)
		if group.role == "from" && len(values) == 0 {
			continue
		}
		texts := make([]string, 0, len(values))
		for _, raw := range values {
			texts = append(texts, mailArgument(raw))
		}
		field := mailField{Name: group.label, Value: strings.Join(texts, "\n"), Missing: len(values) == 0}
		if group.role == "attachments" && len(values) == 1 {
			if names := attachmentNames(values[0]); len(names) > 0 {
				field.Full = string(values[0])
				field.Value = strings.Join(names, "\n")
				field.Count = len(names)
			}
		}
		if group.role == "body" {
			field.Wide = true
			field = foldMailValue(field)
		}
		fields = append(fields, field)
	}
	if len(groups) != 0 {
		return nil
	}
	var extra []string
	for key := range args {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		fields = append(fields, foldMailValue(mailField{Name: key, Value: mailArgument(args[key]), LiteralLabel: true}))
	}
	return fields
}

func mailArgument(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil && string(raw) != "null" {
		if value == "" {
			return `""`
		}
		return value
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		for _, s := range list {
			if s == "" {
				return string(raw)
			}
		}
		return strings.Join(list, "\n")
	}
	return string(raw)
}

func foldMailValue(field mailField) mailField {
	const limit = 72
	text := []rune(field.Value)
	if len(text) > limit {
		field.Full = field.Value
		field.Value = string(text[:limit]) + "…"
	}
	return field
}

// Prefer the supplied file names, keeping each exact upload reference available
// even when two attachments have the same name.
func attachmentNames(raw json.RawMessage) []string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		items = []json.RawMessage{raw}
	}
	var names []string
	for _, item := range items {
		var file struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(item, &file) != nil || strings.TrimSpace(file.Name) == "" {
			return nil
		}
		names = append(names, file.Name)
	}
	return names
}
