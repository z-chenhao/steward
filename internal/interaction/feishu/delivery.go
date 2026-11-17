package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
)

type DeliveryStore interface {
	DeliveryBinding(context.Context, delivery.Request) (interaction.Binding, error)
	DeliveryReplySource(context.Context, delivery.Request) (string, error)
	FeishuTaskFeedbackCard(context.Context, string) (string, string, bool, error)
	LoadProjection(context.Context, string, string) (json.RawMessage, bool, error)
	SaveProjection(context.Context, string, string, int, any) (json.RawMessage, error)
	ChannelReceipt(context.Context, string, int, string) (string, error)
	AcceptChannelReceipt(context.Context, string, int, string, string) error
}
type Artifacts interface {
	DescribeArtifact(context.Context, string) (artifact.Descriptor, error)
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
}
type Adapter struct {
	Client      Client
	Store       DeliveryStore
	Artifacts   Artifacts
	ProjectCard CardProjector
}

// Feishu's UUID dedup window is bounded. A crashed, uncertain remote send is
// query-only after restart; it never acquires permission for another send.
func (Adapter) Idempotent() bool { return false }
func (a Adapter) Query(ctx context.Context, e delivery.Envelope) delivery.Observation {
	return a.deliver(ctx, e, false)
}
func (a Adapter) Send(ctx context.Context, e delivery.Envelope) delivery.Observation {
	return a.deliver(ctx, e, true)
}
func (a Adapter) deliver(ctx context.Context, e delivery.Envelope, send bool) delivery.Observation {
	fail := delivery.Observation{Outcome: delivery.Unknown}
	feedback, reviewURL, pending, err := a.Store.FeishuTaskFeedbackCard(ctx, e.Request.MessageID)
	if err != nil {
		return fail
	}
	rejected := delivery.Observation{Outcome: delivery.Failed, Code: "rejected"}
	if feedback != "" && !send {
		// Revocation blocks a new send. It is not evidence that an earlier,
		// unconfirmed send had no remote effect.
		rejected = fail
	}
	binding, bindingErr := a.Store.DeliveryBinding(ctx, e.Request)
	if bindingErr != nil || !strings.HasPrefix(binding.Subject, a.Client.Subject("")) {
		return rejected
	}
	if _, err := delivery.Compose(e.Request, e.Message); err != nil || e.Request.Channel != "feishu" {
		return rejected
	}
	source, err := a.Store.DeliveryReplySource(ctx, e.Request)
	if err != nil {
		return rejected
	}
	// Eligibility and source remain live Host checks, even for frozen cards.
	if feedback != "" && (source == "" || e.Request.Intent != "needs_input" || len(e.Message.Content) != 1 || e.Message.Content[0].Type != "text") {
		return delivery.Observation{Outcome: delivery.Failed, Code: "rejected"}
	}
	hash := ui.Hash(e.Message)
	raw, exists, err := a.Store.LoadProjection(ctx, e.Request.DeliveryID, hash)
	if err != nil {
		return fail
	}
	if !exists {
		// A query is not acceptance of a new presentation and cannot acquire send
		// permission. Accepted projections must survive current policy changes.
		if !send {
			return fail
		}
		var candidate projection
		if a.ProjectCard != nil {
			candidate, err = a.prepareCard(ctx, e, source, feedback, reviewURL)
			if err != nil {
				return fail
			}
		} else {
			// Explicit historical/storeless embeddings; production always supplies the
			// managed projector. An activated service never takes this branch.
			candidate = composeProjection(e, a.Client.Config.WorkspaceURL, source)
			if feedback != "" {
				candidate.Transport.Card = taskFeedbackCard(feedback, e.Message.Content[0].Text, reviewURL)
				candidate.Transport.IncludeArtifacts = false
			}
		}
		raw, err = a.Store.SaveProjection(ctx, e.Request.DeliveryID, hash, ui.Version, candidate)
		if err != nil {
			return fail
		}
	}
	var spec projection
	if json.Unmarshal(raw, &spec) != nil {
		return fail
	}
	card, includeArtifacts := ui.Card(spec.Spec), true
	if spec.Transport != nil {
		if spec.Transport.Version != 1 || spec.Transport.ReplySource != source || spec.Transport.Card == nil {
			return fail
		}
		card, includeArtifacts = spec.Transport.Card, spec.Transport.IncludeArtifacts
	} else if source != "" {
		return fail
	}
	// Each part has a durable receipt under the original DeliveryRequest. Crash
	// before/after an upload may leave an orphan resource, never a repeated notice.
	parts := []struct {
		kind    string
		content any
		ref     string
	}{{kind: "interactive", content: card}}
	for _, p := range e.Message.Content {
		if includeArtifacts && p.ArtifactRef != "" {
			parts = append(parts, struct {
				kind    string
				content any
				ref     string
			}{kind: "file", ref: p.ArtifactRef})
		}
	}
	for i, p := range parts {
		hash := ui.Hash([]any{e.Request.DestinationRef, p.kind, p.content, p.ref})
		if source != "" {
			hash = ui.Hash([]any{e.Request.DestinationRef, source, p.kind, p.content, p.ref})
		}
		remote, err := a.Store.ChannelReceipt(ctx, e.Request.DeliveryID, i, hash)
		if err != nil {
			return fail
		}
		if remote != "" {
			continue
		}
		if !send {
			return fail
		}
		content := p.content
		if p.ref != "" {
			d, err := a.Artifacts.DescribeArtifact(ctx, p.ref)
			if err != nil {
				return delivery.Observation{Outcome: delivery.Failed, Code: "rejected"}
			}
			src, err := a.Artifacts.OpenArtifact(ctx, p.ref)
			if err != nil {
				return fail
			}
			key, err := a.Client.Upload(ctx, d, src)
			_ = src.Close()
			if err != nil {
				return credentialObservation(err)
			}
			content = map[string]string{"file_key": key}
		}
		var authorize func(context.Context) error
		if feedback != "" {
			if !pending {
				return delivery.Observation{Outcome: delivery.Failed, Code: "rejected"}
			}
			// A slow credential refresh must finish before the final admission
			// check. Only a new card send needs this; receipt recovery stays read-only.
			authorize = func(ctx context.Context) error {
				_, _, current, err := a.Store.FeishuTaskFeedbackCard(ctx, e.Request.MessageID)
				if err == nil && (!current || (reviewURL != "" && !a.trustsReviewURL(reviewURL))) {
					err = errFeedbackClosed
				}
				return err
			}
		}
		if source != "" {
			remote, err = a.Client.reply(ctx, source, keyFor(e.Request.DeliveryID, i), p.kind, content, authorize)
		} else {
			remote, err = a.Client.Send(ctx, e.Request.DestinationRef, keyFor(e.Request.DeliveryID, i), p.kind, content)
		}
		if err != nil {
			return credentialObservation(err)
		}
		if a.Store.AcceptChannelReceipt(ctx, e.Request.DeliveryID, i, hash, remote) != nil {
			return fail
		}
	}
	return delivery.Observation{Outcome: delivery.Delivered}
}

func (a Adapter) trustsReviewURL(reviewURL string) bool {
	origin := strings.TrimRight(a.Client.Config.WorkspaceURL, "/")
	return origin != "" && strings.HasPrefix(reviewURL, origin+"/tasks/")
}

var errFeedbackClosed = errors.New("feishu task feedback is no longer actionable")

func credentialObservation(err error) delivery.Observation {
	if errors.Is(err, errFeedbackClosed) {
		return delivery.Observation{Outcome: delivery.Failed, Code: "rejected"}
	}
	if errors.Is(err, ErrCredentialsRejected) {
		return delivery.Observation{Outcome: delivery.Failed, Code: "credentials_rejected"}
	}
	if errors.Is(err, ErrCredentialsUnavailable) || errors.Is(err, ErrAuthentication) {
		return delivery.Observation{Outcome: delivery.Retry, Code: "credentials_unavailable"}
	}
	return delivery.Observation{Outcome: delivery.Unknown}
}
