package coding

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

// ImportLimits narrows complete source admission; it never selects file names.
// The original entry points retain these exact native ceilings for old work.
type ImportLimits struct {
	Entries   int `json:"entries"`
	FileBytes int `json:"file_bytes"`
	TreeBytes int `json:"tree_bytes"`
}

func NativeImportLimits() ImportLimits {
	return ImportLimits{Entries: maxFiles, FileBytes: 8 << 20, TreeBytes: maxBytes}
}
func (l ImportLimits) Valid() bool {
	return l.Entries >= 1 && l.Entries <= maxFiles && l.FileBytes >= 1 && l.FileBytes <= 8<<20 && l.TreeBytes >= 1 && l.TreeBytes <= maxBytes
}

//go:embed workspace.go
var workspaceDriver string

//go:embed limits.go
var limitsDriver string

func DriverFingerprint() string {
	return fmt.Sprintf("steward-coding-driver/1:%x", sha256.Sum256([]byte(workspaceDriver+"\x00"+limitsDriver)))
}
