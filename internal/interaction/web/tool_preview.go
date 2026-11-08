package web

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/tool"
)

type toolOutputView struct {
	Texts     []string
	Artifacts []string
	Images    []string
	Error     *tool.Error
}

// Only decode the Host receipt and the known CodeMode content-block frame.
// Business JSON strings remain evidence; do not recursively guess their schema.
func previewToolOutput(name string, body observability.Body, offset int) *toolOutputView {
	if !body.Available || body.Next != nil || offset != 0 {
		return nil
	}
	var result tool.Result
	if json.Unmarshal([]byte(body.Content), &result) != nil || strings.HasPrefix(result.Text, "sealed:") {
		return nil
	}
	out := &toolOutputView{Texts: []string{result.Text}, Artifacts: result.ArtifactRefs, Images: result.ImageRefs, Error: result.Error}
	if name == "builtin__codemode_execute" {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		decoder := json.NewDecoder(bytes.NewBufferString(result.Text))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&blocks) == nil && decoder.Decode(new(any)) == io.EOF && len(blocks) > 0 {
			texts := []string{}
			for _, block := range blocks {
				if block.Type != "text" {
					return out
				}
				texts = append(texts, block.Text)
			}
			out.Texts = texts
		}
	}
	return out
}
