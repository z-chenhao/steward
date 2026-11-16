package config

import "github.com/z-chenhao/assistant/internal/artifact"

// Artifact preserves presence for replaceable business choices. Nil means the
// selected plugin supplies its default; an explicit zero remains invalid. Hard
// size/quota bounds are always concrete Host settings. This is an internal Go
// configuration type; the existing YAML names and owner semantics stay fixed.
type Artifact struct {
	RetentionDays *int  `yaml:"retention_days,omitempty"`
	QuotaBytes    int64 `yaml:"quota_bytes"`
	MaxBytes      int64 `yaml:"max_bytes"`
	CleanupBatch  *int  `yaml:"cleanup_batch,omitempty"`
}

func defaultArtifact() Artifact {
	p := artifact.DefaultPolicy()
	return Artifact{QuotaBytes: p.QuotaBytes, MaxBytes: p.MaxBytes}
}

// LegacyPolicy interprets explicit pre-plugin storage consumers only. Managed
// creation and cleanup receive the original optional choices, never these
// filled native defaults. It also centralizes the unchanged hard-bound checks.
func (a Artifact) LegacyPolicy() artifact.Policy {
	p := artifact.DefaultPolicy()
	p.QuotaBytes, p.MaxBytes = a.QuotaBytes, a.MaxBytes
	if a.RetentionDays != nil {
		p.RetentionDays = *a.RetentionDays
	}
	if a.CleanupBatch != nil {
		p.CleanupBatch = *a.CleanupBatch
	}
	return p
}

func (a Artifact) Validate() error { return a.LegacyPolicy().Validate() }
