package coding

import (
	"crypto/sha256"
	_ "embed"
	"fmt"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
)

//go:embed candidate.go
var candidateDriver string

// Include the retained import scanner and the exact fixed command transport.
// This identity changes independently of the original import driver contract.
func CheckDriverFingerprint(shellDriver string) string {
	return fmt.Sprintf("steward-coding-check/1:%x", sha256.Sum256([]byte(DriverFingerprint()+"\x00"+candidateDriver+"\x00"+shellDriver)))
}

//go:embed evidence.go
var projectEvidenceDriver string

// Project evidence capture is part of the method identity independently of
// historical native import/command identities.
func ProjectDriverFingerprint(containerDriver string) string {
	contractDigest := contract.ProjectContractFingerprint()
	return fmt.Sprintf("steward-coding-project/1:%x", sha256.Sum256([]byte(CheckDriverFingerprint(containerDriver)+"\x00"+projectEvidenceDriver+"\x00"+string(contractDigest[:]))))
}
