package coding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

// ProjectEvidence identifies bytes captured after the container has stopped.
// It does not assert that those bytes prove the project's requirement.
type ProjectEvidence struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	Bytes         int64  `json:"bytes"`
	MediaType     string `json:"media_type"`
	ArtifactIndex int    `json:"artifact_index"`
}

type EvidenceFailure struct {
	Path      string `json:"path"`
	ErrorCode string `json:"error_code"`
}

// CaptureEvidence uses an anchored root after confirmed container removal. No
// stream can read the candidate after its deletion, and no Host path is returned.
// Original Artifact staging/authorization/atomic Tool commit remain authoritative.
func CaptureEvidence(ctx context.Context, directory string, names []string) ([]ProjectEvidence, []tool.AttachmentOutput, []EvidenceFailure, error) {
	if !contract.ValidEvidencePaths(names) {
		return nil, nil, nil, ErrInvalid
	}
	if len(names) == 0 {
		return nil, nil, nil, nil
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, nil, err
	}
	defer root.Close()
	var evidence []ProjectEvidence
	var attachments []tool.AttachmentOutput
	var failures []EvidenceFailure
	remaining := int64(16 << 20)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		data, code := captureEvidenceFile(root, name, min(8<<20, remaining))
		if code != "" {
			failures = append(failures, EvidenceFailure{Path: name, ErrorCode: code})
			continue
		}
		remaining -= int64(len(data))
		media := http.DetectContentType(data)
		evidence = append(evidence, ProjectEvidence{Path: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Bytes: int64(len(data)), MediaType: media, ArtifactIndex: len(attachments)})
		attachments = append(attachments, tool.AttachmentOutput{BinaryOutput: tool.BinaryOutput{Name: path.Base(name), MediaType: media, Reader: bytes.NewReader(data)}, Image: media == "image/png" || media == "image/jpeg"})
	}
	return evidence, attachments, failures, nil
}

func captureEvidenceFile(root *os.Root, name string, limit int64) ([]byte, string) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "NOT_FOUND"
	}
	if err != nil {
		return nil, "PROVIDER_UNAVAILABLE"
	}
	// Containers have been removed; the private directory has no untrusted
	// writers. Reject special files before open so a FIFO cannot block capture.
	if !info.Mode().IsRegular() {
		return nil, "INVALID_ARGUMENT"
	}
	file, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "NOT_FOUND"
	}
	if err != nil {
		return nil, "PROVIDER_UNAVAILABLE"
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, "PROVIDER_UNAVAILABLE"
	}
	if !info.Mode().IsRegular() {
		return nil, "INVALID_ARGUMENT"
	}
	if info.Size() > limit {
		return nil, "RESOURCE_LIMIT"
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "PROVIDER_UNAVAILABLE"
	}
	if int64(len(data)) > limit {
		return nil, "RESOURCE_LIMIT"
	}
	return data, ""
}
