package coding

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"

	"github.com/z-chenhao/assistant/internal/tool"
)

const CheckTool = "builtin__coding_check"
const StatusTool = "builtin__coding_status"

func IsCheckTool(name string) bool {
	return name == CheckTool || name == StatusTool || name == VerifyTool
}

// These are private Worker capabilities. A command receipt does not certify a
// requirement, the independence of its oracle, or a reproducible toolchain.
func CheckDefinitions(scope string) []tool.Definition {
	return []tool.Definition{
		{Host: true, Spec: tool.Spec{Name: StatusTool, Version: 1, Description: "Inspect this Task's current candidate SHA-256 and actual independent check attempts. Optional project_file and check return an exact managed lifecycle plan for approval. Changed code makes older attempts stale. Ad hoc command evidence has only a configured environment scope. Configured project-container evidence also compares the current runtime, frozen Task basis, driver and method; unavailable comparisons are omitted.", InputSchema: `{"type":"object","properties":{"project_file":{"type":"string","minLength":1,"maxLength":1024},"check":{"type":"string","minLength":1,"maxLength":64}},"additionalProperties":false}`, ResourceHash: scope, Permission: "read_only", Risk: "low", ReplayPolicy: "safe", TimeoutMS: 300000, OutputBytes: 512 << 10, Provider: "coding", ConflictPolicy: "none"}},
		{Host: true, Spec: tool.Spec{Name: CheckTool, Version: 1, Description: "Run an exact approved command on a disposable independent copy of the candidate from coding_status. Requires unchanged candidate_sha256. Writes never update the author workspace. Returns original output/exit/timeout/truncation; success only describes this command, not requirements or oracle correctness. Offline local Task environments only.", InputSchema: `{"type":"object","properties":{"command":{"type":"string","minLength":1,"maxLength":16384},"candidate_sha256":{"type":"string","minLength":64,"maxLength":64},"timeout":{"type":"integer","minimum":1,"maximum":120}},"required":["command","candidate_sha256"],"additionalProperties":false}`, ResourceHash: scope, Permission: "external_effect", Risk: "high", ReplayPolicy: "never", TimeoutMS: 300000, OutputBytes: 512 << 10, Provider: "coding", ConflictPolicy: "none"}},
	}
}

type CheckRequest struct {
	ProjectFile     string `json:"project_file,omitempty"`
	Check           string `json:"check,omitempty"`
	Command         string `json:"command"`
	CandidateSHA256 string `json:"candidate_sha256"`
	Timeout         *int   `json:"timeout,omitempty"`
}

// CheckReceipt contains Host facts from the fixed driver, never model-authored
// conclusions. Keep the raw terminal fields even when the outcome is incomplete.
type CheckReceipt struct {
	CandidateSHA256     string `json:"candidate_sha256"`
	EnvironmentScope    string `json:"environment_scope"`
	EnvironmentVerified bool   `json:"environment_verified"`
	MethodRevision      string `json:"method_revision"`
	DriverHash          string `json:"driver_hash"`
	Command             string `json:"command"`
	Output              string `json:"output"`
	ExitCode            int    `json:"exit_code"`
	TimedOut            bool   `json:"timed_out"`
	Truncated           bool   `json:"truncated"`
	DurationMS          int64  `json:"duration_ms"`
	Outcome             string `json:"outcome"`
}

type CheckAttempt struct {
	ResourceCleanup    string                `json:"resource_cleanup,omitempty"`
	EnvironmentMatches *bool                 `json:"environment_matches,omitempty"`
	MethodMatches      *bool                 `json:"method_matches,omitempty"`
	DriverMatches      *bool                 `json:"driver_matches,omitempty"`
	Project            *contract.ProjectPlan `json:"project,omitempty"`
	ProjectFile        string                `json:"project_file,omitempty"`
	ProjectSHA256      string                `json:"project_sha256,omitempty"`
	EnvironmentSHA256  string                `json:"environment_sha256,omitempty"`
	Image              string                `json:"image,omitempty"`
	LatestAttempt      bool                  `json:"latest_attempt"`
	InvocationID       string                `json:"invocation_id"`
	Command            string                `json:"command"`
	Status             string                `json:"status"`
	AcceptedAt         int64                 `json:"accepted_at_unix_micro"`
	CandidateSHA256    string                `json:"candidate_sha256,omitempty"`
	CandidateMatches   *bool                 `json:"candidate_matches,omitempty"`
	MethodRevision     string                `json:"method_revision,omitempty"`
	DriverHash         string                `json:"driver_hash,omitempty"`
	EnvironmentScope   string                `json:"environment_scope,omitempty"`
	Result             json.RawMessage       `json:"result"`
	Error              json.RawMessage       `json:"error"`
}

type CheckStatus struct {
	Environment     *ProjectEnvironment `json:"environment,omitempty"`
	Project         *ProjectSelection   `json:"project,omitempty"`
	CandidateSHA256 string              `json:"candidate_sha256"`
	Checks          []CheckAttempt      `json:"checks"`
	ChecksTruncated bool                `json:"checks_truncated"`
}
