package capabilityforms

import (
	"net/url"
	"strings"
	"testing"
)

func TestAutomaticCapabilityFormModes(t *testing.T) {
	for _, id := range []string{"memory", "embedding", "skills", "computer"} {
		v := Capability{ID: id, Text: "mode: automatic\n"}
		prepareCapabilityForm(&v)
		if v.Form.Mode != "automatic" || !v.Form.Available {
			t.Fatal(id, v.Form)
		}
		text, err := guidedCapabilityText(id, v.Text, v.Form)
		if err != nil || text != v.Text {
			t.Fatal(id, text, err)
		}
		v.Form.Mode = "off"
		text, err = guidedCapabilityText(id, v.Text, v.Form)
		if err != nil || text != "" {
			t.Fatal(id, text, err)
		}
	}
}

func TestGuidedCapabilityCodecsKeepAdvancedSettings(t *testing.T) {
	cases := []struct {
		id, text, key, value string
		edit                 func(*capabilityForm)
	}{
		{"memory", "encryption_key_ref: env:OLD\nadvanced: {keep: true}\n", "encryption_key_ref", "env:NEW", func(f *capabilityForm) { f.Memory.KeyRef = "env:NEW" }},
		{"embedding", "provider: openai_compatible\nendpoint: http://127.0.0.1:9/v1\nmodel: old\ndimensions: 3\napi_key_ref: ''\nadvanced: {keep: true}\n", "model", "new", func(f *capabilityForm) { f.Embedding.Model = "new" }},
		{"skills", "root: /private/catalog\nscope: agent\nadvanced: {keep: true}\n", "scope", "workspace", func(f *capabilityForm) { f.Skills.Scope = "workspace" }},
		{"execution", "root: /private/workspace\ntoolchain_roots: [/private/tools]\nadvanced: {keep: true}\n", "root", "/private/new-workspace", func(f *capabilityForm) { f.Execution.Root = "/private/new-workspace" }},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			v := Capability{ID: tc.id, Text: tc.text}
			prepareCapabilityForm(&v)
			tc.edit(v.Form)
			text, err := guidedCapabilityText(tc.id, tc.text, v.Form)
			if err != nil {
				t.Fatal(err)
			}
			n, err := capabilityNode(text, false)
			if err != nil || formValue(n, tc.key) != tc.value || formValue(n, "advanced", "keep") != "true" {
				t.Fatal(text, err)
			}
			unchanged, err := guidedCapabilityText(tc.id, text, v.Form)
			if err != nil || unchanged != text {
				t.Fatal("Test/Save serialization is not stable", text, unchanged, err)
			}
		})
	}
	computer := `computer:
  root: /private/computer
  endpoint: http://127.0.0.1:7777
  browser: {node: /private/browser/node, module: /private/browser/adapter.mjs, port: 9222}
  compatibility: {provider_version: 0.6.1, guest_build: build, driver_version: v1, driver_sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa, driver_team: team, driver_bundle: com.trycua.driver, architecture: arm64}
  guest: {user: lume, port: 22, password_ref: 'env:GUEST_KEY', host_key_sha256: 'SHA256:pinned'}
  manifest: {version: 1, applications: [{bundle_id: com.example.App, documents: [/private/note.txt]}], artifact_refs: ['artifact:kept']}
javascript: {command: /private/node, script: /private/sidecar.mjs}
`
	v := Capability{ID: "computer", Text: computer}
	prepareCapabilityForm(&v)
	v.Form.Computer.User = "owner"
	text, err := guidedCapabilityText("computer", computer, v.Form)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := capabilityNode(text, false)
	if formValue(n, "computer", "guest", "user") != "owner" || formValue(n, "computer", "browser", "port") != "9222" || !strings.Contains(text, "com.example.App") || !strings.Contains(text, "artifact:kept") {
		t.Fatal("advanced Computer settings lost", text)
	}
}

func TestGuidedMCPRowsRetainProviderIdentityAndPermissions(t *testing.T) {
	baseline := "- name: first\n  command: /private/first\n  directory: /private/first-state\n  args: []\n  tools: {read: read_only}\n- name: second\n  command: /private/second\n  directory: /private/second-state\n  args: [one, two]\n  tools: {send: external_effect}\n  advanced: kept\n"
	v := Capability{ID: "mcp", Text: baseline}
	prepareCapabilityForm(&v)
	if !editCapabilityRows(v.Form, "remove-provider-0") {
		t.Fatal("remove not handled")
	}
	v.Form.Providers[0].Args = "new argument\n--flag"
	text, err := guidedCapabilityText("mcp", baseline, v.Form)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := capabilityNode(text, true)
	if len(n.Content) != 1 || formValue(n.Content[0], "name") != "second" || formValue(n.Content[0], "advanced") != "kept" || formValue(n.Content[0], "tools", "send") != "external_effect" || formLines(n.Content[0], "args") != "new argument\n--flag" {
		t.Fatal(text)
	}
	// A tested provider must round-trip through the rendered form without
	// reordering its allowlist and invalidating the manager's exact proof.
	v.Form.Providers[0].Tools = []mcpToolForm{{Name: "z_last", Permission: "read_only"}, {Name: "a_first", Permission: "external_effect"}}
	text, err = guidedCapabilityText("mcp", baseline, v.Form)
	if err != nil {
		t.Fatal(err)
	}
	tested := Capability{ID: "mcp", Text: text}
	prepareCapabilityForm(&tested)
	again, err := guidedCapabilityText("mcp", text, tested.Form)
	if err != nil || text != again {
		t.Fatal("allowlist order changed between Test and Save", text, again, err)
	}
	v.Form.Providers[0].Tools = []mcpToolForm{{Name: "send", Permission: "external_effect"}}
	v.Form.Providers[0].Tools = append(v.Form.Providers[0].Tools, mcpToolForm{Name: "send", Permission: "read_only"})
	if _, err = guidedCapabilityText("mcp", baseline, v.Form); err == nil {
		t.Fatal("duplicate permission silently overwritten")
	}
}

func TestGuidedFallbackDoesNotReinterpretYAML(t *testing.T) {
	for _, text := range []string{"- name: a\n  args: ['']\n", "- name: a\n  args: [\"one\\ntwo\"]\n", "- name: a\n  args: &args [one]\n- name: b\n  args: *args\n", "- name: first\n  name: duplicate\n"} {
		v := Capability{ID: "mcp", Text: text}
		prepareCapabilityForm(&v)
		if v.Form.Available {
			t.Fatal("lossy guided form offered", text)
		}
	}
}
func TestGuidedInputValidationKeepsDraft(t *testing.T) {
	values := url.Values{"enabled": {"yes"}, "endpoint": {"http://127.0.0.1:9/v1"}, "model": {"my <model>"}, "dimensions": {"wrong"}}
	f, err := postedCapabilityForm("embedding", values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guidedCapabilityText("embedding", "", f); err == nil || f.Embedding.Dimensions != "wrong" || f.Embedding.Model != "my <model>" {
		t.Fatal(f, err)
	}
	f = &capabilityForm{Available: true, Enabled: true, Memory: memoryForm{KeyRef: "actual-secret-value"}}
	if _, err = guidedCapabilityText("memory", "", f); err == nil {
		t.Fatal("plaintext credential accepted")
	}
	f.Enabled = false
	if text, err := guidedCapabilityText("memory", "encryption_key_ref: env:OLD\n", f); err != nil || text != "" {
		t.Fatal("explicit disable not empty", text, err)
	}
}
