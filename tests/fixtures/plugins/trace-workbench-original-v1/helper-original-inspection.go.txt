package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
)

// ExecutionReader is separate from the conversation/action Core. Inspection
// reads remain available without a model runtime or a new execution pipeline.
type ExecutionReader interface {
	Executions(context.Context, observability.ExecutionQuery) (observability.Executions, error)
	Relationships(context.Context, observability.RelationshipQuery) (observability.Relationships, error)
}

type inspectionView struct {
	Query                          observability.ExecutionQuery
	RunID, TaskRef, RelationsAfter string
	List                           observability.Executions
	Relations                      *observability.Relationships
}

func inspectionQuery(r *http.Request) (*inspectionView, error) {
	v := &inspectionView{}
	q := r.URL.Query()
	for key, values := range q {
		switch key {
		case "role", "status", "since", "until", "cursor", "run", "task", "relations_after", "limit":
		default:
			return nil, observability.ErrInvalid
		}
		if len(values) != 1 {
			return nil, observability.ErrInvalid
		}
	}
	v.Query.Role = q.Get("role")
	v.Query.Status = q.Get("status")
	v.Query.Cursor = q.Get("cursor")
	var err error
	if value := q.Get("limit"); value != "" {
		v.Query.Limit, err = strconv.Atoi(value)
		if err != nil || v.Query.Limit < 1 {
			return nil, observability.ErrInvalid
		}
	}
	if v.Query.Since, err = inspectionTimeIn(q.Get("since"), filterLocation(r)); err != nil {
		return nil, err
	}
	if v.Query.Until, err = inspectionTimeIn(q.Get("until"), filterLocation(r)); err != nil {
		return nil, err
	}
	if err = v.Query.Validate(); err != nil {
		return nil, err
	}
	v.RunID = q.Get("run")
	v.TaskRef = q.Get("task")
	v.RelationsAfter = q.Get("relations_after")
	if v.RunID != "" || v.TaskRef != "" {
		err = (observability.RelationshipQuery{RunID: v.RunID, TaskRef: v.TaskRef, Cursor: v.RelationsAfter, Limit: v.Query.Limit}).Validate()
	} else if v.RelationsAfter != "" {
		err = observability.ErrInvalid
	}
	return v, err
}

func (s *Server) inspect(ctx context.Context, v *inspectionView, relationshipsOnly bool) error {
	if s.Inspector == nil {
		return errors.New("inspection unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if v.RunID != "" || v.TaskRef != "" {
		relations, err := s.Inspector.Relationships(ctx, observability.RelationshipQuery{RunID: v.RunID, TaskRef: v.TaskRef, Cursor: v.RelationsAfter, Limit: v.Query.Limit})
		if err != nil {
			return err
		}
		v.Relations = &relations
	} else if relationshipsOnly {
		return observability.ErrInvalid
	}
	if !relationshipsOnly {
		list, err := s.Inspector.Executions(ctx, v.Query)
		if err != nil {
			return err
		}
		v.List = list
	}
	return nil
}

func inspectionFailure(w http.ResponseWriter, err error) {
	status := 503
	if errors.Is(err, observability.ErrInvalid) {
		status = 400
	} else if errors.Is(err, observability.ErrNotFound) {
		status = 404
	}
	fail(w, status)
}

func (s *Server) inspectionAPI(w http.ResponseWriter, r *http.Request, relationships bool) {
	v, err := inspectionQuery(r)
	if err == nil && !relationships && (v.RunID != "" || v.TaskRef != "" || v.RelationsAfter != "") {
		err = observability.ErrInvalid
	}
	if err == nil && relationships && (v.Query.Role != "" || v.Query.Status != "" || !v.Query.Since.IsZero() || !v.Query.Until.IsZero() || v.Query.Cursor != "") {
		err = observability.ErrInvalid
	}
	if err == nil {
		err = s.inspect(r.Context(), v, relationships)
	}
	if err != nil {
		inspectionFailure(w, err)
		return
	}
	if relationships {
		jsonResult(w, v.Relations)
	} else {
		jsonResult(w, v.List)
	}
}

func runRole(role string) string {
	switch role {
	case "main":
		return "Main"
	case "root":
		return "Root Worker"
	case "child":
		return "Child Worker"
	}
	return "Execution"
}
func taskAction(operation string) string {
	switch operation {
	case "create":
		return "Created Task"
	case "append":
		return "Appended instruction"
	case "pause":
		return "Paused Task"
	case "cancel":
		return "Cancelled Task"
	case "complete":
		return "Completed Task"
	}
	return "Recorded Task action"
}

func treeIndent(depth int) []struct{} { return make([]struct{}, max(0, min(depth, 16))) }
