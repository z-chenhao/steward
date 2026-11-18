package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// JSON executes a bounded, cancellable provider JSON request using the same
// credentials, hooks and retry policy as chat. Credentials never enter errors.
func (c *Client) JSON(ctx context.Context, m Model, method, path string, body map[string]any, o Options) (map[string]any, error) {
	o = o.Resolve(m)
	m = resolveModel(m, o)
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	base, err := url.Parse(strings.TrimRight(m.BaseURL, "/") + path)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.Fragment != "" {
		return nil, ErrIncompatible
	}
	if o.OnPayload != nil && body != nil {
		if err := o.OnPayload(body); err != nil {
			return nil, err
		}
	}
	var raw []byte
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, ErrIncompatible
		}
	}
	client := c.directHTTPClient(o.HTTPClient)
	retries := o.MaxRetries
	if retries < 0 {
		retries = 0
	}
	for attempt := 0; attempt <= retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(raw))
		if err != nil {
			return nil, ErrIncompatible
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		key := o.APIKey
		if o.AccessToken != "" {
			key = o.AccessToken
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		for k, v := range m.Headers {
			req.Header.Set(k, v)
		}
		for k, v := range o.Headers {
			req.Header.Set(k, v)
		}
		if m.API == AzureResponses {
			req.Header.Del("Authorization")
			if o.AccessToken != "" {
				req.Header.Set("Authorization", "Bearer "+o.AccessToken)
			} else {
				req.Header.Set("api-key", o.APIKey)
			}
			if o.APIVersion != "" {
				q := req.URL.Query()
				q.Set("api-version", o.APIVersion)
				req.URL.RawQuery = q.Encode()
			}
		}
		if m.Provider == "cloudflare-ai-gateway" {
			req.Header.Del("Authorization")
			req.Header.Set("cf-aig-authorization", "Bearer "+key)
		}
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < retries {
				if err := retryWait(ctx, attempt, 0); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &Error{Code: "PROVIDER_UNAVAILABLE"}
		}
		if o.OnResponse != nil {
			o.OnResponse(res)
		}
		limit := o.MaxResponseBytes
		if limit == 0 {
			limit = 64 << 20
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			failure := responseError(res, io.LimitReader(res.Body, limit))
			res.Body.Close()
			retry := res.StatusCode == 429 || res.StatusCode == 500 || res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504
			if m.API == OpenAIDecisions && res.StatusCode == 504 {
				retry = false
			}
			if attempt < retries && retry {
				if o.MaxRetryDelay > 0 && failure.RetryAfter > o.MaxRetryDelay {
					return nil, failure
				}
				if err := retryWait(ctx, attempt, failure.RetryAfter); err != nil {
					return nil, err
				}
				continue
			}
			return nil, failure
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, limit+1))
		res.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &Error{Code: "PROVIDER_UNAVAILABLE"}
		}
		if int64(len(data)) > limit {
			return nil, ErrInvalidResponse
		}
		var result map[string]any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		if d.Decode(&result) != nil || result == nil {
			return nil, ErrInvalidResponse
		}
		var trailing any
		if d.Decode(&trailing) != io.EOF {
			return nil, ErrInvalidResponse
		}
		return result, nil
	}
	return nil, &Error{Code: "PROVIDER_UNAVAILABLE"}
}

func (c *Client) FetchDeferred(ctx context.Context, m Model, h DeferredHandle, o Options) (AssistantMessage, error) {
	return c.deferred(ctx, m, h, o, false)
}
func (c *Client) CancelDeferred(ctx context.Context, m Model, h DeferredHandle, o Options) (AssistantMessage, error) {
	return c.deferred(ctx, m, h, o, true)
}
func (c *Client) deferred(ctx context.Context, m Model, h DeferredHandle, o Options, cancel bool) (AssistantMessage, error) {
	if (m.API != OpenAIResponses && m.API != AzureResponses && m.API != CodexResponses) || h.API != m.API || h.ID == "" {
		return AssistantMessage{}, ErrIncompatible
	}
	path := "/responses/" + url.PathEscape(h.ID)
	method := http.MethodGet
	if cancel {
		path += "/cancel"
		method = http.MethodPost
	}
	response, err := c.JSON(ctx, m, method, path, nil, o)
	if err != nil {
		return AssistantMessage{}, err
	}
	s := newStreamState(m, nil)
	s.message.ResponseID = str(response["id"])
	status := str(response["status"])
	if cancel && status == "cancelled" {
		s.message.StopReason = "aborted"
		return s.message, nil
	}
	if status == "queued" || status == "in_progress" {
		s.message.StopReason = "deferred"
		return s.message, nil
	}
	if status != "completed" {
		return s.message, ErrInvalidResponse
	}
	for _, value := range list(response["output"]) {
		item := record(value)
		switch item["type"] {
		case "message":
			for _, part := range list(item["content"]) {
				p := record(part)
				if p["type"] == "output_text" {
					s.text(str(p["text"]), false)
				}
			}
		case "function_call":
			key := len(s.tools)
			p := s.tool(key, str(item["call_id"]), str(item["name"]), str(item["namespace"]))
			p.arguments.WriteString(str(item["arguments"]))
		case "reasoning":
			for _, part := range list(item["summary"]) {
				s.text(str(record(part)["text"]), true)
			}
		}
	}
	if err := s.usage(record(response["usage"]), m.API); err != nil {
		return s.message, err
	}
	CalculateCost(m, &s.message.Usage)
	return s.message, s.finish()
}
