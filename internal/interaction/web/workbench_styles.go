package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var workbenchStyleCondition = regexp.MustCompile(`^@(?:media|container\s+(?:task|execution)-layout)\s*\(\s*(?:max|min)-width\s*:\s*([0-9]{1,4})px\s*\)$`)

func validateWorkbenchStylesheet(source string) error {
	// Existing animations are fixed Host-supported CSS constructs. Validate their
	// exact passive bodies, then let the shared bounded parser check all rules.
	for _, animation := range []string{
		`@keyframes send-wait{to{transform:rotate(360deg)}}`,
		`@keyframes thinking{0%,70%,100%{opacity:.35;transform:translateY(0)}35%{opacity:1;transform:translateY(-3px)}}`,
	} {
		source = strings.ReplaceAll(source, animation, "")
	}
	source = strings.ReplaceAll(source, `@media(prefers-reduced-motion:reduce)`, `@media(max-width:4096px)`)
	source = strings.ReplaceAll(source, `@media(hover:hover) and (pointer:fine)`, `@media(max-width:4096px)`)
	// The sole important spacing and empty pseudo-content are captured primitives,
	// not permission to hide arbitrary content or insert candidate text/URLs.
	source = strings.ReplaceAll(source, `padding:24px 12px!important`, `padding:24px 12px`)
	source = strings.ReplaceAll(source, `content:""`, `content:none`)
	source = strings.ReplaceAll(source, `"`, ``)
	return validatePassiveStylesheetRules(source, workbenchStyleSlots, workbenchStyleCondition, func(selector, property, value string) bool {
		return workbenchStyleOriginalValues[property][value] || appStyleValue(selector, property, value)
	}, 320)
}

func (v *workbenchPresentation) StylesheetURL() string {
	return "/api/workbench/style?workbench_revision=" + url.QueryEscape(v.ReaderRevision)
}

func (s *Server) workbenchStylesheet(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	ref := q.Get("workbench_revision")
	if err != nil || len(q) != 1 || len(q["workbench_revision"]) != 1 || ref == "" || !validModelReader(ref) {
		fail(w, 400)
		return
	}
	if s.WebWorkbench == nil {
		fail(w, 503)
		return
	}
	out, revision, managed, err := s.WebWorkbench(r.Context(), ref)
	if err != nil || !managed || revision != ref || !validWorkbenchPresentation(out) || validateWorkbenchStylesheet(out.Stylesheet.Source) != nil {
		fail(w, 503)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Steward-Workbench-Reader", revision)
	_, _ = w.Write([]byte(out.Stylesheet.Source))
}
