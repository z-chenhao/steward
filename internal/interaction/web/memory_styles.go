package web

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var memoryStyleCondition = regexp.MustCompile(`^@(?:media|container\s+inspection-layout)\s*\(\s*max-width\s*:\s*([0-9]{1,4})px\s*\)$`)
var memoryStyleViewport = regexp.MustCompile(`^calc\(100dvh\s*-\s*([0-9]{1,3})px\)$`)
var memoryStyleSelectedColor = regexp.MustCompile(`^color-mix\(insrgb,var\(--trace-agent\)([0-9]{1,2})%,var\(--card\)\)$`)

func validateMemoryStylesheet(source string) error {
	return validatePassiveStylesheet(source, memoryStyleSlots, memoryStyleCondition, memoryStyleValue)
}
func memoryStyleValue(selector, property, value string) bool {
	compact := strings.ReplaceAll(value, " ", "")
	viewport := func() bool {
		m := memoryStyleViewport.FindStringSubmatch(value)
		if m == nil {
			return false
		}
		n, _ := strconv.Atoi(m[1])
		return n <= 512
	}
	switch property {
	case "font-size":
		return compact == "var(--text-ui)" || passiveStyleDimension(value, 11, 40)
	case "white-space":
		return value == "pre-wrap"
	case "color":
		return passiveStyleOne(compact, "var(--trace-agent)|var(--muted-foreground)|var(--foreground)|inherit")
	case "background":
		if passiveStyleOne(compact, "var(--hover)|var(--card)|transparent") {
			return true
		}
		m := memoryStyleSelectedColor.FindStringSubmatch(compact)
		if m == nil {
			return false
		}
		n, _ := strconv.Atoi(m[1])
		return n <= 30
	case "border-bottom":
		return compact == "2pxsolidvar(--foreground)" || appStyleValue(selector, property, value)
	case "align-items", "justify-content":
		return passiveStyleOne(value, "flex-start|flex-end") || appStyleValue(selector, property, value)
	case "flex":
		return value == "none" || appStyleValue(selector, property, value)
	case "width":
		return value == "100%" || passiveStyleDimension(value, 12, 2048)
	case "height":
		return passiveStyleOne(value, "auto|100%") || passiveStyleDimension(value, 12, 2048) || viewport()
	case "max-height":
		return value == "none" || passiveStyleDimension(value, 160, 2048)
	case "min-height":
		if strings.Contains(selector, "a.memory-select") {
			return passiveStyleDimension(value, 44, 256)
		}
		return value == "0" || passiveStyleDimension(value, 44, 512) || viewport()
	case "min-width":
		return value == "0" || passiveStyleDimension(value, 320, 2048)
	case "padding-left", "padding-bottom", "margin-block":
		return appStyleValue(selector, "margin", value)
	}
	return appStyleValue(selector, property, value)
}

func (v *memoryView) StylesheetURL() string {
	if v.Presentation != nil {
		return "/api/developer/memory/style?inspector_revision=" + url.QueryEscape(v.Presentation.ReaderRevision)
	}
	return "/memory/legacy-v1.css"
}
