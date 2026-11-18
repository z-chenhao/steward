package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/v1"
)

func TestCapabilityFormCandidateRetainsEveryUnshownSetting(t *testing.T) {
	for _, id := range []string{"memory", "embedding", "skills", "execution", "computer", "mcp"} {
		t.Run(id, func(t *testing.T) {
			in := contract.Request{Operation: "submit", ID: id, Text: "advanced: {keep: true}\n", Action: "apply", Values: []contract.Field{{Name: "mode", Value: "custom"}}}
			values := map[string]string{}
			switch id {
			case "memory":
				values = map[string]string{"key_ref": "env:NEW"}
			case "embedding":
				values = map[string]string{"endpoint": "http://127.0.0.1:9/v1", "model": "embedding", "dimensions": "3", "key_ref": ""}
			case "skills":
				values = map[string]string{"root": "/private/catalog", "scope": "workspace"}
			case "execution":
				values = map[string]string{"root": "/private/workspace", "toolchains": "/private/tools"}
			case "computer":
				values = map[string]string{"root": "/private/computer", "endpoint": "http://127.0.0.1:7777", "provider_version": "0.6.1", "guest_build": "guest", "driver_version": "driver", "driver_sha256": strings.Repeat("a", 64), "driver_team": "TEAM", "driver_bundle": "com.trycua.driver", "architecture": "arm64", "guest_user": "lume", "guest_port": "22", "password_ref": "env:PASS", "host_key": "SHA256:pinned", "node": "/private/node", "script": "/private/sidecar", "manifest_version": "1"}
			case "mcp":
				in.Text = "- name: original\n  env: {keep: true}\n"
				values = map[string]string{"enabled": "yes", "provider_count": "1", "provider.0.original": "0", "provider.0.name": "catalog", "provider.0.command": "/private/server", "provider.0.directory": "/private/state", "provider.0.args": "--safe", "provider.0.tool_count": "1", "provider.0.tool.0.name": "lookup.v1", "provider.0.tool.0.permission": "external_effect"}
			}
			for name, value := range values {
				in.Values = append(in.Values, contract.Field{Name: name, Value: value})
			}
			out := v1.Project(in)
			if out.ErrorCode != "" {
				t.Fatal(out.Message)
			}
			if err := guardCapabilityCandidate(in, out); err != nil {
				t.Fatal("authorized visible edit rejected", err)
			}
			original := out.Text
			out.Text = strings.Replace(original, "keep: true", "keep: false", 1)
			if out.Text == original {
				t.Fatal("hidden config fixture missing")
			}
			if err := guardCapabilityCandidate(in, out); err == nil {
				t.Fatal("hidden setting changed")
			}
			out.Text = ""
			if err := guardCapabilityCandidate(in, out); err == nil {
				t.Fatal("custom form silently disabled configuration")
			}
		})
	}
}
func TestCapabilityFormCandidateRejectsImplicitYAMLAndForeignProvider(t *testing.T) {
	in := contract.Request{Operation: "submit", ID: "mcp", Text: "- name: old\n  env: {private: retained}\n", Action: "apply", Values: []contract.Field{{Name: "mode", Value: "custom"}, {Name: "provider_count", Value: "1"}, {Name: "provider.0.original", Value: "0"}, {Name: "provider.0.name", Value: "new"}, {Name: "provider.0.command", Value: "/private/server"}, {Name: "provider.0.directory", Value: "/private/state"}, {Name: "provider.0.args", Value: ""}, {Name: "provider.0.tool_count", Value: "1"}, {Name: "provider.0.tool.0.name", Value: "lookup.v1"}, {Name: "provider.0.tool.0.permission", Value: "external_effect"}}}
	out := v1.Project(in)
	if out.ErrorCode != "" {
		t.Fatal(out.Message)
	}
	if err := guardCapabilityCandidate(in, out); err != nil {
		t.Fatal("baseline rejected", err)
	}
	original := out.Text
	out.Text = strings.Replace(original, "external_effect", "read_only", 1)
	if err := guardCapabilityCandidate(in, out); err == nil {
		t.Fatal("encoder widened a tool's authorization class")
	}
	out.Text = original
	out.Form.Providers[0].Original = "-1"
	if err := guardCapabilityCandidate(in, out); err == nil {
		t.Fatal("new provider acquired another provider's retained config")
	}
	out.Form.Providers[0].Original = "0"
	out.Text = strings.Replace(original, "env: {private: retained}", "env: &private {private: retained}\n  extra: *private", 1)
	if out.Text == original {
		t.Fatal("alias fixture marker missing")
	}
	if err := guardCapabilityCandidate(in, out); err == nil {
		t.Fatal("alias expanded accepted config")
	}
}

func TestCapabilityFormRequestDoesNotForwardPostedDocumentOrRevision(t *testing.T) {
	in := formRequest("submit", Capability{ID: "memory", Text: "encryption_key_ref: env:BOUND\n"}, "apply", url.Values{"mode": {"custom"}, "key_ref": {"env:NEW"}, "configuration": {"private forged document"}, "revision": {"authority-revision"}, "unrelated": {"private unrelated input"}})
	if in.Text != "encryption_key_ref: env:BOUND\n" || len(in.Values) != 2 {
		t.Fatal(in)
	}
	for _, field := range in.Values {
		if field.Name != "mode" && field.Name != "key_ref" {
			t.Fatal("unrelated input crossed boundary", field.Name)
		}
	}
}
