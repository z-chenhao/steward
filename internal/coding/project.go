package coding

import (
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

const VerifyTool = "builtin__coding_verify"

// ProjectEnvironment distinguishes a current observation from the retained Task
// basis. Unknown comparisons stay absent; an image ID alone is not verification.
type ProjectEnvironment struct {
	Image       string                  `json:"image"`
	SHA256      string                  `json:"sha256,omitempty"`
	Runtime     *projectcontainer.Basis `json:"runtime,omitempty"`
	TaskSHA256  string                  `json:"task_sha256,omitempty"`
	TaskMatches *bool                   `json:"task_matches,omitempty"`
	ErrorCode   string                  `json:"error_code,omitempty"`
}

type ProjectSelection struct {
	Environment     string                `json:"environment,omitempty"`
	File            string                `json:"file"`
	SHA256          string                `json:"sha256"`
	DocumentChanged bool                  `json:"document_changed"`
	Plan            *contract.ProjectPlan `json:"plan,omitempty"`
	ErrorCode       string                `json:"error_code,omitempty"`
}
type ProjectRequest struct {
	EnvironmentSHA256 string               `json:"environment_sha256"`
	CandidateSHA256   string               `json:"candidate_sha256"`
	Environment       string               `json:"environment"`
	ProjectFile       string               `json:"project_file"`
	ProjectSHA256     string               `json:"project_sha256"`
	Plan              contract.ProjectPlan `json:"plan"`
}

func VerificationDefinition(scope string) tool.Definition {
	return tool.Definition{Host: true, Spec: tool.Spec{Name: VerifyTool, Version: 1, Description: "Verify a project from a fresh candidate using the exact approved plan returned by coding_status(project_file,check). Requires the exact owner-selected local container image digest and current environment SHA-256 from coding_status. The image contains installed toolchains and locked offline dependencies; each check gets a new private container. Separate prepare, foreground service, doctor, verify and cleanup receipts; all work is disposable and offline. Command success is not requirement acceptance.", InputSchema: `{"type":"object","properties":{"candidate_sha256":{"type":"string","minLength":64,"maxLength":64},"environment":{"type":"string","minLength":71,"maxLength":71},"environment_sha256":{"type":"string","minLength":64,"maxLength":64},"project_file":{"type":"string","minLength":1,"maxLength":1024},"project_sha256":{"type":"string","minLength":64,"maxLength":64},"plan":` + contract.ProjectPlanSchema + `},"required":["candidate_sha256","environment","environment_sha256","project_file","project_sha256","plan"],"additionalProperties":false}`, ResourceHash: scope, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 300000, OutputBytes: 512 << 10, Provider: "coding", ConflictPolicy: "none"}}
}

type ProjectReceipt struct {
	Evidence          []ProjectEvidence        `json:"evidence,omitempty"`
	EvidenceFailures  []EvidenceFailure        `json:"evidence_failures,omitempty"`
	CandidateSHA256   string                   `json:"candidate_sha256"`
	Image             string                   `json:"image"`
	EnvironmentSHA256 string                   `json:"environment_sha256"`
	Runtime           projectcontainer.Basis   `json:"runtime"`
	ProjectSHA256     string                   `json:"project_sha256"`
	DocumentChanged   bool                     `json:"document_changed"`
	MethodRevision    string                   `json:"method_revision"`
	DriverHash        string                   `json:"driver_hash"`
	Plan              contract.ProjectPlan     `json:"plan"`
	Outcome           string                   `json:"outcome"`
	Phases            []projectcontainer.Phase `json:"phases"`
}
