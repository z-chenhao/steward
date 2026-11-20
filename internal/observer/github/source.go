package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Fixed scoped source IO and original identities are shared by both interpreters.
// No policy can supply an endpoint, credential, object identity or timestamp.
func (c Client) reader(ctx context.Context, cfg Config) (reader, error) {
	if c.Secrets == nil {
		return reader{}, &Failure{"CREDENTIAL_MISSING"}
	}
	secret, err := c.Secrets.Get(ctx, cfg.CredentialRef)
	if err != nil || len(secret) == 0 || len(secret) > 16<<10 {
		return reader{}, &Failure{"CREDENTIAL_MISSING"}
	}
	r := reader{client: http.Client{Timeout: 20 * time.Second}, token: string(secret)}
	for i := range secret {
		secret[i] = 0
	}
	if c.HTTP != nil {
		r.client = *c.HTTP
	}
	r.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return r, nil
}
func (r reader) identity(ctx context.Context, cfg Config) (Batch, int64, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := r.get(ctx, "/user", &user); err != nil {
		return Batch{}, 0, err
	}
	if user.ID <= 0 || user.Login == "" {
		return Batch{}, 0, &Failure{"INVALID_RESPONSE"}
	}
	var pr struct {
		Number int64 `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Repo struct {
				ID       int64  `json:"id"`
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := r.get(ctx, "/repos/"+cfg.Repository+"/pulls/"+fmt.Sprint(cfg.PullNumber), &pr); err != nil {
		return Batch{}, 0, err
	}
	if pr.Number != cfg.PullNumber || pr.Base.Repo.ID <= 0 || !strings.EqualFold(pr.Base.Repo.FullName, cfg.Repository) || !ValidHeadSHA(pr.Head.SHA) {
		return Batch{}, 0, &Failure{"INVALID_RESPONSE"}
	}
	return Batch{RepositoryID: pr.Base.Repo.ID, HeadSHA: pr.Head.SHA}, user.ID, nil
}

type sourceRow struct {
	observation   Observation
	body          string
	eligible      bool
	timeValid     bool
	identityValid bool
}

func (row sourceRow) validate() error {
	if !row.identityValid || row.eligible && !row.timeValid {
		return &Failure{"INVALID_RESPONSE"}
	}
	return nil
}
func (row sourceRow) project(limit int, suffix string) (Observation, error) {
	if !row.eligible || row.validate() != nil {
		return Observation{}, &Failure{"INVALID_RESPONSE"}
	}
	e := row.observation
	e.Body = identityBodyV1(row.body)
	e.identify(row.body)
	e.Body = Preview(row.body, limit, suffix)
	return e, nil
}
func (r reader) page(ctx context.Context, cfg Config, head string, self int64, kind string, page int) ([]sourceRow, error) {
	path := "/repos/" + cfg.Repository
	rows := []sourceRow{}
	if kind == "review_comment" || kind == "review" || kind == "issue_comment" {
		endpoint := path + "/pulls/" + fmt.Sprint(cfg.PullNumber) + "/comments"
		if kind == "issue_comment" {
			endpoint = path + "/issues/" + fmt.Sprint(cfg.PullNumber) + "/comments"
		} else if kind == "review" {
			endpoint = path + "/pulls/" + fmt.Sprint(cfg.PullNumber) + "/reviews"
		}
		var entries []struct {
			ID        int64     `json:"id"`
			Body      string    `json:"body"`
			State     string    `json:"state"`
			URL       string    `json:"html_url"`
			IssueURL  string    `json:"issue_url"`
			Updated   time.Time `json:"updated_at"`
			Submitted time.Time `json:"submitted_at"`
			User      struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := r.get(ctx, endpoint+fmt.Sprintf("?per_page=%d&page=%d", pageSize, page), &entries); err != nil {
			return nil, err
		}
		if entries == nil {
			return nil, &Failure{"INVALID_RESPONSE"}
		}
		for _, entry := range entries {

			at := entry.Updated
			if at.IsZero() {
				at = entry.Submitted
			}
			identityValid := sourceURL(entry.URL, cfg)
			if kind == "issue_comment" {
				_, fragment, _ := strings.Cut(entry.URL, "#")
				identityValid = identityValid && fragment == "issuecomment-"+fmt.Sprint(entry.ID) && strings.EqualFold(entry.IssueURL, "https://api.github.com"+path+"/issues/"+fmt.Sprint(cfg.PullNumber))
			}
			eligible := entry.User.ID != self && (kind != "review" || entry.State != "PENDING" && !entry.Submitted.IsZero())
			rows = append(rows, sourceRow{observation: Observation{Kind: kind, SourceID: entry.ID, URL: entry.URL, Actor: entry.User.Login, State: entry.State, UpdatedAt: at.UTC()}, body: entry.Body, eligible: eligible, timeValid: !at.IsZero(), identityValid: entry.ID > 0 && entry.User.ID > 0 && entry.User.Login != "" && identityValid})
		}
		return rows, nil
	}
	if kind != "check_run" {
		return nil, &Failure{"INVALID_RESPONSE"}
	}
	var checks struct {
		Runs []struct {
			ID         int64     `json:"id"`
			Name       string    `json:"name"`
			Status     string    `json:"status"`
			Conclusion string    `json:"conclusion"`
			HeadSHA    string    `json:"head_sha"`
			URL        string    `json:"html_url"`
			Completed  time.Time `json:"completed_at"`
		} `json:"check_runs"`
	}
	if err := r.get(ctx, path+"/commits/"+head+fmt.Sprintf("/check-runs?filter=all&per_page=%d&page=%d", pageSize, page), &checks); err != nil {
		return nil, err
	}
	if checks.Runs == nil {
		return nil, &Failure{"INVALID_RESPONSE"}
	}
	for _, entry := range checks.Runs {

		rows = append(rows, sourceRow{observation: Observation{Kind: kind, SourceID: entry.ID, URL: entry.URL, State: entry.Conclusion, HeadSHA: entry.HeadSHA, UpdatedAt: entry.Completed.UTC()}, body: entry.Name, eligible: entry.Status == "completed", timeValid: !entry.Completed.IsZero() && entry.Conclusion != "", identityValid: entry.ID > 0 && entry.HeadSHA == head && checkURL(entry.URL, cfg.Repository)})
	}
	return rows, nil
}
