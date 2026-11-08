package memory

import "errors"

// Reasons describe host validation, never model text or private evidence.
type proposalError struct{ reason string }

func (e proposalError) Error() string     { return ErrProposal.Error() + ": " + e.reason }
func (e proposalError) Unwrap() error     { return ErrProposal }
func invalidProposal(reason string) error { return proposalError{reason} }
func ProposalReason(err error) string {
	var failure proposalError
	if errors.As(err, &failure) {
		return failure.reason
	}
	if errors.Is(err, ErrProposal) {
		return "Proposal does not satisfy the required schema or evidence rules."
	}
	return ""
}
