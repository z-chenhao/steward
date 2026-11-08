package skill

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"

	"github.com/z-chenhao/assistant/internal/tool"
)

//go:embed builtin/intelligent_ui
var intelligentUI embed.FS

// IncludeIntelligentUI adds a versioned built-in method to the existing frozen
// Skill surface. It grants no rendering authority to Worker roles.
func IncludeIntelligentUI(defs []tool.Definition) ([]tool.Definition, error) {
	files := map[string]string{}
	_ = fs.WalkDir(intelligentUI, "builtin/intelligent_ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, e := intelligentUI.ReadFile(path)
			if e != nil {
				return e
			}
			files[path[len("builtin/intelligent_ui/"):]] = string(b)
		}
		return nil
	})
	raw, _ := json.Marshal(files)
	version := sum(raw)
	summary := Summary{Name: "intelligent_ui", Description: "Present a compact native component or interactive visual when it makes an answer easier to understand, compare, or explore.", Version: version}
	out := append([]tool.Definition(nil), defs...)
	for i := range out {
		if out[i].Spec.Name != "builtin__skill_list" && out[i].Spec.Name != "builtin__skill_view" {
			continue
		}
		if out[i].Spec.Name == "builtin__skill_list" {
			var entries []Summary
			found := false
			for _, resource := range out[i].Spec.PromptContext {
				if resource.Kind == "skill_index" {
					found = json.Unmarshal([]byte(resource.Text), &entries) == nil
					break
				}
			}
			if !found {
				return nil, errors.New("unavailable frozen Skill catalog")
			}
			for _, entry := range entries {
				if entry.Name == summary.Name {
					return nil, errors.New("intelligent_ui is a reserved builtin Skill")
				}
			}
			entries = append(entries, summary)
			metadata, _ := json.Marshal(entries)
			out[i].Spec.Description = listDescription
			out[i].Spec.PromptContext = []tool.PromptContext{{Kind: "skill_index", Source: "skills:" + sum(metadata), Text: string(metadata)}}
			out[i].Execute = func(_ context.Context, args string) tool.Result { return skillListing(entries, args) }
		}
		if out[i].Spec.Name == "builtin__skill_view" {
			out[i].Spec.Description = viewDescription
			old := out[i].Execute
			out[i].Execute = func(ctx context.Context, args string) tool.Result {
				request, ok := parseView(args)
				if !ok {
					return tool.Failure("INVALID_ARGUMENT")
				}
				if request.Name != summary.Name {
					return old(ctx, args)
				}
				if request.Path == "" {
					request.Path = "SKILL.md"
				}
				content, ok := files[request.Path]
				if !ok {
					return tool.Failure("NOT_FOUND")
				}
				return tool.Result{Text: content}
			}
		}
		out[i].Spec.ResourceHash = sum([]byte(out[i].Spec.ResourceHash + version))
	}
	return out, nil
}
