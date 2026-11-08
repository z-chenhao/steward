package web

import (
	"fmt"
	"github.com/z-chenhao/assistant/internal/observability"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func traceQuery(r *http.Request) (*traceView, error) {
	values := r.URL.Query()
	v := &traceView{Tab: "overview", Field: "output", Compare: "messages"}
	legacy := url.Values{}
	for key, list := range values {
		if len(list) != 1 {
			return nil, observability.ErrInvalid
		}
		switch key {
		case "internal":
			if list[0] != "1" && list[0] != "0" {
				return nil, observability.ErrInvalid
			}
			v.ShowInternal = list[0] == "1"
		case "compare":
			if list[0] != "messages" && list[0] != "tools" {
				return nil, observability.ErrInvalid
			}
			v.Compare = list[0]
		case "search":
			v.Query.Search = strings.TrimSpace(list[0])
		case "session":
			v.Query.Session = list[0]
		case "obs":
			v.Node = list[0]
		case "nodes_after":
			v.After = list[0]
		case "tab":
			v.Tab = list[0]
		case "field":
			v.Field = list[0]
		case "diff_after":
			n, err := strconv.Atoi(list[0])
			if err != nil || n < 0 || n > 1<<30 {
				return nil, observability.ErrInvalid
			}
			v.DiffAfter = n
		case "offset":
			n, err := strconv.Atoi(list[0])
			if err != nil || n < 0 || n > 1<<30 {
				return nil, observability.ErrInvalid
			}
			v.Offset = n
		case "role", "status", "since", "until", "cursor", "run", "task", "relations_after", "limit":
			legacy[key] = list
		default:
			return nil, observability.ErrInvalid
		}
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.RawQuery = legacy.Encode()
	copy.URL = &u
	old, err := inspectionQuery(copy)
	if err != nil {
		return nil, err
	}
	v.Query.ExecutionQuery = old.Query
	v.Run = old.RunID
	v.Task = old.TaskRef
	if v.Query.Validate() != nil || len(v.Node) > 300 || len(v.After) > 512 {
		return nil, observability.ErrInvalid
	}
	switch v.Tab {
	case "overview", "context", "diff", "wire", "metadata":
	default:
		return nil, observability.ErrInvalid
	}
	switch v.Field {
	case "input", "output", "wire":
	default:
		return nil, observability.ErrInvalid
	}
	if v.Run == "" && (v.Node != "" || v.After != "" || v.Offset > 0) {
		return nil, observability.ErrInvalid
	}
	return v, nil
}

func traceURL(v *traceView, field string, value any) string {
	q := url.Values{}
	if v.ShowInternal {
		q.Set("internal", "1")
	}
	if v.Query.Search != "" {
		q.Set("search", v.Query.Search)
	}
	if v.Query.Session != "" {
		q.Set("session", v.Query.Session)
	}
	if v.Query.Role != "" {
		q.Set("role", v.Query.Role)
	}
	if v.Query.Status != "" {
		q.Set("status", v.Query.Status)
	}
	if !v.Query.Since.IsZero() {
		q.Set("since", v.Query.Since.Format(time.RFC3339Nano))
	}
	if !v.Query.Until.IsZero() {
		q.Set("until", v.Query.Until.Format(time.RFC3339Nano))
	}
	if v.Query.Cursor != "" {
		q.Set("cursor", v.Query.Cursor)
	}
	if v.Run != "" {
		q.Set("run", v.Run)
	}
	if v.Task != "" {
		q.Set("task", v.Task)
	}
	if v.Node != "" {
		q.Set("obs", v.Node)
	}
	if v.After != "" {
		q.Set("nodes_after", v.After)
	}
	if v.Tab != "overview" {
		q.Set("tab", v.Tab)
	}
	if v.DiffAfter > 0 {
		q.Set("diff_after", strconv.Itoa(v.DiffAfter))
	}
	if v.Offset > 0 {
		q.Set("offset", strconv.Itoa(v.Offset))
		q.Set("field", v.Field)
	}
	if v.Tab == "diff" {
		q.Set("compare", v.Compare)
	}
	text := fmt.Sprint(value)
	switch x := value.(type) {
	case *int:
		text = strconv.Itoa(*x)
	case *int64:
		text = strconv.FormatInt(*x, 10)
	}
	switch field {
	case "obs":
		q.Set("obs", text)
		q.Del("offset")
		q.Del("field")
	case "run", "task":
		q.Del("run")
		q.Del("task")
		q.Del("obs")
		q.Del("offset")
		q.Del("field")
		q.Del("nodes_after")
		q.Del("tab")
		q.Set(field, text)
	case "tab":
		q.Set("tab", text)
		q.Del("offset")
		q.Del("field")
	case "compare":
		q.Set("compare", text)
		q.Set("tab", "diff")
		q.Del("diff_after")
	case "session":
		q = url.Values{"session": {text}}
		q.Del("run")
		q.Del("task")
		q.Del("obs")
		q.Del("cursor")
		q.Del("nodes_after")
		q.Del("tab")
		q.Del("offset")
		q.Del("field")
	case "input", "output", "wire":
		q.Set("field", field)
		q.Set("offset", text)
	default:
		if text == "" {
			q.Del(field)
		} else {
			q.Set(field, text)
		}
	}
	path := "/developer/observability"
	if run := q.Get("run"); run != "" {
		path = tracePath(run)
		q.Del("run")
	}
	return path + "?" + q.Encode()
}

func tracePath(run string) string { return "/developer/observability/" + url.PathEscape(run) }
func tracePageQuery(r *http.Request) (*traceView, error) {
	copy := r.Clone(r.Context())
	u := *r.URL
	q := r.URL.Query()
	var full *contextView
	for _, values := range q {
		if len(values) != 1 {
			return nil, observability.ErrInvalid
		}
	}
	run := strings.TrimPrefix(r.URL.Path, "/developer/observability/")
	if strings.HasPrefix(r.URL.Path, "/developer/observability/") {
		if run == "" || strings.Contains(run, "/") {
			return nil, observability.ErrInvalid
		}
		if old := q.Get("run"); old != "" && old != run {
			return nil, observability.ErrInvalid
		}
		q.Set("run", run)
	}
	if r.URL.Path == "/developer/context" {
		q.Set("ctx", "1")
		q.Set("tab", "context")
	}
	if q.Get("ctx") == "1" {
		cq := url.Values{"run": q["run"]}
		for _, key := range []string{"step", "section", "item", "format", "after", "round_after", "diff_after", "offset"} {
			if values, ok := q[key]; ok {
				cq[key] = values
				q.Del(key)
			}
		}
		if q.Get("tab") != "context" {
			return nil, observability.ErrInvalid
		}
		contextRequest := r.Clone(r.Context())
		cu := *r.URL
		cu.RawQuery = cq.Encode()
		contextRequest.URL = &cu
		var err error
		full, err = contextQuery(contextRequest)
		if err != nil {
			return nil, err
		}
		q.Del("ctx")
	}
	u.RawQuery = q.Encode()
	copy.URL = &u
	v, err := traceQuery(copy)
	if err != nil {
		return nil, err
	}
	v.FullContext = full
	return v, nil
}

func traceListURL(v *traceView) string {
	if v == nil {
		return "/developer/observability"
	}
	copy := *v
	copy.Run = ""
	copy.Task = ""
	copy.Node = ""
	copy.After = ""
	copy.Tab = "overview"
	copy.Offset = 0
	copy.DiffAfter = 0
	return traceURL(&copy, "cursor", v.Query.Cursor)
}

// Removing a list condition always restarts pagination; details keep list state.
func traceClearURL(v *traceView, field string) string {
	u, _ := url.Parse(traceListURL(v))
	q := u.Query()
	q.Del("cursor")
	if field == "time" {
		q.Del("since")
		q.Del("until")
	} else {
		q.Del(field)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func tracePresetURL(v *traceView, hours int) string {
	copy := *v
	copy.Query.Cursor = ""
	copy.Query.Since = time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Truncate(time.Second)
	copy.Query.Until = time.Time{}
	return traceListURL(&copy)
}
