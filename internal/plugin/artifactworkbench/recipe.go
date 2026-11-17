package artifactworkbench

import (
	"errors"
	"mime"
	"strings"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
)

func Recipe(request contract.RecipeRequest) (contract.RecipePlan, error) {
	days := request.RetentionDays
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 3650 {
		return contract.RecipePlan{}, errors.New("invalid retention choice")
	}
	return contract.RecipePlan{RetentionDays: days, MediaKinds: []string{"image", "audio", "video"}, OverflowName: "tool-output.txt", OverflowMedia: "text/plain", StoredText: "Saved artifact.", PublishText: "Final artifacts selected."}, nil
}
func Read(request contract.ReadRequest) contract.ReadPlan {
	media, _, _ := mime.ParseMediaType(request.MediaType)
	if !strings.HasPrefix(media, "text/") && media != "application/json" && !strings.HasSuffix(media, "+json") {
		// The native binary path returns its reference before applying text range
		// rules, including a syntactically valid offset beyond a binary body.
		return contract.ReadPlan{Kind: "binary", Notice: "Binary content; use the artifact reference."}
	}
	limit := request.Limit
	if limit == 0 {
		limit = 4096
	}
	if limit < 1 || limit > 4096 || request.Offset < 0 || request.Offset > request.ByteSize {
		return contract.ReadPlan{Kind: "text", ErrorCode: "INVALID_ARGUMENT"}
	}
	return contract.ReadPlan{Kind: "text", Limit: limit}
}
