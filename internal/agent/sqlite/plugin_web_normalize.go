package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var errWebFormatLimit = errors.New("OUTPUT_LIMIT")

func normalizeWebSource(ctx context.Context, h rsi.Host, revision string, facts webaccess.SourceObservation) (webaccess.SourceObservation, error) {
	if facts.Format == "" {
		return facts, nil
	} // original hosted model body, no wire-format recipe
	input, err := json.Marshal(webaccess.NormalizeRequest{Format: facts.Format, Text: facts.Text, Rows: facts.Rows})
	if err != nil {
		return webaccess.SourceObservation{}, err
	}
	// Freeze the exact released string bytes before validating returned ranges.
	var original webaccess.NormalizeRequest
	if json.Unmarshal(input, &original) != nil {
		return webaccess.SourceObservation{}, rsi.ErrInvalid
	}
	raw, err := h.ReadService(ctx, revision, webaccess.NormalizeContract, json.RawMessage(`{"input_id":"facts"}`), []rsi.InputData{{ID: "facts", Data: input}})
	if err != nil {
		return webaccess.SourceObservation{}, err
	}
	var plan webaccess.NormalizePlan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return webaccess.SourceObservation{}, rsi.ErrInvalid
	}
	value, err := materializeWebSource(original, plan)
	if err != nil {
		return webaccess.SourceObservation{}, err
	}
	value.RetrievedAt = facts.RetrievedAt
	return value, nil
}

// This only accepts original field prefixes/positions and encodes their V1
// record framing. It has no ranking,20-row,1024/2048-title/snippet or20k recipe.
func materializeWebSource(original webaccess.NormalizeRequest, plan webaccess.NormalizePlan) (webaccess.SourceObservation, error) {
	bad := func() (webaccess.SourceObservation, error) { return webaccess.SourceObservation{}, rsi.ErrInvalid }
	if original.Format != "json" && original.Format != "exa" {
		return bad()
	}
	if plan.ErrorCode != "" {
		if original.Format == "exa" && plan.ErrorCode == "OUTPUT_LIMIT" && plan.LimitBytes > 0 && len(original.Text) > plan.LimitBytes && len(plan.Rows) == 0 {
			return webaccess.SourceObservation{}, errWebFormatLimit
		}
		return bad()
	}
	result := webaccess.SourceObservation{Sources: []llm.WebSource{}}
	seen := map[int]bool{}
	var text strings.Builder
	for _, choice := range plan.Rows {
		if choice.Index < 0 || choice.Index >= len(original.Rows) || seen[choice.Index] {
			return bad()
		}
		seen[choice.Index] = true
		row := original.Rows[choice.Index]
		title, ok := webOriginalRange(row.Title, 0, choice.TitleBytes)
		if !ok {
			return bad()
		}
		content, ok := webOriginalRange(row.Content, 0, choice.ContentBytes)
		if !ok {
			return bad()
		}
		result.Sources = append(result.Sources, llm.WebSource{URL: row.URL, Title: title})
		fmt.Fprintf(&text, "%s\n%s\n%s\n\n", title, row.URL, content)
	}
	body := text.String()
	if original.Format == "exa" {
		body = original.Text
	}
	value, ok := webOriginalRange(body, 0, plan.TextBytes)
	if !ok {
		return bad()
	}
	result.Text = value
	return result, nil
}
