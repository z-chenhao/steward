package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

const timezoneCookie = "steward-timezone"

// This is a browser presentation preference, never the scheduler's timezone.
// Before the browser supplies it, native fallback identifies the host zone.
func browserLocation(r *http.Request) *time.Location {
	if cookie, err := r.Cookie(timezoneCookie); err == nil {
		if zone, err := url.QueryUnescape(cookie.Value); err == nil && zone != "" && zone != "Local" && len(zone) <= 100 {
			if location, err := time.LoadLocation(zone); err == nil {
				return location
			}
		}
	}
	return nil
}

func requestLocation(r *http.Request) *time.Location {
	if location := browserLocation(r); location != nil {
		return location
	}
	return time.Local
}

func relativeDate(at, now time.Time, language string) string {
	day := at.Format("2006-01-02")
	if day == now.Format("2006-01-02") {
		return translate(language, "Today")
	}
	if day == now.AddDate(0, 0, -1).Format("2006-01-02") {
		return translate(language, "Yesterday")
	}
	if day == now.AddDate(0, 0, 1).Format("2006-01-02") {
		return translate(language, "Tomorrow")
	}
	for days := 2; days < 7; days++ {
		if day == now.AddDate(0, 0, -days).Format("2006-01-02") {
			return translate(language, at.Weekday().String())
		}
	}
	return day
}

func localTimeHTML(raw, style string, location *time.Location, language string) template.HTML {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || at.IsZero() {
		return ""
	}
	at = at.In(location)
	full := at.Format("2006-01-02 15:04:05")
	now := time.Now().In(location)
	label := relativeDate(at, now, language)
	text := label + " " + at.Format("15:04:05")
	if at.Format("2006-01-02") == now.Format("2006-01-02") {
		text = at.Format("15:04:05")
	}
	switch style {
	case "message", "clock":
		text = at.Format("15:04:05")
	case "date":
		text = label
	}
	focus := ""
	if style == "message" {
		focus = ` tabindex="0"`
	}
	// All dynamic values are escaped; only this host-authored time markup is trusted.
	return template.HTML(fmt.Sprintf(`<time data-local-time="%s" datetime="%s" title="%s" aria-label="%s"%s>%s</time>`,
		template.HTMLEscapeString(style), template.HTMLEscapeString(raw), template.HTMLEscapeString(full), template.HTMLEscapeString(full), focus, template.HTMLEscapeString(text)))
}

func timeFunctions(location *time.Location, language string) template.FuncMap {
	local := func(raw, style string) template.HTML { return localTimeHTML(raw, style, location, language) }
	return template.FuncMap{
		"localTime":        local,
		"humanTime":        func(raw string) template.HTML { return local(raw, "datetime") },
		"observedTime":     func(raw string) template.HTML { return local(raw, "datetime") },
		"traceTime":        func(raw string) template.HTML { return local(raw, "datetime") },
		"observedDateTime": func(at int64) template.HTML { return local(time.UnixMicro(at).Format(time.RFC3339Nano), "datetime") },
		"observedAt":       func(at int64) template.HTML { return local(time.UnixMicro(at).Format(time.RFC3339Nano), "clock") },
		"messageDay": func(raw string) string {
			at, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil || at.IsZero() {
				return ""
			}
			return at.In(location).Format("2006-01-02")
		},
		"timeInput": func(at time.Time) string {
			if at.IsZero() {
				return ""
			}
			return at.In(location).Format("2006-01-02T15:04:05")
		},
	}
}

func requestLayout(r *http.Request) *template.Template {
	return localizedLayout(requestLanguage(r)).Funcs(timeFunctions(requestLocation(r), requestLanguage(r)))
}

// Keep the existing API contract for legacy offset-free filters. Browser GET
// forms use the viewer zone; explicit RFC3339 instants are identical in both.
func filterLocation(r *http.Request) *time.Location {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return time.UTC
	}
	return requestLocation(r)
}

func inspectionTimeIn(raw string, location *time.Location) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return at.UTC(), nil
	}
	for _, format := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if at, err := time.ParseInLocation(format, raw, location); err == nil && at.Format(format) == raw {
			return at.UTC(), nil
		}
	}
	return time.Time{}, observability.ErrInvalid
}
