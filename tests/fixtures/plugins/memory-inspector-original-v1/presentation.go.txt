// Package memoryinspector owns the existing inspector's replaceable presentation.
package memoryinspector

import (
	_ "embed"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed view/memory.html
var memoryViewSource string

//go:embed view/memory.css
var memoryStylesheetSource string

//go:embed view/copy.json
var memoryLocalizationSource string

//go:embed view/memory-worker.js
var memoryWorkerSource string

func project(req contract.Request) contract.Result {
	r := contract.Result{URLs: make([]string, len(req.Navigation)), Labels: contract.Labels{
		Source: contract.SourceLabels{Conversation: "Source message", Task: "Source task", Tool: "memory_manage", Control: "Local control", Other: "Source"},
		Origin: contract.OriginLabels{Written: "Written note", Dream: "Dreaming", Original: "Original record", Automatic: "Automatically extracted", Explicit: "Explicitly remembered", Other: "Unknown origin"},
		Layer:  contract.LayerLabels{Transcripts: "Transcripts", Representations: "Memory representations", Injected: "Injected context", Notes: "Memory notes"}, DefaultStatus: "active"}}
	r.View = contract.View{Resource: contract.ViewResource, Digest: rsiplugin.FileDigest([]byte(memoryViewSource)), Source: memoryViewSource}
	r.Stylesheet = contract.Stylesheet{Resource: contract.StylesheetResource, Digest: rsiplugin.FileDigest([]byte(memoryStylesheetSource)), Source: memoryStylesheetSource}
	r.Localization = contract.Localization{Resource: contract.LocalizationResource, Digest: rsiplugin.FileDigest([]byte(memoryLocalizationSource)), Source: memoryLocalizationSource}
	r.Program = contract.BrowserProgram{Resource: contract.ProgramResource, Digest: rsiplugin.FileDigest([]byte(memoryWorkerSource)), Source: memoryWorkerSource}
	for i, n := range req.Navigation {
		r.URLs[i] = navigate(n.Query, n.Key, n.Value)
	}
	return r
}
func navigate(q contract.Query, key, value string) string {
	switch key {
	case "layer":
		q = contract.Query{Layer: value}
	case "ref":
		if q.View == "maintenance" {
			value = strings.Replace(value, "memory-event:", "memory:maintenance-", 1)
		}
		q.Ref = value
		q.Cursor = ""
		q.Source = ""
		q.Offset = 0
		q.SourcesOffset = 0
		if q.View != "maintenance" {
			q.View = ""
		}
	case "source":
		q.Source = value
		q.Offset = 0
	case "sources_after":
		q.SourcesOffset, _ = strconv.Atoi(value)
		q.Source = ""
		q.Offset = 0
	case "offset":
		q.Offset, _ = strconv.ParseInt(value, 10, 64)
	case "cursor":
		q.Cursor = value
	case "view":
		q = contract.Query{View: value}
	}
	values := url.Values{}
	for key, value := range map[string]string{"layer": q.Layer, "ref": q.Ref, "search": q.Search, "tier": q.Tier, "origin": q.Origin, "status": q.Status, "view": q.View, "cursor": q.Cursor, "source": q.Source} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if !q.Since.IsZero() {
		values.Set("since", q.Since.Format(time.RFC3339))
	}
	if !q.Until.IsZero() {
		values.Set("until", q.Until.Format(time.RFC3339))
	}
	if q.Offset != 0 {
		values.Set("offset", strconv.FormatInt(q.Offset, 10))
	}
	if q.SourcesOffset != 0 {
		values.Set("sources_after", strconv.Itoa(q.SourcesOffset))
	}
	if q.Limit != 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(values) == 0 {
		return "/developer/memory"
	}
	return "/developer/memory?" + values.Encode()
}
