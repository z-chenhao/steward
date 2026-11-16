// Package artifact owns opaque resource references and trusted access scopes.
package artifact

import (
	"errors"
	"mime"
	"strings"
	"time"
	"unicode"
)

// Scope is constructed by the Host, never decoded from model arguments. The
// local installation has one user principal; Agent and Task remain independent
// authorization boundaries. Owner grants access to the local CLI/user only.
type Scope struct {
	UserID, AgentID, TaskID, RunID string
	Owner                          bool
}

const LocalUser = "local"

type Input struct {
	// RunOnly is a Host choice for private execution evidence, never a model field.
	RunOnly   bool      `json:"-"`
	Creation  *Creation `json:"-"`
	Name      string    `json:"name"`
	MediaType string    `json:"media_type"`
}

type Descriptor struct {
	// Accepted Host facts are private to adapters; public descriptor JSON and
	// frozen model receipts keep their original contract.
	SemanticKind string     `json:"-"`
	MethodGraph  string     `json:"-"`
	Ref          string     `json:"artifact_ref"`
	Name         string     `json:"name"`
	MediaType    string     `json:"media_type"`
	ByteSize     int64      `json:"byte_size"`
	ContentHash  string     `json:"content_hash"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	Version      int64      `json:"version"`
}

// Creation is a Host-prepared immutable choice, never a model/HTTP field or a
// plugin capability. It carries only the facts needed by the storage consumer.
type Creation struct {
	GraphRevision string
	RetentionDays int
	MediaKinds    []string
	AgentID       string
	PrivacyEpoch  int64
}

func (c Creation) Valid() bool {
	if len(c.GraphRevision) != 71 || !strings.HasPrefix(c.GraphRevision, "sha256:") || c.RetentionDays < 1 || c.RetentionDays > 3650 || c.AgentID == "" || c.PrivacyEpoch < 0 {
		return false
	}
	for _, r := range c.GraphRevision[7:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	seen := map[string]bool{}
	for _, kind := range c.MediaKinds {
		if seen[kind] || kind != "image" && kind != "audio" && kind != "video" {
			return false
		}
		seen[kind] = true
	}
	return true
}

func (c Creation) Kind(mediaType string) string {
	media, _, _ := mime.ParseMediaType(mediaType)
	for _, kind := range c.MediaKinds {
		if strings.HasPrefix(media, kind+"/") {
			return kind
		}
	}
	return "file"
}

func (d Descriptor) ContentType() string {
	if d.SemanticKind != "" {
		return d.SemanticKind
	}
	return legacyContentType(d.MediaType) // Explicit original NULL-method reference.
}

type Policy struct {
	RetentionDays int   `yaml:"retention_days"`
	QuotaBytes    int64 `yaml:"quota_bytes"`
	MaxBytes      int64 `yaml:"max_bytes"`
	CleanupBatch  int   `yaml:"cleanup_batch"`
}

func DefaultPolicy() Policy {
	return Policy{RetentionDays: 30, QuotaBytes: 1 << 30, MaxBytes: 64 << 20, CleanupBatch: 100}
}
func (p Policy) Validate() error {
	if p.RetentionDays < 1 || p.RetentionDays > 3650 || p.MaxBytes < 1 || p.MaxBytes > 1<<30 || p.QuotaBytes < p.MaxBytes || p.CleanupBatch < 1 || p.CleanupBatch > 10000 {
		return ErrInvalid
	}
	return nil
}

var (
	ErrNotFound    = errors.New("NOT_FOUND")
	ErrInvalid     = errors.New("INVALID_ARGUMENT")
	ErrLimit       = errors.New("RESOURCE_LIMIT")
	ErrConflict    = errors.New("artifact version conflict")
	ErrReferenced  = errors.New("artifact is referenced")
	ErrIntegrity   = errors.New("artifact integrity check failed")
	ErrStorage     = errors.New("artifact storage unavailable")
	ErrUnavailable = errors.New("artifact policy unavailable")
)

// CleanupReport is an operational view; object paths and storage keys remain private.
type CleanupReport struct {
	DryRun    bool     `json:"dry_run"`
	Artifacts []string `json:"artifacts"`
	Objects   int      `json:"orphan_objects"`
}

// Main delivery needs one text part in the 128-part Conversation envelope.
const MaxPublished = 127

// Validate enforces output metadata invariants shared by artifact staging and
// pre-approval Computer presentation. Explicit metadata keeps its original value.
func (in Input) Validate() error {
	_, _, err := mime.ParseMediaType(in.MediaType)
	if err != nil || len(in.MediaType) > 128 || in.Name == "" || in.Name == "." || in.Name == ".." || len(in.Name) > 255 || strings.ContainsAny(in.Name, "/\\") || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return ErrInvalid
	}
	return nil
}
