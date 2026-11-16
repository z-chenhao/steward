package web

// Historical pre-service/default32 and direct embedder rendering only. Managed
// pages bind their own frozen plugin functions in memory_presentation.go.
// Remove after these named consumers no longer require the native renderer.
import (
	"github.com/z-chenhao/assistant/internal/memory"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func legacyMemoryURLV1(q memory.InspectionQuery, key, value string) string {
	switch key {
	case "layer":
		q = memory.InspectionQuery{Layer: value}
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
		q = memory.InspectionQuery{View: value}
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
func legacyMemorySourceLabelV1(kind string) string {
	switch kind {
	case "conversation":
		return "Source message"
	case "task":
		return "Source task"
	case "tool":
		return "memory_manage"
	case "control":
		return "Local control"
	}
	return "Source"
}
func legacyMemoryOriginLabelV1(origin string) string {
	switch origin {
	case "written":
		return "Written note"
	case "dream":
		return "Dreaming"
	case "original":
		return "Original record"
	case "automatic":
		return "Automatically extracted"
	case "explicit":
		return "Explicitly remembered"
	}
	return "Unknown origin"
}
func legacyMemoryStatusV1(q memory.InspectionQuery) string {
	if q.Status == "" {
		return "active"
	}
	return q.Status
}

func legacyMemoryLayerLabelV1(layer string) string {
	switch layer {
	case memory.Transcripts:
		return "Transcripts"
	case memory.Representations:
		return "Memory representations"
	case memory.Injected:
		return "Injected context"
	default:
		return "Memory notes"
	}
}
