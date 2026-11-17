package computer

import (
	"path"

	"github.com/z-chenhao/assistant/internal/artifact"
)

// A candidate sees explicit display metadata only, never a path, ComputerFile
// grant, ArtifactRef, bytes or original opaque filename-derived resource token.
func TransferPresentationArguments(args []any) []any {
	original := object(arg(args, 0))
	return []any{map[string]any{"name": str(original["name"]), "mediaType": str(original["mediaType"])}}
}

// Source-name resolution is a fixed Host projection of the original path. The
// complete resulting display choice is committed before the original approval.
func ResolveTransferPresentation(method string, args []any, p ActionParameters) ActionParameters {
	if p.NameFromSource && p.Name == "" && p.Failure == "" {
		switch method {
		case "exportArtifact":
			p.Name = path.Base(str(object(arg(args, 0))["path"]))
		case "playwright.upload":
			p.Name = path.Base(str(object(object(arg(args, 0))["file"])["path"]))
		}
	}
	return p
}

func validTransferPresentation(args []any, p ActionParameters) bool {
	original := object(arg(args, 0))
	explicitName, explicitMedia := str(original["name"]), str(original["mediaType"])
	if explicitName != "" {
		if p.NameFromSource || p.Name != explicitName {
			return false
		}
	} else if p.NameFromSource {
		if p.Name != path.Base(str(original["path"])) {
			return false
		}
	} else if (artifact.Input{Name: p.Name, MediaType: "application/octet-stream"}).Validate() != nil {
		return false
	}
	if explicitMedia != "" {
		if p.MediaType != explicitMedia {
			return false
		}
	} else if (artifact.Input{Name: "output", MediaType: p.MediaType}).Validate() != nil {
		return false
	}
	q := p
	q.NameFromSource = false
	q.Name = ""
	q.MediaType = ""
	return emptyActionParameters(q)
}
