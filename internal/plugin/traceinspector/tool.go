package traceinspector

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
)

// ToolPreview unwraps only a saved receipt and the known Code Mode frame.
// Arbitrary business JSON strings and sealed private data are not reinterpreted.
func ToolPreview(f contract.ToolFacts) *contract.ToolPreview {
	if !f.Available || !f.Complete || f.Offset != 0 {
		return nil
	}
	var receipt struct {
		Text      string              `json:"text"`
		Artifacts []string            `json:"artifact_refs"`
		Images    []string            `json:"image_refs"`
		Error     *contract.ToolError `json:"error"`
	}
	if json.Unmarshal([]byte(f.Content), &receipt) != nil || strings.HasPrefix(receipt.Text, "sealed:") {
		return nil
	}
	out := &contract.ToolPreview{Texts: []string{receipt.Text}, Artifacts: append([]string{}, receipt.Artifacts...), Images: append([]string{}, receipt.Images...), Error: receipt.Error}
	if f.Name == "builtin__codemode_execute" {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		d := json.NewDecoder(bytes.NewBufferString(receipt.Text))
		d.DisallowUnknownFields()
		if d.Decode(&blocks) == nil && d.Decode(new(any)) == io.EOF && len(blocks) > 0 {
			texts := []string{}
			for _, b := range blocks {
				if b.Type != "text" {
					return out
				}
				texts = append(texts, b.Text)
			}
			out.Texts = texts
		}
	}
	return out
}
