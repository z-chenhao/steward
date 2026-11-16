package webaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/z-chenhao/assistant/internal/webcontent"
)

type Page = webcontent.Page

type Document struct {
	Version  int            `json:"version"`
	Queries  []SearchResult `json:"queries,omitempty"`
	Pages    []Page         `json:"pages,omitempty"`
	Claim    string         `json:"claim,omitempty"`
	Status   string         `json:"status,omitempty"`
	Passages []Passage      `json:"passages,omitempty"`
}
type Passage struct {
	URL         string `json:"url"`
	Text        string `json:"text"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	ContentHash string `json:"content_hash"`
	SourceHash  string `json:"source_hash"`
}
type Slice struct {
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	More       bool   `json:"has_more"`
	Found      *bool  `json:"found,omitempty"`
}

func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EvidenceText interprets the published V1 page/query selector. It is part of
// reference identity, not the candidate's window-size or context policy.
func EvidenceText(d Document, pageIndex, queryIndex int) (string, error) {
	var text string
	if d.Version != 1 || len(d.Pages) > 40 || len(d.Queries) > 8 {
		return "", errors.New("INVALID_ARGUMENT")
	}
	if len(d.Pages) > 0 {
		if pageIndex < 0 || pageIndex >= len(d.Pages) {
			return "", errors.New("NOT_FOUND")
		}
		text = d.Pages[pageIndex].Text
	} else {
		if queryIndex < 0 || queryIndex >= len(d.Queries) {
			return "", errors.New("NOT_FOUND")
		}
		raw, _ := json.Marshal(d.Queries[queryIndex])
		text = string(raw)
	}
	return text, nil
}

// FindSpanV1 preserves the published matching modes and original byte spans.
func FindSpanV1(text, pattern, mode string) (int, int, error) {
	if mode == "exact" {
		i := strings.Index(text, pattern)
		if i < 0 {
			return -1, -1, nil
		}
		return i, i + len(pattern), nil
	}
	if mode != "" && mode != "case-insensitive" && mode != "fuzzy" {
		return 0, 0, errors.New("INVALID_ARGUMENT")
	}
	var normalized strings.Builder
	positions := make([]int32, 0, len(text)+1)
	for index, r := range text {
		lower := string(unicode.ToLower(r))
		normalized.WriteString(lower)
		for range len(lower) {
			positions = append(positions, int32(index))
		}
	}
	positions = append(positions, int32(len(text)))
	lower := normalized.String()
	needle := strings.Map(unicode.ToLower, pattern)
	if mode != "fuzzy" {
		i := strings.Index(lower, needle)
		if i < 0 {
			return -1, -1, nil
		}
		return int(positions[i]), int(positions[i+len(needle)]), nil
	}
	words := strings.Fields(needle)
	if len(words) == 0 {
		return -1, -1, nil
	}
	start, cursor := -1, 0
	for _, word := range words {
		i := strings.Index(lower[cursor:], word)
		if i < 0 {
			return -1, -1, nil
		}
		i += cursor
		if start < 0 {
			start = i
		}
		cursor = i + len(word)
	}
	return int(positions[start]), int(positions[cursor]), nil
}

func Preview(text string, limit int) string { return webcontent.Preview(text, limit) }
