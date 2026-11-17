package config

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestArtifactChoicesPreserveExplicitAndAbsentDefaults(t *testing.T) {
	for _, c := range []struct {
		name, body string
		explicit   bool
	}{
		{"absent", "version: 1\n", false},
		{"hard-bounds-only", "version: 1\nartifact:\n  max_bytes: 1024\n", false},
		{"explicit-native-default", "version: 1\nartifact:\n  retention_days: 30\n  cleanup_batch: 100\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			config, err := Decode([]byte(c.body), noEnv)
			if err != nil {
				t.Fatal(err)
			}
			if (config.Artifact.RetentionDays != nil) != c.explicit || (config.Artifact.CleanupBatch != nil) != c.explicit {
				t.Fatal("default presence lost", config.Artifact)
			}
			if config.Artifact.LegacyPolicy().RetentionDays != 30 || config.Artifact.LegacyPolicy().CleanupBatch != 100 {
				t.Fatal("historical defaults changed")
			}
			encoded, err := yaml.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(encoded, noEnv)
			if err != nil {
				t.Fatal(err)
			}
			if (decoded.Artifact.RetentionDays != nil) != c.explicit || (decoded.Artifact.CleanupBatch != nil) != c.explicit {
				t.Fatal("config roundtrip invented explicit defaults")
			}
		})
	}
	for _, field := range []string{"retention_days", "cleanup_batch"} {
		if _, err := Decode([]byte("version: 1\nartifact:\n  "+field+": 0\n"), noEnv); err == nil {
			t.Fatal("explicit zero accepted", field)
		}
	}
	// A programmatic choice equal to the historical default is still explicit.
	days, batch := 30, 100
	choice := defaultArtifact()
	choice.RetentionDays = &days
	choice.CleanupBatch = &batch
	if err := choice.Validate(); err != nil || choice.RetentionDays == nil || *choice.RetentionDays != 30 {
		t.Fatal("explicit programmatic choice lost", choice, err)
	}
}
