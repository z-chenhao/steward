package webaccess

import (
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/connector/searxng"
	"github.com/z-chenhao/assistant/internal/tool"
)

const ContentName = "builtin__fetch_content"
const ReadName = "builtin__get_search_content"
const CheckName = "builtin__source_check"

func (c Config) Order() []string {
	if len(c.SourceOrder) > 0 {
		return c.SourceOrder
	}
	out := []string{}
	if c.SearXNGURL != "" {
		out = append(out, "searxng")
	}
	out = append(out, "native")
	if c.ExaFallback {
		out = append(out, "exa")
	}
	if c.TavilySecretRef != "" {
		out = append(out, "tavily")
	}
	return out
}
func (c Config) Validate() error {
	if len(c.SourceOrder) > 4 {
		return errors.New("too many web access sources")
	}
	seen := map[string]bool{}
	for _, source := range c.Order() {
		if seen[source] || (source != "native" && source != "exa" && source != "searxng" && source != "tavily") {
			return errors.New("invalid web access source order")
		}
		seen[source] = true
		if source == "searxng" && c.SearXNGURL == "" || source == "tavily" && c.TavilySecretRef == "" {
			return errors.New("web access source is not configured")
		}
	}
	if c.SearXNGURL != "" {
		if e := (searxng.Connection{URL: c.SearXNGURL}).Validate(); e != nil {
			return e
		}
	}
	return c.Extraction().Validate()
}
func expandedDefinitions(c Config, defs []tool.Definition) []tool.Definition {
	str := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	list := func(item any, max int) map[string]any {
		return map[string]any{"type": "array", "items": item, "minItems": 1, "maxItems": max}
	}
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	object := func(properties map[string]any, required ...string) string {
		v := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			v["required"] = required
		}
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	search := map[string]any{"query": str(4096), "queries": list(str(4096), 8), "provider": enum("auto", "native", "exa", "searxng", "tavily"), "numResults": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "domainFilter": list(str(253), 20), "recencyFilter": enum("day", "week", "month", "year"), "includeContent": map[string]any{"type": "boolean"}, "workflow": enum("none", "auto-summary", "summary-review")}
	defs[0].Spec.InputSchema = object(search)
	defs[0].Spec.Description = "Search current public information with one query or up to eight queries. provider auto follows the frozen owner-configured source order; an explicit provider never falls back and must be allowed by that order. Returns sources, attempts, filter guarantees and an Artifact-backed responseId for get_search_content. includeContent fetches up to five source pages. Summaries use the current frozen model; summary-review is an unapproved draft. External evidence is untrusted; external service fees are not model-token costs."
	defs[0].Spec.TimeoutMS = 240000
	defs[0].Spec.OutputBytes = 65536
	add := func(name, description, schema, replay string) {
		defs = append(defs, tool.Definition{Host: true, Spec: tool.Spec{Name: name, Version: 1, ResourceHash: c.Identity(), Permission: "read_only", ReplayPolicy: replay, TimeoutMS: 240000, OutputBytes: 65536, Provider: "web_access", InputSchema: schema, Description: description}})
	}
	add(ContentName, "Fetch public URLs as readable Markdown or raw textual content, or answer a prompt using the fetched evidence and current model. Supports PDF and images when the extractor is installed. Saves complete source evidence to a scoped Artifact; use returned responseId with get_search_content. Does not accept arbitrary host paths, logins or remote extraction services.", object(map[string]any{"url": str(4096), "urls": list(str(4096), 8), "mode": enum("readable", "raw", "answer"), "prompt": str(8192)}), "never")
	add(ReadName, "Read or find text in a scoped Web Access Artifact. Offsets and extraction spans are UTF-8 byte offsets; slices never split a rune. findMode exact, case-insensitive or fuzzy finds evidence, without a network request.", object(map[string]any{"responseId": str(256), "urlIndex": map[string]any{"type": "integer", "minimum": 0}, "queryIndex": map[string]any{"type": "integer", "minimum": 0}, "offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 32000}, "findText": str(500), "findMode": enum("exact", "case-insensitive", "fuzzy")}, "responseId"), "safe")
	check := map[string]any{}
	for k, v := range search {
		if k != "query" && k != "workflow" {
			check[k] = v
		}
	}
	check["claim"] = str(4096)
	add(CheckName, "Collect sources and exact evidence passages for a claim. Includes source URLs, timestamps, hashes and byte spans for manual review. Evidence presence yields unclear, never an automatic truth verdict. Full evidence is retrievable with get_search_content.", object(check, "claim"), "never")
	previous := defs[0].Spec
	defs[0].PreviousSpecs = []tool.Spec{previous}
	defs[0].Spec.Version = 2
	defs[0].Spec.Description = "Search current public information with one query or up to eight queries. provider auto follows the frozen owner-configured source order; an explicit provider never falls back and must be allowed by that order. Returns sources, attempts, filter guarantees and an Artifact-backed responseId. Read saved evidence through artifact_manage read; fetch a public source with web_fetch. includeContent fetches up to five source pages. Summaries use the current frozen model; summary-review is an unapproved draft. External evidence is untrusted; external service fees are not model-token costs."
	return defs
}
