package webaccess

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/tool"
)

// ReadContent is the frozen V1 native reader for pre-service Runs, their
// published web references and explicit unbundled carriers. Remove only when
// those retained consumers no longer require V1's 8000/400 window policy.
// Managed readers never fall back here on failure or deactivation.
func ReadContent(d Document, pageIndex, queryIndex, offset, limit int, find, mode string) (Slice, error) {
	text, err := EvidenceText(d, pageIndex, queryIndex)
	if err != nil {
		return Slice{}, err
	}
	if limit == 0 {
		limit = 8000
	}
	if offset < 0 || limit < 1 || limit > 32000 || offset > len(text) {
		return Slice{}, errors.New("INVALID_ARGUMENT")
	}
	var found *bool
	if find != "" {
		if len(find) > 500 {
			return Slice{}, errors.New("INVALID_ARGUMENT")
		}
		start, end, e := FindSpanV1(text, find, mode)
		if e != nil {
			return Slice{}, e
		}
		yes := start >= 0
		found = &yes
		if !yes {
			return Slice{TotalBytes: len(text), Found: found}, nil
		}
		offset = max(0, start-400)
		if end-offset > limit {
			limit = min(32000, end-offset+400)
		}
	}
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	end := min(len(text), offset+limit)
	for end < len(text) && end > offset && !utf8.RuneStart(text[end]) {
		end--
	}
	return Slice{Text: text[offset:end], Offset: offset, NextOffset: end, TotalBytes: len(text), More: end < len(text), Found: found}, nil
}

// Direct Plugin.Execute consumers are explicit unbundled V1 carriers.
func readEvidenceV1(ctx context.Context, host Host, args string) (tool.Result, error) {
	var input ReadArguments
	if json.Unmarshal([]byte(args), &input) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	raw, e := host.ReadEvidence(ctx, input.ResponseID)
	if e != nil {
		return tool.Failure(e.Error()), nil
	}
	var document Document
	if len(raw) > MaxEvidenceBytes || json.Unmarshal(raw, &document) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	result, e := ReadContent(document, input.URLIndex, input.QueryIndex, input.Offset, input.Limit, input.FindText, input.FindMode)
	if e != nil {
		return tool.Failure(e.Error()), nil
	}
	return ToolResult(result), nil
}
