package computer

import "context"

// ObservationInput contains only text already authorized by the original
// Computer lease. Host keeps all actionable references and native identities.
type ObservationInput struct {
	Before, After     string
	DiffAllowed, Full bool
}

type ObservationView struct{ Kind, State string }

type ObservationProjector func(context.Context, ObservationInput) (ObservationView, error)

type WaitInput struct{ RequestedTimeoutMS *int }
type WaitRecipe struct{ TimeoutMS, PollMS int }
type WaitPolicy func(context.Context, WaitInput) (WaitRecipe, error)

func (r UseRuntime) projectObservation(ctx context.Context, in ObservationInput) (ObservationView, error) {
	if r.ProjectObservation != nil {
		return r.ProjectObservation(ctx, in)
	}
	// Explicit unbundled carriers and immutable Runs predating the managed
	// observation contract retain the native projection. Managed adapters must
	// always supply a projector, including when a package is unavailable.
	view := ObservationView{Kind: "full", State: in.After}
	if in.DiffAllowed && !in.Full {
		view.Kind, view.State = "diff", legacyLineDiff(in.Before, in.After)
	}
	return view, nil
}
