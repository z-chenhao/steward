package computer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComputerTransferPresentationOriginalMetadataAndNoResourceRelease(t *testing.T) {
	args := []any{map[string]any{"path": "imports/PRIVATE_FILE_ID", "artifactRef": "artifact:PRIVATE_ARTIFACT", "computerId": "PRIVATE_COMPUTER", "name": "", "mediaType": ""}}
	raw, _ := json.Marshal(TransferPresentationArguments(args))
	if strings.Contains(string(raw), "PRIVATE_") || string(raw) != `[{"mediaType":"","name":""}]` {
		t.Fatal("transfer plan released a resource", string(raw))
	}
	p := ResolveTransferPresentation("exportArtifact", args, ActionParameters{NameFromSource: true, MediaType: "application/octet-stream"})
	if p.Name != "PRIVATE_FILE_ID" || !ValidActionParameters("exportArtifact", "", args, p) {
		t.Fatal("Host did not resolve original source name", p)
	}
	for _, c := range []struct {
		input map[string]any
		p     ActionParameters
		valid bool
	}{
		{map[string]any{"path": "imports/original"}, ActionParameters{Name: "alternate.bin", MediaType: "application/octet-stream"}, true},
		{map[string]any{"path": "imports/original"}, ActionParameters{NameFromSource: true, Name: "replacement", MediaType: "application/octet-stream"}, false},
		{map[string]any{"path": "imports/original", "name": "explicit.txt"}, ActionParameters{Name: "other.txt", MediaType: "text/plain"}, false},
		{map[string]any{"path": "imports/original", "name": "explicit.txt", "mediaType": "text/plain"}, ActionParameters{Name: "explicit.txt", MediaType: "application/json"}, false},
		{map[string]any{"path": "imports/original"}, ActionParameters{Name: "../escape", MediaType: "application/octet-stream"}, false},
		{map[string]any{"path": "imports/original"}, ActionParameters{Name: "export.bin", MediaType: "not a media type"}, false},
		{map[string]any{"path": "imports/original"}, ActionParameters{Name: "export.bin", MediaType: "text/plain", URL: "https://injected"}, false},
	} {
		if got := ValidActionParameters("exportArtifact", "", []any{c.input}, c.p); got != c.valid {
			t.Fatal(c, got)
		}
	}
}
