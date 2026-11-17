package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/legacyv1"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type capabilityForm = contract.Form
type CapabilityFormProjector func(context.Context, contract.Request) (contract.Result, error)

func formRequest(operation string, v Capability, action string, values url.Values) contract.Request {
	fields := []contract.Field{}
	for name, items := range values {
		if !capabilityPostedField(name) {
			continue
		}
		for _, value := range items {
			fields = append(fields, contract.Field{Name: name, Value: value})
		}
	}
	return contract.Request{WithView: true, Operation: operation, ID: v.ID, Text: v.Text, DefaultKeyRef: v.DefaultKeyRef, Action: action, Values: fields}
}
func (s *Server) projectCapabilityForm(ctx context.Context, in contract.Request) (contract.Result, error) {
	var out contract.Result
	if s.CapabilityForms == nil {
		out = legacyv1.Project(in)
	} else {
		var err error
		out, err = s.CapabilityForms(ctx, in)
		if err != nil {
			return out, ErrCapabilityUnavailable
		}
	}
	if out.Form == nil {
		return out, ErrCapabilityUnavailable
	}
	for _, text := range []string{out.Message, out.Form.Error} {
		if !utf8.ValidString(text) || len(text) > 4096 || strings.ContainsRune(text, 0) {
			return contract.Result{}, ErrCapabilityUnavailable
		}
	}
	if len(out.Form.Providers) > 32 {
		return contract.Result{}, ErrCapabilityUnavailable
	}
	for _, provider := range out.Form.Providers {
		if len(provider.Tools) > 128 {
			return contract.Result{}, ErrCapabilityUnavailable
		}
	}
	if len(out.Text) > 65536 {
		return contract.Result{}, ErrCapabilityInvalid
	}
	switch out.ErrorCode {
	case "":
	case "invalid":
		return out, ErrCapabilityInvalid
	case "validation":
		if out.Message == "" {
			return out, ErrCapabilityInvalid
		}
		return out, errors.New(out.Message)
	default:
		return contract.Result{}, ErrCapabilityUnavailable
	}
	if in.Operation == "submit" && !out.Draft {
		if out.Action != in.Action && !(in.ID == "mcp" && strings.HasPrefix(in.Action, "remove-") && out.Action == "apply") {
			return contract.Result{}, ErrCapabilityInvalid
		}
		if err := guardCapabilityCandidate(in, out); err != nil {
			return contract.Result{}, err
		}
	}
	return out, nil
}
func (s *Server) prepareCapabilityForm(ctx context.Context, v *Capability) {
	out, err := s.projectCapabilityForm(ctx, formRequest("prepare", *v, "", nil))
	if err != nil {
		v.Form = &capabilityForm{Available: false}
		v.View = nil
		v.Copy = nil
		return
	}
	v.Form = out.Form
	v.View = out.View
	v.Copy = out.Copy
}

// Only guided fields cross the plugin boundary. Uploaded/posted documents,
// control revisions and unrelated request values never become plugin facts.
func capabilityPostedField(name string) bool {
	switch name {
	case "mode", "enabled", "key_ref", "endpoint", "model", "dimensions", "root", "scope", "toolchains", "provider_version", "guest_build", "driver_version", "driver_sha256", "driver_team", "driver_bundle", "architecture", "guest_user", "guest_port", "password_ref", "host_key", "node", "script", "manifest_version", "provider_count":
		return true
	}
	parts := strings.Split(name, ".")
	if len(parts) < 3 || parts[0] != "provider" {
		return false
	}
	i, err := strconv.Atoi(parts[1])
	if err != nil || i < 0 || i >= 32 {
		return false
	}
	if len(parts) == 3 {
		switch parts[2] {
		case "original", "name", "command", "directory", "args", "tool_count":
			return true
		}
		return false
	}
	if len(parts) != 5 || parts[2] != "tool" {
		return false
	}
	j, err := strconv.Atoi(parts[3])
	return err == nil && j >= 0 && j < 128 && (parts[4] == "name" || parts[4] == "permission")
}
