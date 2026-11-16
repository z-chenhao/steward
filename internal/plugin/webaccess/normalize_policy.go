package webaccess

import (
	"context"
	"fmt"
	"strings"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const NormalizeContract = "steward.web-source-format/1"

type NormalizeRequest struct {
	Format string      `json:"format"`
	Text   string      `json:"text"`
	Rows   []SourceRow `json:"rows"`
}
type NormalizeChoice struct {
	Index        int `json:"index"`
	TitleBytes   int `json:"title_bytes"`
	ContentBytes int `json:"content_bytes"`
}
type NormalizePlan struct {
	Rows       []NormalizeChoice `json:"rows"`
	TextBytes  int               `json:"text_bytes"`
	LimitBytes int               `json:"limit_bytes"`
	ErrorCode  string            `json:"error_code"`
}

func normalizeService(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var reference struct {
		InputID string `json:"input_id"`
	}
	if err := rsiplugin.Decode(call.Input, &reference); err != nil {
		return rsiplugin.Result{}, err
	}
	if reference.InputID != "facts" || len(call.Inputs) != 1 || call.Inputs[0].ID != reference.InputID {
		return rsiplugin.Result{}, fmt.Errorf("missing source facts")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	var input NormalizeRequest
	if err := rsiplugin.DecodeInput(data, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	plan := NormalizePlan{Rows: []NormalizeChoice{}, LimitBytes: 20000}
	if input.Format == "exa" && len(input.Text) > plan.LimitBytes {
		plan.ErrorCode = "OUTPUT_LIMIT"
		return rsiplugin.JSONResult(ctx, plan)
	}
	var text strings.Builder
	for index, row := range input.Rows {
		if len(plan.Rows) >= 20 {
			break
		}
		title := Preview(row.Title, 1024)
		content := Preview(row.Content, 2048)
		plan.Rows = append(plan.Rows, NormalizeChoice{Index: index, TitleBytes: len(title), ContentBytes: len(content)})
		fmt.Fprintf(&text, "%s\n%s\n%s\n\n", title, row.URL, content)
	}
	value := text.String()
	if input.Format == "exa" {
		value = input.Text
	}
	plan.TextBytes = len(Preview(value, plan.LimitBytes))
	return rsiplugin.JSONResult(ctx, plan)
}
