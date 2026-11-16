package builtin

import (
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// The acceptance evidence belongs to the Host, independently of the candidate.
// CLI and store fixtures use this same owner file; never derive expected output
// by executing the package being considered for activation.
//
//go:embed computer-policy.json
var computerAcceptance []byte

func computerPolicy() (rsi.Policy, error) {
	var p rsi.Policy
	err := json.Unmarshal(computerAcceptance, &p)
	return p, err
}

func computerConfig() json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"browser_upload_driver": macosvm.BrowserUploadResourceHash(), "browser_snapshot_driver": macosvm.BrowserSnapshotResourceHash()})
	return raw
}
