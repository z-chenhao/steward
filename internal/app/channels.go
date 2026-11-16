package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func (a *App) BindChannel(ctx context.Context, c, s, d string, enabled bool, v int64) (interaction.Binding, error) {
	return a.store.BindChannel(ctx, c, s, d, enabled, v)
}
func (a *App) ChannelBindings(ctx context.Context) ([]interaction.Binding, error) {
	return a.store.ChannelBindings(ctx)
}
func (a *App) CheckChannel(ctx context.Context, c, s, d string) error {
	return a.store.CheckChannel(ctx, c, s, d)
}
func (a *App) AcceptInbound(ctx context.Context, e interaction.InboundEvent) (agent.Receipt, error) {
	if e.Channel == "web" && (strings.HasPrefix(e.SourceID, "ui-form:") || strings.HasPrefix(e.EventID, "ui-form:") || strings.HasPrefix(e.SourceID, "ui-action:") || strings.HasPrefix(e.EventID, "ui-action:")) {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	return a.acceptInbound(ctx, e)
}
func (a *App) acceptInbound(ctx context.Context, e interaction.InboundEvent) (agent.Receipt, error) {
	core := a.core.Load()
	d, err := a.Diagnostics(ctx)
	// Accept durable user intent from configured readiness. Token refresh belongs
	// to the provider boundary and must not block the HTTP admission receipt.
	if err != nil || !d.AgentIngressEnabled || core.registry == nil || a.core.Load() != core {
		return agent.Receipt{}, agent.ErrSetup
	}
	return core.runs.AcceptInbound(ctx, e)
}
func (a *App) DecideInbound(ctx context.Context, e interaction.InboundEvent, id, hash, decision string) error {
	return a.runtimeRuns().DecideInbound(ctx, e, id, hash, decision)
}
func (a *App) WebStatus(ctx context.Context) (web.Status, error) {
	d, err := a.Diagnostics(ctx)
	if err != nil {
		return web.Status{}, err
	}
	status, err := a.store.AgentStatus(ctx)
	return web.Status{Readiness: d.Readiness, Status: status, ConfigVersion: d.ConfigVersion, ActiveModelProfileID: d.ActiveModelProfileID}, err
}
func (a *App) ImportChannelArtifact(ctx context.Context, e interaction.InboundEvent, key string, input artifact.Input, src io.Reader) (string, error) {
	if a.prepareArtifactPlugins != nil {
		if err := a.prepareArtifactPlugins(ctx); err != nil {
			return "", err
		}
	}
	return a.store.ImportChannelArtifact(ctx, e, key, input, src)
}
func (a *App) WebProjection(ctx context.Context, m conversation.Message) (json.RawMessage, error) {
	raw, err := a.store.WebProjection(ctx, m)
	if err != nil {
		return nil, err
	}
	var spec ui.Spec
	if json.Unmarshal(raw, &spec) != nil || spec.SchemaVersion != ui.Version {
		return raw, nil
	}
	bubbles, err := a.store.WebBubbles(ctx, m, spec)
	if err != nil {
		return nil, err
	}
	changed := len(bubbles) > 0
	for i := range spec.Components {
		c := &spec.Components[i]
		if !ui.Respondable(*c) || c.FormID == "" || c.MessageID != m.MessageID {
			continue
		}
		changed = true
		content, found, err := a.store.ChannelSourceContent(ctx, "web", web.Subject, web.Destination, "ui-form:"+c.FormID)
		if err != nil {
			c.Disabled = true
			continue
		}
		if found {
			if a.store.RestoreWebForm(ctx, m, c, content) != nil {
				c.Disabled = true
			}
		} else {
			d, err := a.Diagnostics(ctx)
			c.Disabled = err != nil || !d.AgentIngressEnabled
		}
	}
	if !changed {
		return raw, nil
	}
	// Private App-to-Web display data. Never part of the frozen component or
	// the client projection: Web uses it only for escaped native HTML.
	formDisplays := map[string]ui.FormView{}
	for _, c := range spec.Components {
		if c.Name == "steward.form" && c.ViewRef != "" && c.FormID != "" && c.FormView != nil {
			formDisplays[c.FormID] = *c.FormView
		}
	}
	attachDisplayData := func(target *ui.Spec) {
		for _, c := range target.Components {
			if c.ActionManaged && c.WidgetRef != "" && c.ActionView != nil {
				if target.ActionViews == nil {
					target.ActionViews = map[string]contract.ActionView{}
				}
				target.ActionViews[c.WidgetRef] = *c.ActionView
			}
			if c.MeetingManaged && c.FormID != "" && c.MeetingView != nil {
				if target.MeetingViews == nil {
					target.MeetingViews = map[string]contract.MeetingView{}
				}
				target.MeetingViews[c.FormID] = *c.MeetingView
			}
		}
	}
	attachDisplayData(&spec)
	for i := range bubbles {
		attachDisplayData(&bubbles[i])
	}
	return json.Marshal(struct {
		ui.Spec
		Bubbles      []ui.Spec              `json:"bubbles,omitempty"`
		FormDisplays map[string]ui.FormView `json:"form_displays,omitempty"`
	}{spec, bubbles, formDisplays})
}
func (a *App) ServeWeb(ctx context.Context, listener net.Listener, cfg web.Config) error {
	s := web.Server{Apps: a.connectedApps, Logos: a.readAppsLogos, Interface: a.readAppsInterface, Workers: a.store.Inspector(), Computer: a, Capabilities: a.capabilitySettings, CapabilityForms: a.readCapabilityForm, Config: cfg, Core: a, Tasks: a.store.TaskReader(), Feedbacks: a, Schedules: a, Inspector: a.store.Inspector(), Memory: a, Contexts: a.store.Inspector(), Traces: a.store.Inspector(), Models: a.Models(), Control: a.control, Accounts: a.accounts, Secrets: a.control.Secrets, AccountCatalog: a.AccountCatalog, ModelMenu: a.readModelMenu, ModelPicker: a.readModelPicker, WebWorkbench: a.readWebWorkbench, ModelSettings: a.readModelSettings, ModelConnectionPolicy: a.readModelConnection}
	if a.feishu != nil {
		s.Callback = feishu.Handler{Client: *a.feishu, Core: a, ProjectCard: a.readFeishuCard, Wake: func() {
			if s.Wake != nil {
				s.Wake()
			}
		}}
	}
	// The server owns ingress only. A single foreground lifecycle coordinates
	// Actor draining, Automation, delivery recovery and orderly shutdown.
	return web.Run(ctx, &s, listener, func(ctx context.Context) error {
		err := a.Actor(s.ObserveReply).Drain(ctx)
		if errors.Is(err, agent.ErrSetup) {
			return nil
		}
		return err
	}, a.Deliver)
}
func (a *App) deliverChannels(ctx context.Context) error {
	bindings, err := a.store.ChannelBindings(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		key := b.Channel + ":" + b.Destination
		if seen[key] {
			continue
		}
		seen[key] = true
		var adapter delivery.Adapter = web.Adapter{Store: a.store}
		if b.Channel == "feishu" {
			if a.feishu == nil {
				continue
			}
			adapter = feishu.Adapter{Client: *a.feishu, Store: a.store, Artifacts: a, ProjectCard: a.readFeishuCard}
		}
		if err := (delivery.Service{Store: a.store.Deliveries(), History: a.history, Adapter: adapter, Channel: b.Channel, Destination: b.Destination, Policy: a.deliveryPolicy, Boundary: a.testBoundary}).Drain(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) ManageChannel(ctx context.Context, op, channel, subject, destination string, enabled bool, version int64, binding string, out io.Writer) error {
	switch op {
	case "list":
		v, e := a.store.ChannelBindings(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	case "bind":
		v, e := a.store.BindChannel(ctx, channel, subject, destination, enabled, version)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	case "preference":
		v, e := a.store.ChannelPreference(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	case "prefer":
		if e := a.store.PreferChannel(ctx, channel, binding, version); e != nil {
			return e
		}
		v, e := a.store.ChannelPreference(ctx)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	}
	return agent.ErrConflict
}

func (a *App) ChannelPreference(ctx context.Context) (interaction.Preference, error) {
	return a.store.ChannelPreference(ctx)
}
func (a *App) PreferChannel(ctx context.Context, channel, binding string, version int64) error {
	return a.store.PreferChannel(ctx, channel, binding, version)
}

func (a *App) Conversation(ctx context.Context, before int64) (conversation.Page, error) {
	d, err := a.store.Diagnostics(ctx)
	if err != nil {
		return conversation.Page{}, err
	}
	return a.history.Page(ctx, d.AgentID, before)
}

func (a *App) WebApprovals(ctx context.Context) ([]interaction.Approval, error) {
	return a.runtimeRuns().ApprovalView(ctx)
}
