package web

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var appStyleCondition = regexp.MustCompile(`^@(media|container)\s*\(\s*max-width\s*:\s*([0-9]{1,4})px\s*\)$`)
var appStyleGrid = regexp.MustCompile(`^repeat\([1-4],minmax\(0,1fr\)\)$`)

// Supports the original passive stylesheet, not arbitrary CSS. No selectors on
// private identities, new attributes, network sources, scripts, hidden fact
// containers or escape syntax cross this seam. Existing logo-only clipping and
// fallback display do not grant those powers to account/disclosure slots.
func validateAppStylesheet(source string) error {
	return validatePassiveStylesheet(source, appStyleSlots, appStyleCondition, appStyleValue)
}
func appStyleValue(selector, property, value string) bool {
	compact := strings.ReplaceAll(value, " ", "")
	switch property {
	case "display":
		if selector == ".app-logo>span" || selector == ".app-logo img[data-logo-failed]" {
			return passiveStyleOne(value, "none|block")
		}
		return passiveStyleOne(value, "block|flex|grid|inline-flex")
	case "position":
		return (selector == ".app-logo" && passiveStyleOne(value, "static|relative")) || (selector == ".apps-dialog-close" && passiveStyleOne(value, "static|absolute"))
	case "overflow":
		return value == "hidden" || value == "visible"
	case "overflow-wrap":
		return passiveStyleOne(value, "anywhere|break-word|normal")
	case "white-space":
		return passiveStyleOne(value, "normal|nowrap")
	case "text-overflow":
		return passiveStyleOne(value, "ellipsis|clip")
	case "color":
		return passiveStyleOne(compact, "inherit|var(--foreground)|var(--muted-foreground)") || (selector == ".app-logo" && compact == "#363636")
	case "background":
		return passiveStyleOne(compact, "transparent|var(--muted)|var(--hover)|var(--muted-foreground)") || (selector == ".app-logo" && compact == "#fff")
	case "font-size":
		minimum := 12.0
		if selector == ".app-add-account input" {
			minimum = 16
		}
		return (minimum == 12 && passiveStyleOne(compact, "var(--text-meta)|var(--text-section)")) || passiveStyleDimension(value, minimum, 40)
	case "font-weight":
		return passiveStyleOne(value, "400|500|600|700")
	case "line-height":
		n, err := strconv.ParseFloat(value, 64)
		return err == nil && n >= 1 && n <= 2.5
	case "border", "border-top", "border-bottom":
		if value == "0" {
			return true
		}
		p := strings.Fields(value)
		return len(p) == 3 && passiveStyleDimension(p[0], 1, 4) && p[1] == "solid" && p[2] == "var(--border)"
	case "border-radius":
		return value == "50%" || passiveStyleDimension(value, 0, 1024)
	case "flex":
		return passiveStyleOne(value, "0|1|2|3|4")
	case "flex-shrink":
		return value == "0"
	case "flex-direction":
		return passiveStyleOne(value, "row|column")
	case "flex-wrap":
		return passiveStyleOne(value, "wrap|nowrap")
	case "align-items":
		return passiveStyleOne(value, "center|start|end|stretch|baseline")
	case "justify-content":
		return passiveStyleOne(value, "center|start|end|space-between|space-around|space-evenly")
	case "justify-self":
		return passiveStyleOne(value, "start|end|center|stretch")
	case "text-decoration":
		return passiveStyleOne(value, "none|underline")
	case "object-fit":
		return passiveStyleOne(value, "contain|cover")
	case "cursor":
		return value == "pointer"
	case "container-type":
		return value == "inline-size"
	case "grid-template-columns":
		return compact == "1fr" || appStyleGrid.MatchString(compact)
	case "min-width":
		return value == "0"
	case "min-height":
		return passiveStyleDimension(value, 44, 256)
	case "width", "height":
		if strings.Contains(selector, "app-logo") {
			return passiveStyleDimension(value, 12, 128)
		}
		if selector == ".app-pending-dot" {
			return passiveStyleDimension(value, 3, 12)
		}
		if value == "auto" || value == "100%" {
			return property == "width"
		}
		return property == "width" && passiveStyleDimension(value, 120, 1280)
	case "max-width":
		return value == "100%" || passiveStyleDimension(value, 240, 1280)
	case "padding", "padding-top", "margin", "margin-top", "margin-bottom", "margin-left", "margin-right", "gap", "top", "right", "text-underline-offset":
		parts := strings.Fields(value)
		if len(parts) < 1 || len(parts) > 4 {
			return false
		}
		for _, part := range parts {
			if part == "auto" && strings.HasPrefix(property, "margin") {
				continue
			}
			if !passiveStyleDimension(part, 0, 64) {
				return false
			}
		}
		return true
	}
	return false
}

func (v *appsView) StylesheetURL() string {
	if v.Interface != nil && v.Interface.browserView != nil {
		return "/api/apps/browser?interface_revision=" + url.QueryEscape(v.Interface.revision) + "&format=css"
	}
	return "/apps/legacy-v2.css"
}
