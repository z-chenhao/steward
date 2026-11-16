package intelligentui

import "github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"

// The fixed driver retains actual receipts, idempotency and trusted gestures.
// This owner supplies the existing feedback and success presentation policy.
func actionViews(components []Component) []contract.ActionView {
	var views []contract.ActionView
	for i, c := range components {
		if c.Name != "steward.action" {
			continue
		}
		views = append(views, contract.ActionView{Component: i, WorkingLabel: "Working", UnconfirmedError: "Action not confirmed. Retry.", UnknownError: "Result unknown. Check the conversation.", CancelledError: "Action cancelled.", NotExecutedError: "Action was not completed.", FailedError: "Action failed. Retry.", SuccessIndicator: true})
	}
	return views
}
