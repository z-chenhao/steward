package contract

const CopyResource = "model-connections-copy"
const CopyFile = "views/copy.json"

type Translation struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

const ViewResource = "model-connections-page"
const ViewFile = "views/models.html"

type View struct {
	Resource string `json:"resource"`
	Digest   string `json:"digest"`
	Source   string `json:"source"`
}

const Settings = "steward.model-settings/1"

type SettingsRow struct {
	Ref          string `json:"ref"`
	Account      string `json:"account"`
	KnownAccount bool   `json:"known_account"`
	Verified     bool   `json:"verified"`
	Current      bool   `json:"current"`
	AuthStatus   string `json:"auth_status"`
}

// DraftFacts contains only presentation facts. Source profile values stay in Host.
type DraftFacts struct {
	Method         string `json:"method"`
	HasSource      bool   `json:"has_source"`
	OpenAIEndpoint bool   `json:"openai_endpoint"`
}
type DraftProjection struct {
	Method    string `json:"method"`
	Endpoint  string `json:"endpoint"`
	Service   string `json:"service"`
	WireAPI   string `json:"wire_api"`
	UseSource bool   `json:"use_source"`
}
type SettingsFacts struct {
	WithView  bool          `json:"with_view,omitempty"`
	Draft     *DraftFacts   `json:"draft,omitempty"`
	Rows      []SettingsRow `json:"rows"`
	Requested string        `json:"requested"`
	Notice    string        `json:"notice"`
}
type SettingsChoice struct {
	Ref     string `json:"ref"`
	Account string `json:"account"`
	Open    bool   `json:"open"`
	Status  string `json:"status"`
}
type SettingsProjection struct {
	Copy           *View            `json:"copy,omitempty"`
	View           *View            `json:"view,omitempty"`
	Draft          *DraftProjection `json:"draft,omitempty"`
	Rows           []SettingsChoice `json:"rows"`
	SuppressNotice bool             `json:"suppress_notice"`
}
