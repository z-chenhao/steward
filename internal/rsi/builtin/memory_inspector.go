package builtin

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Captured original navigation/view/style/copy and independently reviewed Worker
// bytes. These are Owner acceptance expectations, never candidate-supplied policy.
// Worker semantics additionally require the captured original client regressions.
//
//go:embed memory-inspector-owner-v1.json
var memoryInspectorAcceptance []byte

// MemoryInspectorCases selects the Owner-reviewed resource pins for the sealed
// version. Frozen 1.0 packages retain their original captured expectations.
func MemoryInspectorCases(version, directory string) ([]rsi.Case, error) {
	var cases []rsi.Case
	if err := json.Unmarshal(memoryInspectorAcceptance, &cases); err != nil {
		return nil, err
	}
	if version == "1.0.0" {
		return cases, nil
	}
	if version != "1.1.0" && version != "1.1.1" {
		return nil, rsi.ErrInvalid
	}
	read := func(path, digest string) (string, error) {
		raw, err := os.ReadFile(filepath.Join(directory, path))
		if err != nil || rsiplugin.FileDigest(raw) != digest {
			return "", rsi.ErrInvalid
		}
		return string(raw), nil
	}
	viewDigest := "sha256:6c58a942eb2f771ee8741b2ca83aa394f3b0f8c676cdfc6684cb96b2ee98a507"
	copyDigest := "sha256:da5b35fb6a31d5d4baea529a631c9a1d692f8b5a91191c3e45786ac3160797a4"
	if version == "1.1.1" {
		viewDigest = "sha256:5469dc1526f9d8bb80cad2e9cc961f48a7c710c14066b750c3b06b9f198804da"
		copyDigest = "sha256:fca717dc0a5731eb48be34df444365216c1a7a9fbc425ee0d0ee5a696f579eba"
	}
	view, err := read(contract.ViewFile, viewDigest)
	if err != nil {
		return nil, err
	}
	copy, err := read(contract.LocalizationFile, copyDigest)
	if err != nil {
		return nil, err
	}
	for i := range cases {
		var expected contract.Result
		if err := json.Unmarshal(cases[i].Expected, &expected); err != nil {
			return nil, err
		}
		expected.CorrectionDraft = version == "1.1.1"
		expected.View.Source, expected.View.Digest = view, viewDigest
		expected.Localization.Source, expected.Localization.Digest = copy, copyDigest
		if i == 1 {
			// Preserve captured JSON field presence, particularly unset timestamps.
			var input struct {
				Navigation []json.RawMessage `json:"navigation"`
			}
			if err := json.Unmarshal(cases[i].Input, &input); err != nil {
				return nil, err
			}
			input.Navigation = append(input.Navigation, json.RawMessage(`{"query":{"view":"maintenance","ref":"memory:maintenance-receipt"},"key":"ref","value":"memory:changed-note"}`))
			cases[i].Input, err = json.Marshal(input)
			if err != nil {
				return nil, err
			}
			expected.URLs = append(expected.URLs, "/developer/memory?ref=memory%3Achanged-note")
		}
		cases[i].Expected, err = json.Marshal(expected)
		if err != nil {
			return nil, err
		}
	}
	return cases, nil
}
