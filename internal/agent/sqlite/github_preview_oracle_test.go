package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestGitHubFollowupPreviewMatchesOriginal6Cases(t *testing.T) {
	raw, err := os.ReadFile("testdata/github-preview-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "80f79b7c1a83b4d5fb5714857a8328da3b75f2da68bea6a6aef0cddb315c62cd" {
		t.Fatal("original native preview oracle changed")
	}
	var cases []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) != 6 {
		t.Fatal("incomplete original preview oracle", err)
	}
	for i, test := range cases {
		if actual := githubContextPreviewLegacyV1(test.Input); actual != test.Output {
			t.Errorf("original preview case %d changed", i)
		}
	}
}
