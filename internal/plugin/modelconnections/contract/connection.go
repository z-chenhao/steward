package contract

// ConnectionPolicy is a private candidate-construction policy, with no effects
// or credentials. Host binds explicit user values and authoritative catalogue facts.
const ConnectionPolicy = "steward.model-connection-policy/1"

type LoginFacts struct {
	RestoreOwnedCurrent bool     `json:"restore_owned_current,omitempty"`
	Models              []string `json:"models"`
}

const CLIShorthand = "cli-shorthand"
const TerminalSetup = "terminal-setup"

type ReconnectFacts struct {
	HasCurrent    bool `json:"has_current"`
	TargetCurrent bool `json:"target_current"`
}

type AddedFacts struct {
	HasCurrent bool `json:"has_current"`
}

type ConnectionFacts struct {
	Added         *AddedFacts     `json:"added,omitempty"`
	Reconnect     *ReconnectFacts `json:"reconnect,omitempty"`
	Preset        string          `json:"preset,omitempty"`
	Login         *LoginFacts     `json:"login,omitempty"`
	Service       string          `json:"service"`
	WireAPI       string          `json:"wire_api"`
	AccountBacked bool            `json:"account_backed"`
}
type ConnectionDefaults struct {
	EndpointDefault     string `json:"endpoint_default,omitempty"`
	TokenLimit          int64  `json:"token_limit,omitempty"`
	RestoreOwnedCurrent *bool  `json:"restore_owned_current,omitempty"`
	AddedAction         string `json:"added_action,omitempty"`
	ReconnectAction     string `json:"reconnect_action,omitempty"`
	LoginModel          string `json:"login_model,omitempty"`
	EndpointOverride    string `json:"endpoint_override"`
	WireAPI             string `json:"wire_api"`
	ContextWindow       int64  `json:"context_window"`
	MaxOutputTokens     int64  `json:"max_output_tokens"`
	ImageInput          bool   `json:"image_input"`
}
