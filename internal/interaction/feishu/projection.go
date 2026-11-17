package feishu

import (
	"github.com/z-chenhao/assistant/internal/plugin/feishuchat/legacyv1"

	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
)

// The shared semantic UI stays complete. This private extension freezes the
// exact IM presentation and link, so restart/config changes cannot change the
// action associated with an existing delivery key.
type projection struct {
	ui.Spec
	Transport *transportProjection `json:"feishu_transport,omitempty"`
}
type transportProjection struct {
	Version          int            `json:"version"`
	ReplySource      string         `json:"reply_source"`
	Card             map[string]any `json:"card"`
	IncludeArtifacts bool           `json:"include_artifacts"`
}

// composeProjection is the named never-activated historical interpreter. The
// production managed path uses Adapter.prepareCard and cannot fall back here.
func composeProjection(e delivery.Envelope, workspace, source string) projection {
	spec := ui.Compose(e)
	facts := cardFacts(spec, workspace != "" && e.Message.Sequence > 0 && e.Request.Intent != "approval", false, false)
	out := legacyv1.ProjectCard(facts)
	p, _ := bindCard(spec, out, facts, workspace, e.Message.Sequence, source, "", "")
	return p
}
