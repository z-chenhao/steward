package contract

// Workbench is a repository-private experimental resource seam. It has the
// same package/version/lifecycle as the existing diagnostic services.
const Workbench = "steward.trace-workbench/1"
const ProgramResource = "trace-workbench-program"
const ProgramFile = "views/trace-worker.js"

const StylesheetResource = "trace-activity-styles"
const StylesheetFile = "views/trace-activity.css"

type Stylesheet struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

const TemplateResource = "trace-workbench-template"
const TemplateFile = "views/trace.html"

// Template is private resource syntax, not a public domain-model contract.
type Template struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

type WorkbenchRequest struct{}
type BrowserProgram struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

const CopyResource = "trace-workbench-copy"
const CopyFile = "views/copy.json"

type WorkbenchResult struct {
	Copy       Template       `json:"copy"`
	Template   Template       `json:"template"`
	Program    BrowserProgram `json:"program"`
	Stylesheet Stylesheet     `json:"stylesheet"`
}
