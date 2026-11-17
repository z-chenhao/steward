package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func pluginInput(in io.Reader, v any) error {
	b, err := io.ReadAll(io.LimitReader(in, rsiplugin.MaxFrameBytes+1))
	if err != nil {
		return err
	}
	return rsiplugin.Decode(b, v)
}

// Sealing is a source/build operation. It must not initialize user databases,
// SecretStore, runtime ownership or a selected execution environment.
func sealPlugin(root string, out io.Writer) error {
	raw, err := os.ReadFile(filepath.Join(root, "steward.plugin.json"))
	if err != nil {
		return err
	}
	var m rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &m); err != nil {
		return err
	}
	m, err = rsi.Seal(root, m)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "steward.plugin.json"), append(data, '\n'), 0600); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(m)
}
func runPlugin(ctx context.Context, a *app.App, f *flag.FlagSet, in io.Reader, out io.Writer) error {
	h := a.Plugins()
	emit := func(v any, err error) error {
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	}
	switch f.Arg(1) {
	case "retry-event":
		return emit(struct {
			Accepted bool `json:"accepted"`
		}{true}, a.RetryPluginEvent(ctx, f.Arg(2)))
	case "deactivate":
		var input struct {
			OperationID string `json:"operation_id"`
			Expected    string `json:"expected_graph_revision"`
		}
		if err := pluginInput(in, &input); err != nil {
			return err
		}
		v, e := h.Store.Deactivate(ctx, input.OperationID, input.Expected)
		return emit(v, e)
	case "uninstall":
		return emit(struct {
			Digest string `json:"digest"`
		}{f.Arg(2)}, h.Store.Retire(ctx, f.Arg(2)))
	case "calls":
		v, e := a.PluginCalls(ctx, f.Arg(2))
		return emit(v, e)
	case "retry-call":
		return emit(struct {
			Accepted bool `json:"accepted"`
		}{true}, a.RetryPluginCall(ctx, f.Arg(2), f.Arg(3), f.Arg(4)))
	case "seal":
		return sealPlugin(f.Arg(2), out)
	case "install":
		p, e := h.Install(ctx, f.Arg(2))
		return emit(p, e)
	case "prepare":
		var g rsiplugin.Graph
		if e := pluginInput(in, &g); e != nil {
			return e
		}
		v, e := h.Prepare(ctx, g)
		return emit(v, e)
	case "policy":
		var p rsi.Policy
		data, e := io.ReadAll(io.LimitReader(in, rsiplugin.MaxControlBytes+1))
		if e != nil {
			return e
		}
		if e := rsiplugin.DecodeControl(data, &p); e != nil {
			return e
		}
		return emit(p, h.Store.PutPolicy(ctx, p))
	case "propose":
		var c rsi.Candidate
		if e := pluginInput(in, &c); e != nil {
			return e
		}
		return emit(c, h.Propose(ctx, c))
	case "verify":
		id, e := h.Verify(ctx, f.Arg(2))
		return emit(struct {
			DecisionID string `json:"decision_ref"`
		}{id}, e)
	case "activate":
		var v rsi.Activation
		if e := pluginInput(in, &v); e != nil {
			return e
		}
		result, e := h.Activate(ctx, v)
		return emit(result, e)
	case "status":
		v, e := h.Store.Status(ctx)
		return emit(v, e)
	case "audit":
		v, e := h.Store.Audit(ctx)
		return emit(v, e)
	case "work":
		v, e := a.WorkPlugins(ctx)
		return emit(struct {
			Processed int `json:"processed"`
		}{v}, e)
	case "quarantine":
		return emit(struct {
			Revision string `json:"revision"`
		}{f.Arg(2)}, h.Store.Quarantine(ctx, f.Arg(2)))
	case "stop", "resume":
		return emit(struct {
			Stopped bool `json:"stopped"`
		}{f.Arg(1) == "stop"}, h.Store.Stop(ctx, f.Arg(1) == "stop"))
	}
	return rsi.ErrInvalid
}
