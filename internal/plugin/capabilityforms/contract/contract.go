// Package contract defines the repository-private capability form service.
package contract

const PluginID = "steward.capability-settings"
const Service = "steward.capability-form/1"

type Form struct {
	Mode      string
	Available bool
	Enabled   bool
	Error     string
	Memory    MemoryForm
	Embedding EmbeddingForm
	Skills    SkillsForm
	Execution ExecutionForm
	Computer  ComputerForm
	Providers []ProviderForm `json:"Providers,omitempty"`
}
type MemoryForm struct {
	KeyRef string
}
type EmbeddingForm struct {
	Endpoint   string
	Model      string
	Dimensions string
	KeyRef     string
}
type SkillsForm struct {
	Root  string
	Scope string
}
type ExecutionForm struct {
	Root       string
	Toolchains string
}
type ComputerForm struct {
	Root            string
	Endpoint        string
	ProviderVersion string
	GuestBuild      string
	DriverVersion   string
	DriverSHA256    string
	DriverTeam      string
	DriverBundle    string
	Architecture    string
	User            string
	Port            string
	PasswordRef     string
	HostKey         string
	Node            string
	Script          string
	ManifestVersion string
}
type ProviderForm struct {
	Original  string
	Name      string
	Command   string
	Directory string
	Args      string
	Tools     []ToolForm `json:"Tools,omitempty"`
}
type ToolForm struct {
	Name       string
	Permission string
}
type Field struct {
	Name  string
	Value string
}

type Request struct {
	WithView      bool    `json:"with_view,omitempty"`
	Operation     string  `json:"operation"`
	ID            string  `json:"id"`
	Text          string  `json:"text"`
	DefaultKeyRef string  `json:"default_key_ref"`
	Action        string  `json:"action"`
	Values        []Field `json:"values"`
}
type Result struct {
	View      *Asset `json:"view,omitempty"`
	Copy      *Asset `json:"copy,omitempty"`
	Form      *Form  `json:"form,omitempty"`
	Text      string `json:"text"`
	Action    string `json:"action"`
	Draft     bool   `json:"draft"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

const PresentationService = "steward.capability-presentation/1"

type PresentationFacts struct {
	Root string `json:"root"`
}
type Description struct {
	ID, Title, Description, Help, Example string
	MemoryChild                           bool
}
type Copy struct {
	Automatic                string
	MemoryKeyUnavailable     string
	ExecutionManaged         string
	VerificationExpired      string
	ProbeAutomatic           string
	ProbeExecutionConflict   string
	ProbeDisabled            string
	ProbeMemoryFailed        string
	ProbeMemoryReady         string
	ProbeEmbeddingFailed     string
	ProbeEmbeddingReady      string
	ProbeSkillsFailed        string
	ProbeSkillsReady         string
	ProbeMCPFailed           string
	ProbeMCPReady            string
	ProbeExecutionFailed     string
	ProbeExecutionInvalid    string
	ProbeExecutionReady      string
	ProbeJavaScriptMissing   string
	ProbeJavaScriptFailed    string
	ProbeComputerStoreFailed string
	ProbeComputerFailed      string
	ProbeComputerReady       string
	ProbeUnknown             string
}
type Presentation struct {
	Descriptions []Description `json:"descriptions"`
	Copy         Copy          `json:"copy"`
}

const ViewFile = "view/capabilities.html"
const CopyFile = "view/copy.json"
const ViewResource = "capability-settings-page"
const CopyResource = "capability-settings-copy"

type Asset struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}
type Translation struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
