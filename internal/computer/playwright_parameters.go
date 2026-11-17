package computer

import (
	"math"
	"strings"
	"unicode/utf16"
)

func PlaywrightParameterMethod(method string) bool {
	switch method {
	case "playwright.count", "playwright.textContent", "playwright.click", "playwright.fill", "playwright.waitForSelector", "playwright.waitForURL", "playwright.download", "playwright.upload":
		return true
	}
	return false
}

// Only the original public business strings and explicit time budget enter the
// method. File grants, paths, handles, navigation and provider IDs stay in Host.
func PlaywrightParameterArguments(method string, args []any) []any {
	original := object(arg(args, 0))
	out := map[string]any{}
	keys := []string{"selector", "timeoutMs"}
	if method == "playwright.waitForURL" {
		keys = []string{"url", "timeoutMs"}
	}
	if method == "playwright.fill" {
		keys = append(keys, "value")
	}
	for _, key := range keys {
		if value, ok := original[key]; ok {
			out[key] = value
		}
	}
	return []any{out}
}

// This proves intent/bounds; it never chooses the absent timeout or resolves
// CSS/URL. Driver keeps the original document, CSS-only and URL security fences.
func validPlaywrightParameters(method string, args []any, p ActionParameters) bool {
	original := object(arg(args, 0))
	if p.TimeoutMS == nil || *p.TimeoutMS < 1 || *p.TimeoutMS > 10000 {
		return false
	}
	if explicit, ok := original["timeoutMs"]; ok {
		n, ok := explicit.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || float64(*p.TimeoutMS) != n {
			return false
		}
	}
	q := p
	if method == "playwright.upload" && (p.NameFromSource || p.Name != "" || p.MediaType != "") {
		if !validUploadPresentation(args, p) {
			return false
		}
		q.NameFromSource, q.Name, q.MediaType = false, "", ""
	}
	q.TimeoutMS = nil
	if method == "playwright.waitForURL" {
		if value, ok := original["url"].(string); !ok || value != p.URL {
			return false
		}
		q.URL = ""
	} else {
		if value, ok := original["selector"].(string); !ok || value != p.Selector || value == "" || len(utf16.Encode([]rune(value))) > 2048 || strings.Contains(value, ">>") {
			return false
		}
		q.Selector = ""
	}
	if method == "playwright.fill" {
		if value, ok := original["value"].(string); !ok || value != p.Value {
			return false
		}
		q.Value = ""
	}
	return emptyActionParameters(q)
}
