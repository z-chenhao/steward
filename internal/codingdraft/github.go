package codingdraft

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/llm"
)

type GitHub struct {
	Secrets   llm.SecretStore
	Transport http.RoundTripper
}

func (g GitHub) request(ctx context.Context, t Target, method, path string, body, out any) (int, error) {
	if !t.Valid() || g.Secrets == nil {
		return 0, ErrInvalid
	}
	token, e := g.Secrets.Get(ctx, t.CredentialRef)
	if e != nil || len(token) == 0 {
		return 0, ErrInvalid
	}
	defer clear(token)
	var input io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return 0, ErrInvalid
		}
		input = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.github.com/repos/"+t.Repository+path, input)
	if e != nil {
		return 0, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Transport: g.Transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return 0, ErrUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, ErrConflict
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if e != nil || len(data) > 4<<20 || out != nil && json.Unmarshal(data, out) != nil {
		return resp.StatusCode, ErrUnknown
	}
	return resp.StatusCode, nil
}
func (g GitHub) ref(ctx context.Context, t Target, name string) (string, error) {
	var out struct {
		Ref    string
		Object struct{ SHA, Type string }
	}
	code, e := g.request(ctx, t, "GET", "/git/ref/heads/"+name, nil, &out)
	if code == 404 {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if out.Ref != "refs/heads/"+name || out.Object.Type != "commit" || !objectPattern.MatchString(out.Object.SHA) {
		return "", ErrConflict
	}
	return out.Object.SHA, nil
}
func (g GitHub) Inspect(ctx context.Context, t Target) (RemoteState, error) {
	var r RemoteState
	var repo struct {
		ID   int64
		Name string `json:"full_name"`
	}
	if _, e := g.request(ctx, t, "GET", "", nil, &repo); e != nil || repo.ID < 1 || !strings.EqualFold(repo.Name, t.Repository) {
		return r, ErrConflict
	}
	r.RepositoryID = repo.ID
	var e error
	if r.Base, e = g.ref(ctx, t, t.Base); e != nil || r.Base == "" {
		return r, ErrConflict
	}
	if r.Branch, e = g.ref(ctx, t, t.Branch); e != nil {
		return r, e
	}
	var commit struct {
		SHA  string
		Tree struct{ SHA string }
	}
	if _, e = g.request(ctx, t, "GET", "/git/commits/"+r.Base, nil, &commit); e != nil || commit.SHA != r.Base || !objectPattern.MatchString(commit.Tree.SHA) {
		return r, ErrConflict
	}
	r.Tree = commit.Tree.SHA
	var tree struct {
		SHA       string
		Truncated *bool
		Tree      []coding.DraftEntry
	}
	if _, e = g.request(ctx, t, "GET", "/git/trees/"+r.Tree+"?recursive=1", nil, &tree); e != nil || tree.SHA != r.Tree || tree.Truncated == nil || *tree.Truncated || tree.Tree == nil || len(tree.Tree) > coding.NativeImportLimits().Entries {
		return r, ErrConflict
	}
	seen := map[string]bool{}
	for _, file := range tree.Tree {
		if file.Path == "" || len(file.Path) > 1024 || strings.HasPrefix(file.Path, "/") || file.Path == "." || file.Path == ".." || strings.HasPrefix(file.Path, "../") || path.Clean(file.Path) != file.Path || strings.IndexFunc(file.Path, unicode.IsControl) >= 0 || seen[file.Path] {
			return r, ErrConflict
		}
		for _, part := range strings.Split(file.Path, "/") {
			if strings.EqualFold(part, ".git") {
				return r, ErrConflict
			}
		}
		seen[file.Path] = true
		if file.Type == "tree" {
			continue
		}
		if file.Type != "blob" || file.Mode != "100644" && file.Mode != "100755" || !objectPattern.MatchString(file.SHA) {
			return r, ErrConflict
		}
		r.Entries = append(r.Entries, file)
	}
	return r, nil
}
func (g GitHub) Blob(ctx context.Context, t Target, data []byte, sha string) error {
	var out struct{ SHA string }
	_, e := g.request(ctx, t, "POST", "/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(data), "encoding": "base64"}, &out)
	if e != nil || out.SHA != sha {
		return ErrUnknown
	}
	return nil
}
func (g GitHub) Tree(ctx context.Context, t Target, entries []coding.DraftEntry, sha string) error {
	var out struct{ SHA string }
	_, e := g.request(ctx, t, "POST", "/git/trees", map[string]any{"tree": entries}, &out)
	if e != nil || out.SHA != sha {
		return ErrUnknown
	}
	return nil
}
func (g GitHub) Commit(ctx context.Context, p Plan) (string, error) {
	author := map[string]string{"name": "Steward", "email": "steward@users.noreply.github.com", "date": Date(p.VerifiedAt)}
	var out struct {
		SHA     string
		Tree    struct{ SHA string }
		Parents []struct{ SHA string }
	}
	_, e := g.request(ctx, p.Target, "POST", "/git/commits", map[string]any{"message": p.Message, "tree": p.Tree, "parents": []string{p.BaseSHA}, "author": author, "committer": author}, &out)
	if e != nil || !objectPattern.MatchString(out.SHA) || out.Tree.SHA != p.Tree || len(out.Parents) != 1 || out.Parents[0].SHA != p.BaseSHA {
		return "", ErrUnknown
	}
	return out.SHA, nil
}
func (g GitHub) CreateRef(ctx context.Context, t Target, head string) error {
	var out struct {
		Ref    string
		Object struct{ SHA string }
	}
	_, e := g.request(ctx, t, "POST", "/git/refs", map[string]string{"ref": "refs/heads/" + t.Branch, "sha": head}, &out)
	if e != nil || out.Ref != "refs/heads/"+t.Branch || out.Object.SHA != head {
		return ErrUnknown
	}
	return nil
}

type gitPull struct {
	Number     int64
	Draft      bool
	URL        string `json:"html_url"`
	Body       string
	Head, Base struct {
		Ref, SHA string
		Repo     struct{ ID int64 }
	}
}

func (p gitPull) match(plan Plan, digest, head string) bool {
	return p.Draft && p.Number > 0 && p.Base.SHA == plan.BaseSHA && p.Head.SHA == head && p.Head.Ref == plan.Target.Branch && p.Base.Ref == plan.Target.Base && p.Head.Repo.ID == plan.RepositoryID && p.Base.Repo.ID == plan.RepositoryID && p.URL == "https://github.com/"+plan.Target.Repository+"/pull/"+strconv.FormatInt(p.Number, 10) && strings.Contains(p.Body, marker(digest))
}
func marker(digest string) string { return "<!-- steward-draft:" + digest + " -->" }
func (p gitPull) receipt() Pull {
	return Pull{Number: p.Number, URL: p.URL, Head: p.Head.SHA, Base: p.Base.Ref}
}
func (g GitHub) CreatePull(ctx context.Context, p Plan, digest, head string) (Pull, error) {
	var out gitPull
	body := map[string]any{"head": p.Target.Branch, "base": p.Target.Base, "title": p.Target.Title, "body": p.Body + "\n\n" + marker(digest), "draft": true}
	_, e := g.request(ctx, p.Target, "POST", "/pulls", body, &out)
	if e != nil || !out.match(p, digest, head) {
		return Pull{}, ErrUnknown
	}
	return out.receipt(), nil
}
func (g GitHub) FindPull(ctx context.Context, p Plan, digest, head string) (Pull, bool, error) {
	if !objectPattern.MatchString(head) {
		return Pull{}, false, nil
	}
	owner, _, _ := strings.Cut(p.Target.Repository, "/")
	q := url.Values{"state": {"all"}, "base": {p.Target.Base}, "head": {owner + ":" + p.Target.Branch}, "per_page": {"100"}}
	for page := 1; page <= 5; page++ {
		q.Set("page", fmt.Sprint(page))
		var list []gitPull
		if _, e := g.request(ctx, p.Target, "GET", "/pulls?"+q.Encode(), nil, &list); e != nil {
			return Pull{}, false, e
		}
		for _, v := range list {
			if v.match(p, digest, head) {
				return v.receipt(), true, nil
			}
		}
		if len(list) < 100 {
			return Pull{}, false, nil
		}
	}
	return Pull{}, false, ErrUnknown
}
