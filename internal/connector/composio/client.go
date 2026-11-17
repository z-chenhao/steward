// Package composio implements Composio's public REST protocol. It owns no
// agent loop, approvals, credentials, or automatic retries.
package composio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
)

const Endpoint = "https://backend.composio.dev/api/v3.1"
const maxResponse = 8 << 20

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,159}$`)

func ValidID(s string) bool { return identifier.MatchString(s) }

// Error deliberately excludes provider bodies, URLs, and credentials.
type Error struct {
	Status    int
	Uncertain bool
}

func (e *Error) Error() string { return fmt.Sprintf("Composio request failed (HTTP %d)", e.Status) }

type Client struct {
	Secrets llm.SecretStore
	KeyRef  string
	// Endpoint is only overridden by in-process protocol tests. Production
	// composition always uses the constant above, never a model-supplied URL.
	Endpoint string
	HTTP     *http.Client
}

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Toolkit struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	AuthSchemes []string `json:"auth_schemes,omitempty"`
	ManagedAuth []string `json:"composio_managed_auth_schemes,omitempty"`
	NoAuth      bool     `json:"no_auth"`
	Meta        struct {
		Logo        string     `json:"logo"`
		Description string     `json:"description"`
		Categories  []Category `json:"categories,omitempty"`
		ToolsCount  int        `json:"tools_count"`
	} `json:"meta"`
}
type Catalog struct {
	Items      []Toolkit `json:"items"`
	NextCursor string    `json:"next_cursor"`
	Total      int       `json:"total_items"`
}
type Account struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Status   string `json:"status"`
	Disabled bool   `json:"is_disabled"`
	Toolkit  struct {
		Slug string `json:"slug"`
	} `json:"toolkit"`
}
type Tool struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Deprecated  bool   `json:"is_deprecated"`
	NoAuth      bool   `json:"no_auth"`
	Toolkit     struct {
		Slug string `json:"slug"`
	} `json:"toolkit"`
	Input json.RawMessage `json:"input_parameters"`
}
type Tools struct {
	Items      []Tool `json:"items"`
	NextCursor string `json:"next_cursor"`
}
type Link struct {
	ConnectionID string    `json:"connection_id,omitempty"`
	ExpiresAt    time.Time `json:"-"`
	URL          string    `json:"redirect_url"`
	AccountID    string    `json:"connected_account_id"`
}
type Execution struct {
	Data       json.RawMessage `json:"data"`
	Successful bool            `json:"successful"`
	LogID      string          `json:"log_id,omitempty"`
	// Provider error text can contain echoed credentials; expose a fixed code.
	Error string `json:"error,omitempty"`
}

func (c Client) request(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if c.Secrets == nil || c.KeyRef == "" {
		return &Error{Status: 401}
	}
	key, err := c.Secrets.Get(ctx, c.KeyRef)
	if err != nil || len(key) == 0 {
		return &Error{Status: 401}
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	base := c.Endpoint
	if base == "" {
		base = Endpoint
	}
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return &Error{}
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	for name, values := range query {
		if len(values) == 1 && values[0] == "" {
			query.Del(name)
		}
	}
	u.RawQuery = query.Encode()
	var input io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return &Error{}
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), input)
	if err != nil {
		return &Error{}
	}
	req.Header.Set("x-api-key", string(key))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 45 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	mutating := method != http.MethodGet
	if err != nil {
		return &Error{Uncertain: mutating}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Uncertain: mutating && (resp.StatusCode >= 500 || resp.StatusCode == 408)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return &Error{Status: resp.StatusCode, Uncertain: mutating}
	}
	if out == nil {
		return nil
	}
	// This also prevents an echoed project key from entering logs/artifacts.
	data = bytes.ReplaceAll(data, key, []byte("[redacted]"))
	if json.Unmarshal(data, out) != nil {
		return &Error{Status: resp.StatusCode, Uncertain: mutating}
	}
	return nil
}

func (c Client) Toolkit(ctx context.Context, slug string) (Toolkit, error) {
	var v Toolkit
	if !ValidID(slug) {
		return v, &Error{Status: 400}
	}
	err := c.request(ctx, "GET", "/toolkits/"+slug, nil, nil, &v)
	return v, err
}

// CreateSession is one non-replaying protocol effect. The fixed envelope only
// grants the accepted Toolkit/user/auth selection; no extra router abilities.
func (c Client) CreateSession(ctx context.Context, user, toolkit, alias, authConfig string) (string, error) {
	if !ValidID(user) || !ValidID(toolkit) || !ValidID(alias) || authConfig != "" && !ValidID(authConfig) {
		return "", &Error{Status: 400}
	}
	body := map[string]any{"user_id": user, "toolkits": map[string]any{"enabled": []string{toolkit}}, "instant": false, "manage_connections": map[string]any{"enabled": false}, "workbench": map[string]any{"enable": false}, "proxy_execute": map[string]any{"enable": false}, "multi_account": map[string]any{"enable": true, "require_explicit_selection": true}, "execute": map[string]any{"enable_multi_execute": false}}
	if authConfig != "" {
		body["auth_configs"] = map[string]string{toolkit: authConfig}
	}

	var session struct {
		ID string `json:"session_id"`
	}
	if err := c.request(ctx, "POST", "/tool_router/session", nil, body, &session); err != nil {
		return "", err
	}
	if !ValidID(session.ID) {
		return "", &Error{Uncertain: true}
	}
	return session.ID, nil
}

// CreateSessionLink is one non-replaying protocol effect. It validates the
// original provider URL and account identity before returning an opaque grant.
func (c Client) CreateSessionLink(ctx context.Context, session, toolkit, alias string) (Link, error) {
	if !ValidID(session) || !ValidID(toolkit) || !ValidID(alias) {
		return Link{}, &Error{Status: 400}
	}
	var link Link
	if err := c.request(ctx, "POST", "/tool_router/session/"+session+"/link", nil, map[string]string{"toolkit": toolkit, "alias": alias}, &link); err != nil {
		return link, err
	}
	u, err := url.Parse(link.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Hostname() != "app.composio.dev" && u.Hostname() != "connect.composio.dev") || !ValidID(link.AccountID) {
		return Link{}, &Error{Uncertain: true}
	}
	return link, nil
}
func (c Client) Account(ctx context.Context, id string) (Account, error) {
	var v Account
	if !ValidID(id) {
		return v, &Error{Status: 400}
	}
	err := c.request(ctx, "GET", "/connected_accounts/"+id, nil, nil, &v)
	return v, err
}

// RevokeAccount performs one accepted token-revocation effect. Only this exact
// original account's authoritative REVOKED response proves revocation.
func (c Client) RevokeAccount(ctx context.Context, id string) bool {
	if !ValidID(id) {
		return false
	}
	var result struct {
		Account Account `json:"connected_account"`
	}
	err := c.request(ctx, "POST", "/connected_accounts/"+id+"/revoke", nil, nil, &result)
	return err == nil && result.Account.ID == id && result.Account.Status == "REVOKED"
}

// DeleteAccount performs one broker-record effect. Success does not prove the
// supplier token was revoked, and failure is never retried by this adapter.
func (c Client) DeleteAccount(ctx context.Context, id string) error {
	if !ValidID(id) {
		return &Error{Status: 400}
	}
	return c.request(ctx, "DELETE", "/connected_accounts/"+id, nil, nil, nil)
}
func (c Client) Tool(ctx context.Context, slug, version string) (Tool, error) {
	var v Tool
	if !ValidID(slug) || !ValidID(version) || version == "latest" {
		return v, &Error{Status: 400}
	}
	err := c.request(ctx, "GET", "/tools/"+slug, url.Values{"version": {version}}, nil, &v)
	return v, err
}
func (c Client) Execute(ctx context.Context, user, account, slug, version string, args json.RawMessage) (Execution, error) {
	var raw struct {
		Data       json.RawMessage `json:"data"`
		Successful *bool           `json:"successful"`
		LogID      string          `json:"log_id"`
	}
	if !ValidID(slug) || !ValidID(version) || version == "latest" {
		return Execution{}, &Error{Status: 400}
	}
	body := map[string]any{"user_id": user, "version": version, "arguments": args}
	if account != "" {
		body["connected_account_id"] = account
	}
	err := c.request(ctx, "POST", "/tools/execute/"+slug, nil, body, &raw)
	if err != nil {
		return Execution{}, err
	}
	if raw.Successful == nil {
		return Execution{}, &Error{Uncertain: true}
	}
	v := Execution{Data: redactCredentials(raw.Data), Successful: *raw.Successful, LogID: raw.LogID}
	if !v.Successful {
		v.Error = "COMPOSIO_TOOL_FAILED"
	}
	return v, nil
}

func Uncertain(err error) bool { var e *Error; return errors.As(err, &e) && e.Uncertain }

func redactCredentials(raw json.RawMessage) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, item := range v {
				switch strings.ToLower(strings.ReplaceAll(k, "-", "_")) {
				case "access_token", "refresh_token", "client_secret", "api_key", "password", "authorization", "cookie", "set_cookie":
					v[k] = "[redacted]"
				default:
					visit(item)
				}
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		}
	}
	visit(value)
	b, _ := json.Marshal(value)
	return b
}
