package rsiplugin

import (
	"context"
	"errors"
)

type Proposal struct {
	Target   string `json:"target_graph_revision"`
	PolicyID string `json:"policy_id"`
	Goal     string `json:"goal"`
}
type ProposalReceipt struct {
	CandidateID string `json:"candidate_id"`
}
type proposalClientKey struct{}
type proposalClient func(context.Context, Proposal) (ProposalReceipt, error)

// Propose can create a candidate only when explicitly granted. It cannot write
// evidence, change acceptance policy, activate a graph or restore stopped work.
func Propose(ctx context.Context, p Proposal) (ProposalReceipt, error) {
	client, ok := ctx.Value(proposalClientKey{}).(proposalClient)
	if !ok {
		return ProposalReceipt{}, errors.New("no proposal context")
	}
	return client(ctx, p)
}
