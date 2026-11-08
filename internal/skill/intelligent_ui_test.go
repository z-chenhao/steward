package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestBuiltinIntelligentUISkillIsVersioned(t *testing.T) {
	defs, err := IncludeIntelligentUI(EmptyDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	r, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := r.Resolve("builtin__skill_list")
	view, _ := r.Resolve("builtin__skill_view")
	if strings.Contains(strings.ToLower(view.Spec.Description), "empty") {
		t.Fatal("readable builtin Skill is advertised as an empty catalog", view.Spec.Description)
	}
	var entries []Summary
	result := list.Execute(context.Background(), `{}`)
	if json.Unmarshal([]byte(result.Text), &entries) != nil || len(entries) != 1 || entries[0].Name != "intelligent_ui" || strings.Contains(result.Text, `"scope"`) || strings.Contains(result.Text, `"version"`) || strings.HasPrefix(entries[0].Description, "Main:") {
		t.Fatal(result)
	}
	for _, path := range []string{"SKILL.md", "references/design.md"} {
		args, _ := json.Marshal(map[string]string{"name": "intelligent_ui", "path": path})
		result = view.Execute(context.Background(), string(args))
		if result.Error != nil || !strings.Contains(result.Text, "Websandbox.connection.remote.sendPrompt") {
			t.Fatal(path, result)
		}
	}
	for _, path := range []string{"templates/native-comparison.json", "templates/calculator.json", "templates/flow-diagram.json", "templates/native-explorer.json", "templates/architecture-diagram.json", "templates/scenario-comparison.json", "templates/three-dimensional.json", "templates/geographic-overview.json"} {
		args, _ := json.Marshal(map[string]string{"name": "intelligent_ui", "path": path})
		result := view.Execute(context.Background(), string(args))
		if result.Error != nil {
			t.Fatal(result)
		}
		if _, ok := ui.ParseWidget(result.Text); !ok {
			t.Fatal("ready template does not satisfy UI tool", path)
		}
	}

	for _, request := range []string{`{"name":"intelligent_ui","version":"wrong"}`, `{"name":"intelligent_ui","path":"../secret"}`} {
		if result = view.Execute(context.Background(), request); result.Error == nil {
			t.Fatal("invalid Skill resource accepted", request)
		}
	}
}

func TestBuiltinSkillKeepsLocalReadsFrozen(t *testing.T) {
	c := catalog(t)
	local, err := Definitions(c)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := IncludeIntelligentUI(local)
	if err != nil {
		t.Fatal(err)
	}
	var metadata []Summary
	if json.Unmarshal([]byte(defs[0].Spec.PromptContext[0].Text), &metadata) != nil || len(metadata) != 2 || metadata[0].Version == "" || metadata[1].Version == "" {
		t.Fatal("combined catalog lost frozen versions", metadata)
	}
	if got := defs[1].Execute(context.Background(), `{"name":"review","path":"references/rules.md"}`); got.Text != "reference" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(c.Root, "review", "references", "rules.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := defs[1].Execute(context.Background(), `{"name":"review","path":"references/rules.md"}`); got.Error == nil || got.Error.Code != "NON_DETERMINISTIC_REPLAY" {
		t.Fatal("combined adapter read changed resources", got)
	}
}
