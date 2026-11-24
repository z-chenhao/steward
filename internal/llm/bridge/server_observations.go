package bridge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm/provider"
)

func imageNotice(w, h, pw, ph int) string {
	return fmt.Sprintf("Image original coordinate size: %d x %d; preview: %d x %d. Use original coordinates for screenshot actions.", w, h, pw, ph)
}
func (s *Server) observe(r generationRequest, o provider.Observation) {
	if !r.Observe || o.Attempt == "" {
		return
	}
	value := map[string]any{"generation_id": r.GenerationID, "attempt": o.Attempt, "phase": o.Phase, "at": o.At.UnixMicro(), "format": "steward-wire-v1/go-ai-v1"}
	// Timing milestones carry only time and attempt identity in the Host
	// protocol; status and errors belong to HTTP outcome observations.
	if o.Phase == "started" || o.Phase == "headers" || o.Phase == "error" {
		value["http_status"] = o.Status
		value["error_code"] = o.Error
	}
	if o.Phase == "started" {
		var body any
		if json.Unmarshal(o.Body, &body) != nil {
			return
		}
		clean := redact(body, r)
		raw, err := json.Marshal(clean)
		if err != nil {
			return
		}
		value["body_hash"] = digestString(string(raw))
		if len(raw) <= 256<<10 {
			value["body"] = clean
		} else {
			value["omitted"] = "sanitized body exceeds 256 KiB"
		}
	}
	s.send(map[string]any{"jsonrpc": "2.0", "method": "generation.observation", "params": value})
}

var sensitive = regexp.MustCompile(`(?i)^(authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|cookie|set-cookie)$`)
var secretValuePattern = regexp.MustCompile(`(Bearer\s+[A-Za-z0-9._~+/=-]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----|sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,})`)

func redact(value any, r generationRequest) any {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "data:") {
			_, data, _ := strings.Cut(v, ",")
			decoded, _ := base64.StdEncoding.DecodeString(data)
			out := map[string]any{"media_ref": nil, "sha256": digestString(string(decoded)), "redacted": "image pixels"}
			for ref, image := range r.Images {
				if image.Data == data {
					out["media_ref"] = ref
					out["original_width"] = image.OriginalWidth
					out["original_height"] = image.OriginalHeight
					out["preview_width"] = image.PreviewWidth
					out["preview_height"] = image.PreviewHeight
					break
				}
			}
			return out
		}
		for _, secret := range []string{r.Connection.APIKey, r.Connection.AccessToken} {
			if secret != "" {
				v = strings.ReplaceAll(v, secret, "[credential redacted]")
			}
		}
		return secretValuePattern.ReplaceAllString(v, "[credential redacted]")
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = redact(x, r)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if sensitive.MatchString(k) {
				out[k] = "[credential redacted]"
			} else {
				out[k] = redact(x, r)
			}
		}
		return out
	}
	return value
}
