package capabilityforms

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"net/url"
	"strings"
)

func Project(in contract.Request) contract.Result {
	out := contract.Result{Text: in.Text, Action: in.Action}
	if in.WithView {
		assets(&out)
	}
	if in.Operation == "prepare" {
		v := Capability{ID: in.ID, Text: in.Text, DefaultKeyRef: in.DefaultKeyRef}
		prepareCapabilityForm(&v)
		out.Form = v.Form
		return out
	}
	if in.Operation != "submit" {
		out.ErrorCode = "invalid"
		return out
	}
	values := url.Values{}
	for _, field := range in.Values {
		values.Add(field.Name, field.Value)
	}
	form, err := postedCapabilityForm(in.ID, values)
	out.Form = form
	if err == nil && in.ID == "mcp" && editCapabilityRows(form, in.Action) {
		if strings.HasPrefix(in.Action, "remove-") {
			if _, e := guidedCapabilityText(in.ID, in.Text, form); e == nil {
				out.Action = "apply"
			} else {
				form.Error = e.Error()
			}
		}
		if out.Action != "apply" {
			out.Draft = true
			return out
		}
	}
	if err == nil {
		out.Text, err = guidedCapabilityText(in.ID, in.Text, form)
	}
	if err != nil {
		out.ErrorCode = "validation"
		out.Message = err.Error()
		if errors.Is(err, ErrCapabilityInvalid) {
			out.ErrorCode = "invalid"
		}
	}
	return out
}
func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.PresentationService: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var facts contract.PresentationFacts
		if err := rsiplugin.Decode(call.Input, &facts); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, ProjectPresentation(facts))
	}, contract.Service: func(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
		var in contract.Request
		if err := rsiplugin.Decode(call.Input, &in); err != nil {
			return rsiplugin.Result{}, err
		}
		return rsiplugin.JSONResult(ctx, Project(in))
	}}}
}
