package sqlite

import (
	"mime"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
)

// Retained pre-recipe Runs and their descendants keep the original output
// metadata/receipt interpretation. Remove with those consumers after retirement;
// never use this to recover a missing managed preparation after an effect.
func legacyArtifactRecipe(retention int) contract.RecipePlan {
	return contract.RecipePlan{RetentionDays: retention, MediaKinds: []string{"image", "audio", "video"}, OverflowName: "tool-output.txt", OverflowMedia: "text/plain", StoredText: "Saved artifact.", PublishText: "Final artifacts selected."}
}

// Historical consumer: retained Runs whose immutable service list predates
// steward.artifact-read/1 and descendants that inherit that original list.
// Delete only when those executions are explicitly retired. Managed reads do
// not call this interpreter when their producer is missing, failed or disabled.
func legacyArtifactReadPolicy(request artifactReadRequest, descriptor artifact.Descriptor) contract.ReadPlan {
	media, _, _ := mime.ParseMediaType(descriptor.MediaType)
	if !strings.HasPrefix(media, "text/") && media != "application/json" && !strings.HasSuffix(media, "+json") {
		return contract.ReadPlan{Kind: "binary", Notice: "Binary content; use the artifact reference."}
	}
	limit := request.Limit
	if limit == 0 {
		limit = 4096
	}
	if limit < 1 || limit > 4096 || request.Offset < 0 || request.Offset > descriptor.ByteSize {
		return contract.ReadPlan{Kind: "text", ErrorCode: "INVALID_ARGUMENT"}
	}
	return contract.ReadPlan{Kind: "text", Limit: limit}
}
