package environment

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/z-chenhao/assistant/internal/tool"
)

// FileChange is private Host-to-driver data, reconstructed from the original
// approved invocation. A pure plugin may choose only the verified edit span.
type FileChange struct {
	Operation  string  `json:"operation"`
	Path       string  `json:"path"`
	BeforeHash *string `json:"before_hash,omitempty"`
	OldText    string  `json:"old_text,omitempty"`
	NewText    string  `json:"new_text"`
	Content    string  `json:"content"`
	Start      int     `json:"start"`
}

//go:embed file_change.py
var changeProgram string

func FileChangeProgram() (string, error) { return nativeDriverProgram(changeProgram) }

func FileDriverFingerprint() (string, error) {
	snapshot, err := FileSnapshotProgram()
	if err != nil {
		return "", err
	}
	change, err := FileChangeProgram()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("steward-file-driver/1:%x", sha256.Sum256([]byte(snapshot+"\x00"+change))), nil
}

func FileChangeRequest(root string, change FileChange) ([]byte, error) {
	return json.Marshal(struct {
		Root   string     `json:"root"`
		Change FileChange `json:"change"`
	}{root, change})
}

func (l Local) ApplyFile(ctx context.Context, change FileChange) tool.Result {
	if failure := l.prepareWorkspace(); failure.Error != nil {
		return failure
	}
	program, err := FileChangeProgram()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
	request, err := FileChangeRequest(l.Config.Root, change)
	if err != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	raw, failure := l.runProgram(ctx, program, request, false)
	if failure.Error != nil {
		return failure
	}
	return Response(raw)
}
