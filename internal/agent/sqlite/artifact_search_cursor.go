package sqlite

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
)

type artifactSearchPage struct {
	Artifacts  []artifact.Descriptor `json:"artifacts"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

type artifactSearchCursor struct {
	Key       string
	CreatedAt int64
	Ref       string
}

func artifactSearchKey(run, graph, query string, content bool) string {
	parts := []string{run, graph, query}
	if content {
		parts = append(parts, "body")
	}
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (cursor artifactSearchCursor) encode() string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func readArtifactSearchCursor(key string, raw *string) (artifactSearchCursor, error) {
	var c artifactSearchCursor
	if raw == nil || *raw == "" {
		return c, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(*raw)
	if err != nil || json.Unmarshal(data, &c) != nil || c.Key != key || c.CreatedAt < 0 || !strings.HasPrefix(c.Ref, "artifact:") || len(c.Ref) != len("artifact:")+64 {
		return artifactSearchCursor{}, artifact.ErrInvalid
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(c.Ref, "artifact:")); err != nil {
		return artifactSearchCursor{}, artifact.ErrInvalid
	}
	return c, nil
}
