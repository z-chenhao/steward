package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

// Exercise production composition rather than marking a test registry ourselves.
// New registrations stay in Code Mode unless they are a fixed coordination primitive.
func TestProductionToolSurfaceKeepsOnlyFrequentControlsDirect(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../runtime/javascript/src/sidecar.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{true, false} {
		name := "configured"
		if !configured {
			name = "missing-runtime-keeps-execution-inaccessible"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
			if err != nil {
				t.Fatal(err)
			}
			if configured {
				cfg.JavaScript = &config.JavaScript{Command: node, Script: script}
			}
			registry, err := (&App{}).runtimeRegistry(context.Background(), cfg, "", memory.New(), nil)
			if err != nil {
				t.Fatal(err)
			}
			direct := map[string]bool{codemode.ToolName: true, tool.SearchName: true, "builtin__intelligent_ui_show": true, "builtin__skill_list": true, "builtin__skill_view": true, "builtin__task_manage": true, "builtin__task_search": true}
			for _, spec := range registry.Describe() {
				if !spec.CodeModeOnly && !direct[spec.Name] {
					t.Errorf("unintended native capability: %s", spec.Name)
				}
			}
			found := tool.SearchTools(agent.MainTools(registry.Describe()), nil, "builtin__web_search", 1)
			if len(found.Tools) != 1 || found.Tools[0].Name != "builtin__web_search" || len(found.Loaded) != 0 {
				t.Fatalf("native search must discover a Code Mode tool without declaring it: %+v", found)
			}
			for role, specs := range map[string][]tool.Spec{"main": agent.MainTools(registry.Describe()), "worker": agent.WorkerTools(registry.Describe())} {
				loaded := make([]string, 0, len(specs))
				want := map[string]bool{}
				for _, spec := range specs {
					switch spec.Name {
					case "builtin__fetch_content", "builtin__get_search_content", "builtin__source_check", "builtin__artifact_search":
						t.Fatalf("%s retained a superseded tool: %s", role, spec.Name)
					}
					loaded = append(loaded, spec.Name)
					if direct[spec.Name] && !spec.Internal {
						want[spec.Name] = true
					}
				}
				declared := agent.ProviderTools(specs, loaded)
				if len(declared) != len(want) {
					t.Fatalf("%s direct controls differ: %+v want %+v", role, declared, want)
				}
				for _, spec := range declared {
					if !want[spec.Name] {
						t.Fatalf("%s exposes unexpected native tool %s", role, spec.Name)
					}
				}
				if !configured && want[codemode.ToolName] {
					t.Fatal("missing runtime registered Code Mode")
				}
			}
			for _, name := range []string{"builtin__fetch_content", "builtin__get_search_content", "builtin__source_check", "builtin__artifact_search"} {
				legacy, ok := registry.Resolve(name)
				if !ok {
					t.Fatal("old frozen capability lost its executor", name)
				}
				legacy.Spec.Exposure = tool.CodeMode
				selected, e := registry.Select([]tool.Spec{legacy.Spec})
				if e != nil {
					t.Fatal("old Run cannot select its frozen tool", name, e)
				}
				if frozen, ok := selected.Resolve(name); !ok || !frozen.Spec.ScriptCallable() {
					t.Fatal("old Run cannot call its frozen tool", name)
				}
			}

		})
	}
}

func TestCoreExecutionAndSkillDiscoveryAreAvailableWithoutOptionalConfig(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	registry, err := (&App{}).runtimeRegistry(context.Background(), cfg, "", memory.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"builtin__read", "builtin__bash", "builtin__edit", "builtin__write", "builtin__skill_list", "builtin__skill_view", "builtin__image_ocr", "builtin__web_search", "builtin__web_fetch"} {
		if _, ok := registry.Resolve(name); !ok {
			t.Fatal("missing core tool", name)
		}
	}
	for _, spec := range registry.Describe() {
		if strings.HasPrefix(spec.Name, "builtin__workspace_") {
			t.Fatal("legacy tool remains", spec.Name)
		}
	}
	list, _ := registry.Resolve("builtin__skill_list")
	if result := list.Execute(context.Background(), `{}`); result.Error != nil || !strings.Contains(result.Text, `"name":"intelligent_ui"`) {
		t.Fatal("builtin catalog not usable", result)
	}
	if _, err := os.Stat(cfg.Execution.Root); !os.IsNotExist(err) {
		t.Fatal("registry discovery created execution files")
	}
}

func TestRuntimePreparationRetriesAfterDependencyRepair(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	cfg.Skills = &skill.Config{Root: filepath.Join(root, "skills"), Scope: "agent"}
	app, err := open(context.Background(), cfg, "", memory.New(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	original := app.core.Load()
	if err := app.prepareCore(context.Background()); err == nil {
		t.Fatal("unavailable catalog admitted")
	}
	if app.core.Load() != original {
		t.Fatal("partial runtime published after preparation failure")
	}
	if err := os.Mkdir(cfg.Skills.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := app.prepareCore(context.Background()); err != nil {
		t.Fatal("first preparation failure cached forever", err)
	}
	prepared := app.core.Load()
	if prepared == original || prepared.registry == nil {
		t.Fatal("repaired runtime not published")
	}
	if err := app.prepareCore(context.Background()); err != nil || app.core.Load() != prepared {
		t.Fatal("successful runtime reconstructed or drifted", err)
	}
}
