package contract

// ActionView is private feedback presentation in the existing normalizer receipt.
// It carries no request arguments, ingress identity, source token or authority.
type ActionView struct {
	Component        int    `json:"component"`
	WorkingLabel     string `json:"working_label"`
	UnconfirmedError string `json:"unconfirmed_error"`
	UnknownError     string `json:"unknown_error"`
	CancelledError   string `json:"cancelled_error"`
	NotExecutedError string `json:"not_executed_error"`
	FailedError      string `json:"failed_error"`
	SuccessIndicator bool   `json:"success_indicator"`
}

func ActionDisplayRequired(version string) bool { return displayRequired(version, 27) }
