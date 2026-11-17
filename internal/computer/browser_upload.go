package computer

import (
	"context"
	"mime"
	"path"
	"strings"
	"unicode/utf16"
)

// BrowserUploadRequest is process-local Host authority, never a candidate DTO.
// The owner reconstructs resource identity and bytes from the original call.
type BrowserUploadRequest struct {
	Path, Navigation, Selector, Name, MediaType string
	TimeoutMS                                   int
}

type BrowserUploader interface {
	Upload(context.Context, Instance, Target, string, BrowserUploadRequest) (any, error)
}

func (r BrowserUploadRequest) Validate() error {
	if !SafeTransferPath(r.Path) || r.Navigation == "" || r.Selector == "" || len(utf16.Encode([]rune(r.Selector))) > 2048 || strings.Contains(r.Selector, ">>") || r.TimeoutMS < 1 || r.TimeoutMS > 10000 {
		return ErrInvalid
	}
	if !validUploadMetadata(r.Name, r.MediaType) {
		return ErrInvalid
	}
	return nil
}

func HasUploadPresentation(p ActionParameters) bool {
	return p.Name != "" && p.MediaType != ""
}

func validUploadPresentation(args []any, p ActionParameters) bool {
	// The published upload(file) API has no explicit filename/MIME options.
	// Extra fields on that original file object never become intent or grants.
	file := object(object(arg(args, 0))["file"])
	if p.NameFromSource && p.Name != path.Base(str(file["path"])) {
		return false
	}
	return validUploadMetadata(p.Name, p.MediaType)
}

func validUploadMetadata(name, media string) bool {
	// FileChooser buffers are not Artifact names or disk resource allocation.
	// Preserve every filename admitted by the original 512-byte transfer path,
	// including long UTF-8 / control characters other than its existing bans.
	_, _, err := mime.ParseMediaType(media)
	return err == nil && len(media) <= 128 && name != "" && name != "." && name != ".." && len(name) <= 512 && !strings.ContainsAny(name, "/\\\x00\r\n:")
}
