package capabilityforms

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"go.yaml.in/yaml/v3"
)

type capabilityForm = contract.Form
type memoryForm = contract.MemoryForm
type embeddingForm = contract.EmbeddingForm
type skillsForm = contract.SkillsForm
type executionForm = contract.ExecutionForm
type computerForm = contract.ComputerForm
type mcpProviderForm = contract.ProviderForm
type mcpToolForm = contract.ToolForm
type Capability struct {
	ID, Text, DefaultKeyRef string
	Form                    *capabilityForm
}

var ErrCapabilityInvalid = errors.New("invalid capability configuration; check the documented fields and separate private paths")

func capabilityNode(text string, sequence bool) (*yaml.Node, error) {
	kind := yaml.MappingNode
	tag := "!!map"
	if sequence {
		kind = yaml.SequenceNode
		tag = "!!seq"
	}
	if strings.TrimSpace(text) == "" {
		return &yaml.Node{Kind: kind, Tag: tag}, nil
	}
	var doc yaml.Node
	d := yaml.NewDecoder(strings.NewReader(text))
	if d.Decode(&doc) != nil || len(doc.Content) != 1 {
		return nil, ErrCapabilityInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrCapabilityInvalid
	}
	n := doc.Content[0]
	if n.Tag == "!!null" {
		return &yaml.Node{Kind: kind, Tag: tag}, nil
	}
	if n.Kind != kind || !plainCapabilityNode(n) {
		return nil, ErrCapabilityInvalid
	}
	return n, nil
}

// Alias/merge documents remain available through Advanced YAML; changing one
// alias through a guided field must never alter another implicit setting.
func plainCapabilityNode(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if !plainCapabilityNode(child) {
			return false
		}
	}
	return true
}
func formNode(n *yaml.Node, path ...string) *yaml.Node {
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}
func formValue(n *yaml.Node, path ...string) string {
	v := formNode(n, path...)
	if v != nil && v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
		return v.Value
	}
	return ""
}
func formLines(n *yaml.Node, path ...string) string {
	v := formNode(n, path...)
	if v == nil || v.Tag == "!!null" {
		return ""
	}
	var lines []string
	for _, entry := range v.Content {
		lines = append(lines, entry.Value)
	}
	return strings.Join(lines, "\n")
}
func prepareCapabilityForm(v *Capability) {
	f := &capabilityForm{Enabled: strings.TrimSpace(v.Text) != "", Available: true}
	n, err := capabilityNode(v.Text, v.ID == "mcp")
	if err != nil {
		f.Available = false
		v.Form = f
		return
	}
	if formValue(n, "mode") == "automatic" {
		f.Mode = "automatic"
		f.Enabled = false
	}
	if f.Mode == "" {
		if f.Enabled {
			f.Mode = "custom"
		} else {
			f.Mode = "off"
		}
	}
	switch v.ID {
	case "memory":
		f.Memory = memoryForm{KeyRef: formValue(n, "encryption_key_ref")}
		if f.Mode == "automatic" && v.DefaultKeyRef != "" {
			f.Memory.KeyRef = v.DefaultKeyRef
		}
	case "embedding":
		f.Embedding = embeddingForm{Endpoint: formValue(n, "endpoint"), Model: formValue(n, "model"), Dimensions: formValue(n, "dimensions"), KeyRef: formValue(n, "api_key_ref")}
	case "skills":
		f.Skills = skillsForm{Root: formValue(n, "root"), Scope: formValue(n, "scope")}
		if f.Skills.Scope == "" {
			f.Skills.Scope = "agent"
		}
	case "execution":
		f.Execution = executionForm{Root: formValue(n, "root"), Toolchains: formLines(n, "toolchain_roots")}
		f.Available = formLineListFits(formNode(n, "toolchain_roots"))
	case "computer":
		c := &f.Computer
		c.Root = formValue(n, "computer", "root")
		c.Endpoint = formValue(n, "computer", "endpoint")
		c.ProviderVersion = formValue(n, "computer", "compatibility", "provider_version")
		c.GuestBuild = formValue(n, "computer", "compatibility", "guest_build")
		c.DriverVersion = formValue(n, "computer", "compatibility", "driver_version")
		c.DriverSHA256 = formValue(n, "computer", "compatibility", "driver_sha256")
		c.DriverTeam = formValue(n, "computer", "compatibility", "driver_team")
		c.DriverBundle = formValue(n, "computer", "compatibility", "driver_bundle")
		c.Architecture = formValue(n, "computer", "compatibility", "architecture")
		c.User = formValue(n, "computer", "guest", "user")
		c.Port = formValue(n, "computer", "guest", "port")
		c.PasswordRef = formValue(n, "computer", "guest", "password_ref")
		c.HostKey = formValue(n, "computer", "guest", "host_key_sha256")
		c.Node = formValue(n, "javascript", "command")
		c.Script = formValue(n, "javascript", "script")
		c.ManifestVersion = formValue(n, "computer", "manifest", "version")
		if !f.Enabled {
			c.ProviderVersion = "0.6.1"
			c.DriverBundle = "com.trycua.driver"
			c.Architecture = "arm64"
			c.Port = "22"
			c.ManifestVersion = "1"
		}
	case "mcp":
		if len(n.Content) > 32 {
			f.Available = false
		}
		for index, p := range n.Content {
			if p.Kind != yaml.MappingNode {
				f.Available = false
				break
			}
			row := mcpProviderForm{Original: strconv.Itoa(index), Name: formValue(p, "name"), Command: formValue(p, "command"), Directory: formValue(p, "directory"), Args: formLines(p, "args")}
			if !formLineListFits(formNode(p, "args")) {
				f.Available = false
			}
			tools := formNode(p, "tools")
			if tools != nil && tools.Kind == yaml.MappingNode {
				for i := 0; i < len(tools.Content); i += 2 {
					row.Tools = append(row.Tools, mcpToolForm{Name: tools.Content[i].Value, Permission: tools.Content[i+1].Value})
				}
			}
			if len(row.Tools) == 0 {
				row.Tools = []mcpToolForm{{Permission: "read_only"}}
			}
			f.Providers = append(f.Providers, row)
		}
		if len(f.Providers) == 0 {
			f.Providers = []mcpProviderForm{blankMCPProvider()}
		}
	}
	v.Form = f
}

func formLineListFits(n *yaml.Node) bool {
	if n == nil || n.Tag == "!!null" {
		return true
	}
	if n.Kind != yaml.SequenceNode {
		return false
	}
	for _, value := range n.Content {
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" || strings.ContainsAny(value.Value, "\r\n") {
			return false
		}
	}
	return true
}
func blankMCPProvider() mcpProviderForm {
	return mcpProviderForm{Original: "-1", Tools: []mcpToolForm{{Permission: "read_only"}}}
}
func postedCapabilityForm(id string, values url.Values) (*capabilityForm, error) {
	f := &capabilityForm{Available: true, Enabled: values.Get("enabled") == "yes"}
	f.Mode = values.Get("mode")
	if f.Mode != "" && f.Mode != "automatic" && f.Mode != "custom" && f.Mode != "off" {
		return f, ErrCapabilityInvalid
	}
	if f.Mode != "" {
		f.Enabled = f.Mode == "custom"
	}
	get := values.Get
	switch id {
	case "memory":
		f.Memory = memoryForm{KeyRef: get("key_ref")}
	case "embedding":
		f.Embedding = embeddingForm{Endpoint: get("endpoint"), Model: get("model"), Dimensions: get("dimensions"), KeyRef: get("key_ref")}
	case "skills":
		f.Skills = skillsForm{Root: get("root"), Scope: get("scope")}
	case "execution":
		f.Execution = executionForm{Root: get("root"), Toolchains: get("toolchains")}
	case "computer":
		f.Computer = computerForm{Root: get("root"), Endpoint: get("endpoint"), ProviderVersion: get("provider_version"), GuestBuild: get("guest_build"), DriverVersion: get("driver_version"), DriverSHA256: get("driver_sha256"), DriverTeam: get("driver_team"), DriverBundle: get("driver_bundle"), Architecture: get("architecture"), User: get("guest_user"), Port: get("guest_port"), PasswordRef: get("password_ref"), HostKey: get("host_key"), Node: get("node"), Script: get("script"), ManifestVersion: get("manifest_version")}
	case "mcp":
		count, e := strconv.Atoi(get("provider_count"))
		if e != nil || count < 1 || count > 32 {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return f, errors.New("Keep between one and 32 tool providers.")
		}
		for i := 0; i < count; i++ {
			prefix := fmt.Sprintf("provider.%d.", i)
			p := mcpProviderForm{Original: get(prefix + "original"), Name: get(prefix + "name"), Command: get(prefix + "command"), Directory: get(prefix + "directory"), Args: get(prefix + "args")}
			toolCount, e := strconv.Atoi(get(prefix + "tool_count"))
			if e != nil || toolCount < 1 || toolCount > 128 {
				//lint:ignore ST1005 This validation message is displayed verbatim in the form.
				return f, errors.New("Keep between one and 128 allowed tools per provider.")
			}
			for j := 0; j < toolCount; j++ {
				key := fmt.Sprintf("%stool.%d.", prefix, j)
				p.Tools = append(p.Tools, mcpToolForm{Name: get(key + "name"), Permission: get(key + "permission")})
			}
			f.Providers = append(f.Providers, p)
		}
	}
	return f, nil
}
func editCapabilityRows(f *capabilityForm, action string) bool {
	if action == "add-provider" {
		if len(f.Providers) < 32 {
			f.Providers = append(f.Providers, blankMCPProvider())
		}
		return true
	}
	var i, j int
	if strings.HasPrefix(action, "remove-provider-") {
		if _, e := fmt.Sscanf(action, "remove-provider-%d", &i); e == nil && i >= 0 && i < len(f.Providers) {
			f.Providers = append(f.Providers[:i], f.Providers[i+1:]...)
			if len(f.Providers) == 0 {
				f.Providers = []mcpProviderForm{blankMCPProvider()}
				f.Enabled = false
			}
		}
		return true
	}
	if strings.HasPrefix(action, "add-tool-") {
		if _, e := fmt.Sscanf(action, "add-tool-%d", &i); e == nil && i >= 0 && i < len(f.Providers) && len(f.Providers[i].Tools) < 128 {
			f.Providers[i].Tools = append(f.Providers[i].Tools, mcpToolForm{Permission: "read_only"})
		}
		return true
	}
	if strings.HasPrefix(action, "remove-tool-") {
		if _, e := fmt.Sscanf(action, "remove-tool-%d-%d", &i, &j); e == nil && i >= 0 && i < len(f.Providers) && j >= 0 && j < len(f.Providers[i].Tools) {
			p := &f.Providers[i]
			p.Tools = append(p.Tools[:j], p.Tools[j+1:]...)
			if len(p.Tools) == 0 {
				p.Tools = []mcpToolForm{{Permission: "read_only"}}
			}
		}
		return true
	}
	return false
}
func setFormNode(n *yaml.Node, value *yaml.Node, path ...string) error {
	for _, key := range path[:len(path)-1] {
		child := formNode(n, key)
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		if child.Kind != yaml.MappingNode {
			return ErrCapabilityInvalid
		}
		n = child
	}
	if n.Kind != yaml.MappingNode {
		return ErrCapabilityInvalid
	}
	key := path[len(path)-1]
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return nil
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return nil
}
func setFormString(n *yaml.Node, value string, path ...string) error {
	return setFormNode(n, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}, path...)
}
func setFormInteger(n *yaml.Node, value string, min, max int, path ...string) error {
	integer, err := strconv.Atoi(value)
	if err != nil || integer < min || integer > max {
		//lint:ignore ST1005 This validation message is displayed verbatim in the form.
		return errors.New("Enter a whole number within the range shown.")
	}
	return setFormNode(n, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(integer)}, path...)
}
func lineValues(text string) []string {
	var values []string
	for _, value := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
func setFormLines(n *yaml.Node, value string, path ...string) error {
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, line := range lineValues(value) {
		list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: line})
	}
	return setFormNode(n, list, path...)
}
func credentialFormRef(value string, optional bool) error {
	if optional && value == "" {
		return nil
	}
	if !interaction.ValidCredentialRef(value) {
		//lint:ignore ST1005 This validation message is displayed verbatim in the form.
		return errors.New("Enter a SecretStore or environment reference, never a secret value.")
	}
	return nil
}
func guidedCapabilityText(id, baseline string, f *capabilityForm) (string, error) {
	if f.Mode == "automatic" && (id == "memory" || id == "embedding" || id == "skills" || id == "computer") {
		return "mode: automatic\n", nil
	}
	if f.Mode == "off" {
		return "", nil
	}
	if strings.TrimSpace(baseline) == "mode: automatic" {
		baseline = ""
	}
	base := Capability{ID: id, Text: baseline}
	prepareCapabilityForm(&base)
	if !base.Form.Available {
		//lint:ignore ST1005 This validation message is displayed verbatim in the form.
		return baseline, errors.New("These settings cannot be edited here. Your existing configuration is unchanged.")
	}
	n, err := capabilityNode(baseline, id == "mcp")
	if err != nil {
		//lint:ignore ST1005 This validation message is displayed verbatim in the form.
		return baseline, errors.New("These settings cannot be edited here. Your existing configuration is unchanged.")
	}
	if !f.Enabled {
		return "", nil
	}
	if err := validateGuidedFields(id, f); err != nil {
		return baseline, err
	}
	// Do not silently reinterpret lists containing embedded line breaks.
	switch id {
	case "memory":
		if err = credentialFormRef(f.Memory.KeyRef, false); err != nil {
			return baseline, err
		}
		err = setFormString(n, f.Memory.KeyRef, "encryption_key_ref")
	case "embedding":
		c := f.Embedding
		if err = credentialFormRef(c.KeyRef, true); err != nil {
			return baseline, err
		}
		for _, field := range []struct{ key, value string }{{"provider", "openai_compatible"}, {"endpoint", c.Endpoint}, {"model", c.Model}, {"api_key_ref", c.KeyRef}} {
			if err = setFormString(n, field.value, field.key); err != nil {
				return baseline, err
			}
		}
		err = setFormInteger(n, c.Dimensions, 1, 4096, "dimensions")
	case "skills":
		if f.Skills.Scope != "agent" && f.Skills.Scope != "workspace" {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return baseline, errors.New("Choose Agent or Workspace for the skill scope.")
		}
		if err = setFormString(n, f.Skills.Root, "root"); err == nil {
			err = setFormString(n, f.Skills.Scope, "scope")
		}
	case "execution":
		if err = setFormString(n, f.Execution.Root, "root"); err == nil {
			err = setFormLines(n, f.Execution.Toolchains, "toolchain_roots")
		}
	case "computer":
		err = patchComputerForm(n, f.Computer)
	case "mcp":
		original := n.Content
		n.Content = nil
		seen := map[int]bool{}
		for _, p := range f.Providers {
			index, e := strconv.Atoi(p.Original)
			if e != nil || index < -1 || index >= len(original) || index >= 0 && seen[index] {
				return baseline, ErrCapabilityInvalid
			}
			seen[index] = true
			node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if index >= 0 {
				node = original[index]
			}
			if len(lineValues(p.Args)) > 32 {
				//lint:ignore ST1005 This validation message is displayed verbatim in the form.
				return baseline, errors.New("Use at most 32 arguments per provider, one per line.")
			}
			for _, field := range []struct{ key, value string }{{"name", p.Name}, {"command", p.Command}, {"directory", p.Directory}} {
				if err = setFormString(node, field.value, field.key); err != nil {
					return baseline, err
				}
			}
			if err = setFormLines(node, p.Args, "args"); err != nil {
				return baseline, err
			}
			tools := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			names := map[string]bool{}
			for _, tool := range p.Tools {
				if tool.Name == "" || names[tool.Name] {
					//lint:ignore ST1005 This validation message is displayed verbatim in the form.
					return baseline, errors.New("Give each allowed tool a unique name.")
				}
				names[tool.Name] = true
				if tool.Permission != "read_only" && tool.Permission != "external_effect" {
					//lint:ignore ST1005 This validation message is displayed verbatim in the form.
					return baseline, errors.New("Choose a permission for every allowed tool.")
				}
				if err = setFormString(tools, tool.Permission, tool.Name); err != nil {
					return baseline, err
				}
			}
			if err = setFormNode(node, tools, "tools"); err != nil {
				return baseline, err
			}
			n.Content = append(n.Content, node)
		}
	}
	if err != nil {
		return baseline, err
	}
	raw, err := yaml.Marshal(n)
	if err != nil || len(raw) > 65536 {
		return baseline, ErrCapabilityInvalid
	}
	return string(raw), nil
}

func validateGuidedFields(id string, f *capabilityForm) error {
	abs := func(value string) bool { return filepath.IsAbs(value) && filepath.Clean(value) != "/" }
	switch id {
	case "skills":
		if !abs(f.Skills.Root) {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return errors.New("Enter an absolute private directory path, not the filesystem root.")
		}
	case "execution":
		if !abs(f.Execution.Root) {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return errors.New("Enter an absolute private directory path, not the filesystem root.")
		}
		for _, path := range lineValues(f.Execution.Toolchains) {
			if !abs(path) {
				//lint:ignore ST1005 This validation message is displayed verbatim in the form.
				return errors.New("Enter one absolute toolchain directory per line.")
			}
		}
	case "embedding":
		u, err := url.Parse(f.Embedding.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(f.Embedding.Model) == "" {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return errors.New("Enter the embedding service URL and its exact model ID.")
		}
	case "mcp":
		for _, p := range f.Providers {
			if strings.TrimSpace(p.Name) == "" || !filepath.IsAbs(p.Command) || !abs(p.Directory) {
				//lint:ignore ST1005 This validation message is displayed verbatim in the form.
				return errors.New("Name each provider and use absolute executable and private directory paths.")
			}
		}
	case "computer":
		c := f.Computer
		if !abs(c.Root) || !filepath.IsAbs(c.Node) || !filepath.IsAbs(c.Script) {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return errors.New("Enter absolute paths for the Computer store, Node executable and sidecar script.")
		}
		if strings.TrimSpace(c.Endpoint) == "" || strings.TrimSpace(c.User) == "" || strings.TrimSpace(c.HostKey) == "" || strings.TrimSpace(c.GuestBuild) == "" || strings.TrimSpace(c.DriverVersion) == "" || strings.TrimSpace(c.DriverSHA256) == "" || strings.TrimSpace(c.DriverTeam) == "" {
			//lint:ignore ST1005 This validation message is displayed verbatim in the form.
			return errors.New("Complete the Computer connection, Guest access and sealed base compatibility fields.")
		}
	}
	return nil
}
func patchComputerForm(n *yaml.Node, c computerForm) error {
	if err := credentialFormRef(c.PasswordRef, false); err != nil {
		return err
	}
	fields := []struct {
		value string
		path  []string
	}{
		{c.Root, []string{"computer", "root"}}, {c.Endpoint, []string{"computer", "endpoint"}},
		{c.ProviderVersion, []string{"computer", "compatibility", "provider_version"}}, {c.GuestBuild, []string{"computer", "compatibility", "guest_build"}},
		{c.DriverVersion, []string{"computer", "compatibility", "driver_version"}}, {c.DriverSHA256, []string{"computer", "compatibility", "driver_sha256"}},
		{c.DriverTeam, []string{"computer", "compatibility", "driver_team"}}, {c.DriverBundle, []string{"computer", "compatibility", "driver_bundle"}}, {c.Architecture, []string{"computer", "compatibility", "architecture"}},
		{c.User, []string{"computer", "guest", "user"}}, {c.PasswordRef, []string{"computer", "guest", "password_ref"}}, {c.HostKey, []string{"computer", "guest", "host_key_sha256"}},
		{c.Node, []string{"javascript", "command"}}, {c.Script, []string{"javascript", "script"}},
	}
	for _, field := range fields {
		if err := setFormString(n, field.value, field.path...); err != nil {
			return err
		}
	}
	if err := setFormInteger(n, c.Port, 1, 65535, "computer", "guest", "port"); err != nil {
		return err
	}
	return setFormInteger(n, c.ManifestVersion, 1, 1, "computer", "manifest", "version")
}
