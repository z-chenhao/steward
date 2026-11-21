package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// StatusFacts are authoritative credential/transport observations, without
// replaceable repair instructions or presentation policy.
type StatusFacts struct {
	Channel            string     `json:"channel"`
	Mode               string     `json:"mode"`
	State              string     `json:"state"`
	WorkspaceLinkScope string     `json:"workspace_link_scope,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}
type ConnectionStatus struct {
	StatusFacts
	Action              string `json:"action,omitempty"`
	GuidanceUnavailable bool   `json:"guidance_unavailable,omitempty"`
}

func (c Client) ConnectionFacts() StatusFacts {
	status := StatusFacts{Channel: "feishu", Mode: "external_token", State: "unchecked"}
	if origin, err := url.Parse(c.Config.WorkspaceURL); err == nil && origin.Hostname() != "" {
		host := strings.TrimSuffix(strings.ToLower(origin.Hostname()), ".")
		ip, _ := netip.ParseAddr(host)
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || ip.Unmap().IsLoopback() {
			status.WorkspaceLinkScope = "same_device_only"
		} else if origin.Scheme == "https" {
			status.WorkspaceLinkScope = "configured_https"
		}
	}
	if c.Config.AppSecretRef != "" {
		status.Mode = "app_credentials"
	}
	if c.tokens != nil {
		c.tokens.mu.Lock()
		defer c.tokens.mu.Unlock()
		if !c.tokens.expires.IsZero() {
			expires := c.tokens.expires.UTC()
			status.ExpiresAt = &expires
		}
		if c.tokens.lastErr != nil {
			status.State = credentialState(c.tokens.lastErr)
		}
	}
	return status
}
func credentialState(err error) string {
	switch {
	case errors.Is(err, ErrCredentialsRejected), errors.Is(err, ErrAuthentication):
		return "credentials_rejected"
	case errors.Is(err, ErrCredentialsUnavailable):
		return "credentials_unavailable"
	default:
		return "unavailable"
	}
}

// Check only issues a credential and reads the bot's own information. It never
// changes bindings, sends a message or reconciles an unknown delivery.
func (c Client) CheckFacts(ctx context.Context) StatusFacts {
	status := c.ConnectionFacts()
	err := c.checkBot(ctx)
	if errors.Is(err, ErrAuthentication) && c.Config.AppSecretRef != "" {
		err = c.checkBot(ctx)
	} // A read-only query can safely refresh once.
	status = c.ConnectionFacts()
	if err != nil {
		status.State = credentialState(err)
		return status
	}
	status.State = "ready"
	return status
}
func (c Client) checkBot(ctx context.Context) error {
	resp, err := c.request(ctx, "GET", "/bot/v3/info/", "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		c.invalidate(resp)
		return ErrAuthentication
	}
	var reply struct {
		Code *int                       `json:"code"`
		Bot  map[string]json.RawMessage `json:"bot"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply) != nil {
		return ErrProtocol
	}
	code := -1
	if reply.Code != nil {
		code = *reply.Code
	}
	if authenticationRejected(resp.StatusCode, code) {
		c.invalidate(resp)
		return ErrAuthentication
	}
	if resp.StatusCode != 200 || reply.Code == nil || code != 0 || len(reply.Bot) == 0 {
		return ErrProtocol
	}
	return nil
}
