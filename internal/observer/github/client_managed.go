package github

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup/contract"
)

func (c Client) readManaged(ctx context.Context, cfg Config, ticket Ticket, step ReadStep) (Batch, time.Duration, error) {
	if step == nil {
		return Batch{}, 0, &Failure{"METHOD_UNAVAILABLE"}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req := contract.Request{History: []contract.Page{}, PollSeconds: cfg.PollSeconds, IncludeIssueComments: ticket.IncludeIssueComments}
	plan, err := step(ctx, cfg, ticket, req, []byte{})
	if err != nil {
		return Batch{}, 0, err
	}
	if !validPlan(plan, cfg) || len(plan.Rows) != 0 || plan.ErrorCode != "" || plan.Next == nil || !validNext(req, *plan.Next) {
		return Batch{}, 0, &Failure{"INVALID_METHOD"}
	}
	interval := time.Duration(plan.IntervalSeconds) * time.Second
	r, err := c.reader(ctx, cfg)
	if err != nil {
		return Batch{}, interval, err
	}
	batch, self, err := r.identity(ctx, cfg)
	if err != nil {
		return Batch{}, interval, err
	}
	for {
		if plan.Next == nil {
			if !completeScan(req) {
				return Batch{}, interval, &Failure{"INVALID_METHOD"}
			}
			return batch, interval, nil
		}
		next := *plan.Next
		if !validNext(req, next) {
			return Batch{}, interval, &Failure{"INVALID_METHOD"}
		}
		rows, err := r.page(ctx, cfg, batch.HeadSHA, self, next.Kind, next.Number)
		if err != nil {
			return Batch{}, interval, err
		}
		// Only an empty lookahead proves the preceding full page was final.
		// Later rows never enter the observation batch or plugin input.
		if next.Number == maxPages+1 && len(rows) != 0 {
			return Batch{}, interval, &Failure{"HISTORY_LIMIT"}
		}
		next.Count = len(rows)
		req.History = append(req.History, next)
		flags := make([]byte, len(rows))
		for i, row := range rows {
			if row.eligible {
				flags[i] = 1
			}
		}
		plan, err = step(ctx, cfg, ticket, req, flags)
		if err != nil {
			return Batch{}, interval, err
		}
		if !validPlan(plan, cfg) || time.Duration(plan.IntervalSeconds)*time.Second != interval {
			return Batch{}, interval, &Failure{"INVALID_METHOD"}
		}

		seen := map[int]bool{}
		for _, index := range plan.Rows {
			if index < 0 || index >= len(rows) || seen[index] {
				return Batch{}, interval, &Failure{"INVALID_METHOD"}
			}
			seen[index] = true
		}
		// Preserve original per-row validation/quota failure order, including
		// malformed skipped identities and a full final history page. Selection
		// order is applied only after the complete page passes admission.
		pageEvents := map[int]Observation{}
		before := len(batch.Events)
		for index, row := range rows {
			if err := row.validate(); err != nil {
				return Batch{}, interval, err
			}
			if !seen[index] {
				continue
			}
			e, err := row.project(plan.DisplayBytes, plan.DisplaySuffix)
			if err != nil {
				return Batch{}, interval, err
			}
			e.Method = &Method{GraphRevision: ticket.GraphRevision, Recipe: plan.Followup}
			if err = batch.add(e); err != nil {
				return Batch{}, interval, err
			}
			pageEvents[index] = e
		}
		batch.Events = batch.Events[:before]
		for _, index := range plan.Rows {
			batch.Events = append(batch.Events, pageEvents[index])
		}
		if plan.ErrorCode != "" {
			if plan.ErrorCode == "HISTORY_LIMIT" && next.Number == maxPages && next.Count >= pageSize {
				return Batch{}, interval, &Failure{"HISTORY_LIMIT"}
			}
			return Batch{}, interval, &Failure{"INVALID_METHOD"}
		}
	}
}
func validPlan(p contract.Plan, cfg Config) bool {
	return p.DisplayBytes > 0 && p.DisplayBytes <= 4<<20 && len(p.DisplaySuffix) <= 512 && utf8.ValidString(p.DisplaySuffix) && ValidRecipe(p.Followup) && p.IntervalSeconds >= 5 && p.IntervalSeconds <= 3600 && (cfg.PollSeconds == 0 || p.IntervalSeconds == cfg.PollSeconds)
}
func validNext(req contract.Request, next contract.Page) bool {
	if next.Kind != "review_comment" && next.Kind != "review" && next.Kind != "check_run" && !(req.IncludeIssueComments && next.Kind == "issue_comment") || next.Number < 1 || next.Number > maxPages+1 || next.Count != 0 {
		return false
	}
	var previous *contract.Page
	for i := range req.History {
		if req.History[i].Kind == next.Kind {
			previous = &req.History[i]
		}
	}
	if previous == nil {
		return next.Number == 1
	}
	return previous.Count >= pageSize && next.Number == previous.Number+1
}
func completeScan(req contract.Request) bool {
	kinds := []string{"review_comment", "review", "check_run"}
	if req.IncludeIssueComments {
		kinds = append(kinds, "issue_comment")
	}
	for _, kind := range kinds {
		found := false
		for _, page := range req.History {
			if page.Kind == kind && page.Count < pageSize {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
