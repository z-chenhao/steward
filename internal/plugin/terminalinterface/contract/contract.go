// Package contract contains only private terminal display and bounded choice facts.
package contract

const PluginID = "steward.terminal-interface"
const Service = "steward.terminal-interface/1"

type Copy struct {
	Help               string `json:"Help" format:""`
	State              string `json:"State" format:"s"`
	InputPrompt        string `json:"InputPrompt" format:""`
	Repair             string `json:"Repair" format:"s"`
	Busy               string `json:"Busy" format:""`
	Idle               string `json:"Idle" format:""`
	InputRejected      string `json:"InputRejected" format:"s"`
	Ask                string `json:"Ask" format:"s"`
	Cancelled          string `json:"Cancelled" format:""`
	Retained           string `json:"Retained" format:"ss"`
	BridgeMissing      string `json:"BridgeMissing" format:""`
	ProbeFailed        string `json:"ProbeFailed" format:"s"`
	ProbeTimeout       string `json:"ProbeTimeout" format:""`
	ProbeIncompatible  string `json:"ProbeIncompatible" format:""`
	KnownFailure       string `json:"KnownFailure" format:"s"`
	UnknownFailure     string `json:"UnknownFailure" format:""`
	Message            string `json:"Message" format:"ss"`
	Attachment         string `json:"Attachment" format:"ss"`
	OtherPart          string `json:"OtherPart" format:"ss"`
	LoginURL           string `json:"LoginURL" format:"s"`
	LoginCode          string `json:"LoginCode" format:"s"`
	LoginProgress      string `json:"LoginProgress" format:""`
	Workspace          string `json:"Workspace" format:"s"`
	LoginRequired      string `json:"LoginRequired" format:""`
	ApprovalSaved      string `json:"ApprovalSaved" format:""`
	Task               string `json:"Task" format:"sss"`
	Profile            string `json:"Profile" format:"sss"`
	ProbePassed        string `json:"ProbePassed" format:""`
	ActivateCommand    string `json:"ActivateCommand" format:"s"`
	CredentialStorage  string `json:"CredentialStorage" format:""`
	AccountConnected   string `json:"AccountConnected" format:"s"`
	AccountLoggedOut   string `json:"AccountLoggedOut" format:""`
	UnknownCommand     string `json:"UnknownCommand" format:""`
	Privacy            string `json:"Privacy" format:""`
	Activate           string `json:"Activate" format:"ss"`
	Selected           string `json:"Selected" format:""`
	ConnectionMenu     string `json:"ConnectionMenu" format:""`
	LocalHelp          string `json:"LocalHelp" format:""`
	ConnectionPrompt   string `json:"ConnectionPrompt" format:""`
	Endpoint           string `json:"Endpoint" format:"s"`
	KeyEnvironment     string `json:"KeyEnvironment" format:""`
	CatalogUnavailable string `json:"CatalogUnavailable" format:""`
	AccountPrompt      string `json:"AccountPrompt" format:""`
	ContextTokens      string `json:"ContextTokens" format:""`
	OutputTokens       string `json:"OutputTokens" format:""`
	TokenPrompt        string `json:"TokenPrompt" format:"sd"`
	TokenInvalid       string `json:"TokenInvalid" format:"d"`
	ProfileSaved       string `json:"ProfileSaved" format:"s"`
	ProbeCapabilities  string `json:"ProbeCapabilities" format:""`
	Testing            string `json:"Testing" format:"s"`
	Checking           string `json:"Checking" format:"s"`
	Model              string `json:"Model" format:"ds"`
	ModelPrompt        string `json:"ModelPrompt" format:""`
	SingleModel        string `json:"SingleModel" format:"s"`
	ChooseListed       string `json:"ChooseListed" format:""`
	RecoveredState     string `json:"RecoveredState" format:"s"`
}

// Indices refer to the Host's original catalogue. No IDs, URLs, input text or
// credentials cross this boundary. -1 means there is no exact/numeric match.
type ChoiceFacts struct {
	Count  int  `json:"count"`
	Empty  bool `json:"empty"`
	Exact  int  `json:"exact"`
	Number int  `json:"number"`
	Manual bool `json:"manual"`
}
type Choice struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}
type Query struct {
	Operation string       `json:"operation"`
	Facts     *ChoiceFacts `json:"facts,omitempty"`
}
type Result struct {
	View   *Copy   `json:"view,omitempty"`
	Choice *Choice `json:"choice,omitempty"`
}
