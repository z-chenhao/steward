package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxPages = 20
const pageSize = 100

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

// ValidHeadSHA applies the exact-head format accepted by the PR reader.
func ValidHeadSHA(value string) bool { return shaPattern.MatchString(value) }

type reader struct {
	client http.Client
	token  string
}

func (r reader) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return &Failure{"INVALID_REQUEST"}
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	resp, err := r.client.Do(req)
	if err != nil {
		return &Failure{"UNAVAILABLE"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		code := "UNAVAILABLE"
		switch resp.StatusCode {
		case 401:
			code = "CREDENTIAL_REJECTED"
		case 403, 429:
			code = "ACCESS_DENIED_OR_RATE_LIMITED"
		case 404:
			code = "SOURCE_UNAVAILABLE"
		case 301, 302, 303, 307, 308:
			code = "SOURCE_MOVED"
		}
		return &Failure{code}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || !utf8.Valid(raw) {
		return &Failure{"INVALID_RESPONSE"}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err = dec.Decode(dst); err != nil {
		return &Failure{"INVALID_RESPONSE"}
	}
	if dec.Decode(new(any)) != io.EOF {
		return &Failure{"INVALID_RESPONSE"}
	}
	return nil
}

func sourceURL(raw string, cfg Config) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && strings.EqualFold(u.Path, "/"+cfg.Repository+"/pull/"+fmt.Sprint(cfg.PullNumber))
}
func checkURL(raw, repository string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && strings.HasPrefix(strings.ToLower(u.Path), "/"+strings.ToLower(repository)+"/")
}

// identityBodyV1 is the original canonical source identity projection. Changing
// display policy must not turn the same source version into a new observation.
func identityBodyV1(text string) string {
	const limit = 16 << 10
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + "\n[truncated; open the source URL for full text]"
}
