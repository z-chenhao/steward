package contract

import (
	"crypto/sha256"
	_ "embed"
	"path"
	"slices"
	"strings"
)

const ProjectContract = "steward.coding-project-plan/1"

//go:embed project.go
var projectContractSource []byte

func ProjectContractFingerprint() [sha256.Size]byte {
	return sha256.Sum256(projectContractSource)
}

type ProjectStep struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type ProjectCheck struct {
	Evidence    []string `json:"evidence,omitempty"`
	Name        string   `json:"name"`
	Requirement string   `json:"requirement"`
	ProjectStep
}
type ProjectDocument struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	Prepare ProjectStep    `json:"prepare"`
	Start   *ProjectStep   `json:"start,omitempty"`
	Doctor  ProjectStep    `json:"doctor"`
	Checks  []ProjectCheck `json:"checks"`
	Cleanup ProjectStep    `json:"cleanup"`
}
type ProjectFacts struct {
	Document string `json:"document"`
	Check    string `json:"check"`
}
type ProjectPlan struct {
	Evidence    []string     `json:"evidence,omitempty"`
	Version     int          `json:"version"`
	Name        string       `json:"name"`
	Check       string       `json:"check"`
	Requirement string       `json:"requirement"`
	Prepare     ProjectStep  `json:"prepare"`
	Start       *ProjectStep `json:"start,omitempty"`
	Doctor      ProjectStep  `json:"doctor"`
	Verify      ProjectStep  `json:"verify"`
	Cleanup     ProjectStep  `json:"cleanup"`
}
type ProjectProposal struct {
	Plan      *ProjectPlan `json:"plan,omitempty"`
	ErrorCode string       `json:"error_code,omitempty"`
}

func ValidProjectPath(name string) bool {
	return name != "" && len(name) <= 1024 && !strings.ContainsAny(name, "\\\x00") &&
		!path.IsAbs(name) && path.Clean(name) == name &&
		name != "." && name != ".." && !strings.HasPrefix(name, "../")
}

func ValidEvidencePaths(names []string) bool {
	if len(names) > 4 {
		return false
	}
	for i, name := range names {
		if !ValidProjectPath(name) || slices.Contains(names[:i], name) {
			return false
		}
	}
	return true
}

const projectStepSchema = `{"type":"object","properties":{"command":{"type":"string","minLength":1,"maxLength":16384},"timeout_seconds":{"type":"integer","minimum":1,"maximum":120}},"required":["command","timeout_seconds"],"additionalProperties":false}`

// ProjectPlanSchema is the private approved-plan wire vocabulary.
const ProjectPlanSchema = `{"type":"object","properties":{"version":{"type":"integer","const":1},"name":{"type":"string","minLength":1,"maxLength":128},"check":{"type":"string","minLength":1,"maxLength":64},"requirement":{"type":"string","minLength":1,"maxLength":2048},"prepare":` + projectStepSchema + `,"start":` + projectStepSchema + `,"doctor":` + projectStepSchema + `,"verify":` + projectStepSchema + `,"cleanup":` + projectStepSchema + `,"evidence":{"type":"array","maxItems":4,"items":{"type":"string","minLength":1,"maxLength":1024}}},"required":["version","name","check","requirement","prepare","doctor","verify","cleanup"],"additionalProperties":false}`
