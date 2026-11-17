// Package contract contains repository-private experimental display and
// candidate-policy facts. Credentials, full provider profiles and activation
// commands are not part of it.
package contract

const PluginID = "steward.model-connections"
const Menu = "steward.model-menu/1"

type Candidate struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Model         string `json:"model"`
	Account       string `json:"account"`
	AccountBacked bool   `json:"account_backed"`
	Connected     bool   `json:"connected"`
	Verified      bool   `json:"verified"`
	Current       bool   `json:"current"`
}
type Model struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type Catalogue struct {
	Account   string  `json:"account"`
	Available bool    `json:"available"`
	Models    []Model `json:"models"`
}

// BrowserFacts are untrusted local display snapshots, never activation facts.
type BrowserChoice struct {
	Choice
	SortKey string `json:"sort_key"`
}
type BrowserFacts struct {
	Previous    []BrowserChoice `json:"previous"`
	Received    []BrowserChoice `json:"received"`
	Reason      string          `json:"reason"`
	Unavailable bool            `json:"unavailable"`
}
type ChoiceRef struct {
	Source string `json:"source"`
	Index  int    `json:"index"`
}
type Facts struct {
	// Host-only frozen reader identity; never sent to candidate code.
	ReaderRevision string        `json:"-"`
	Browser        *BrowserFacts `json:"browser,omitempty"`
	Candidates     []Candidate   `json:"candidates"`
	Catalogues     []Catalogue   `json:"catalogues"`
}
type Choice struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Disabled bool   `json:"disabled,omitempty"`
}
type Projection struct {
	// Host stamps the actual admitted graph after decoding the candidate.
	ReaderRevision string       `json:"-"`
	Order          *[]ChoiceRef `json:"order,omitempty"`
	Choices        []Choice     `json:"choices"`
	Current        string       `json:"current"`
	Unavailable    bool         `json:"unavailable,omitempty"`
}
