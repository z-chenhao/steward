package environment

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
)

// FileSnapshot is a private Host-driver value. Pure plugins receive authorized
// bytes through an input descriptor, never this driver or a workspace handle.
type FileSnapshot struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

//go:embed file_snapshot.py
var snapshotProgram string

// FileSnapshotProgram reuses the original trusted descriptor/regular-file
// primitives without changing historical Source bytes or ResourceHash.
func FileSnapshotProgram() (string, error) {
	return nativeDriverProgram(snapshotProgram)
}

func nativeDriverProgram(body string) (string, error) {
	prefix, _, ok := strings.Cut(Source, "\ntry:\n    print(json.dumps({'value': run(json.load(sys.stdin))}, ensure_ascii=True))")
	if !ok {
		return "", os.ErrInvalid
	}
	return prefix + "\n" + body, nil
}

// FileSnapshotRequest is constructed by the owning Host adapter after the
// original Tool invocation has selected its workspace and exact path.
func FileSnapshotRequest(root, path string) ([]byte, error) {
	return json.Marshal(struct {
		Root string `json:"root"`
		Path string `json:"path"`
	}{root, path})
}

func DecodeFileSnapshot(raw []byte) (FileSnapshot, *tool.Error) {
	var wire struct {
		Value *FileSnapshot `json:"value"`
		Error string        `json:"error"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || decoder.Decode(new(any)) != io.EOF || (wire.Value == nil) == (wire.Error == "") {
		return FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	if wire.Error != "" {
		switch wire.Error {
		case "RESOURCE_LIMIT", "INVALID_ARGUMENT", "PERMISSION_DENIED":
			return FileSnapshot{}, tool.Failure(wire.Error).Error
		default:
			return FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
		}
	}
	value := *wire.Value
	if len(value.Data) > 8<<20 || (!value.Exists && (len(value.Data) != 0 || value.Hash != "")) || (value.Exists && value.Hash != fmt.Sprintf("%x", sha256.Sum256(value.Data))) {
		return FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	return value, nil
}

func (l Local) SnapshotFile(ctx context.Context, path string) (FileSnapshot, *tool.Error) {
	if failure := l.prepareWorkspace(); failure.Error != nil {
		return FileSnapshot{}, failure.Error
	}
	program, err := FileSnapshotProgram()
	if err != nil {
		return FileSnapshot{}, tool.Failure("PROVIDER_UNAVAILABLE").Error
	}
	request, err := FileSnapshotRequest(l.Config.Root, path)
	if err != nil {
		return FileSnapshot{}, tool.Failure("INVALID_ARGUMENT").Error
	}
	raw, failure := l.runProgram(ctx, program, request, true)
	if failure.Error != nil {
		return FileSnapshot{}, failure.Error
	}
	return DecodeFileSnapshot(raw)
}
