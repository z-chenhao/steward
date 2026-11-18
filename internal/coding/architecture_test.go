package coding_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const module = "github.com/z-chenhao/assistant/"
const policyPackage = module + "internal/plugin/codingworkspace"
const driverPackage = module + "internal/computer/projectcontainer"

type codingPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func codingGraph(t *testing.T, overlay string) map[string]codingPackage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args := []string{"list", "-deps", "-json"}
	if overlay != "" {
		args = append(args, "-overlay", overlay)
	}
	args = append(args, policyPackage, driverPackage)
	cmd := exec.CommandContext(ctx, "go", args...)
	var failure bytes.Buffer
	cmd.Stderr = &failure
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual coding dependency graph: %v\n%s", err, &failure)
	}
	graph := map[string]codingPackage{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var p codingPackage
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		graph[p.ImportPath] = p
	}
	if graph[policyPackage].ImportPath == "" || graph[driverPackage].ImportPath == "" {
		t.Fatal("required policy/driver packages were not inspected")
	}
	return graph
}

func codingBoundaryViolations(graph map[string]codingPackage) []string {
	var violations []string
	for name, p := range graph {
		if name == policyPackage || strings.HasPrefix(name, policyPackage+"/") {
			for _, dep := range p.Imports {
				hostIO := dep == "os" || strings.HasPrefix(dep, "os/") || dep == "net" || strings.HasPrefix(dep, "net/") || dep == "syscall" || dep == "unsafe" || dep == "plugin"
				otherDomain := strings.HasPrefix(dep, module) && dep != module+"pkg/rsiplugin" && dep != policyPackage && !strings.HasPrefix(dep, policyPackage+"/")
				if hostIO || otherDomain {
					violations = append(violations, name+" imports "+dep+"; select project plans from pure contract facts in coding-workspace; put OS/network authority in the narrow Host driver")
				}
			}
		}
	}
	for _, dep := range graph[driverPackage].Deps {
		if strings.HasPrefix(dep, module+"internal/") {
			violations = append(violations, driverPackage+" depends on "+dep+"; keep project policy and domain models in coding-workspace/Host composition; pass only fixed resource and command values to the driver")
		}
	}
	sort.Strings(violations)
	return violations
}

// These boundaries protect the actual policy/kernel split. Import aliases and
// helper packages do not hide dependencies from the compiler's full graph.
func TestCodingArchitectureKeepsPolicyAndResourceAuthoritySeparate(t *testing.T) {
	if failures := codingBoundaryViolations(codingGraph(t, "")); len(failures) != 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
	for _, mutant := range []struct{ file, dependency string }{
		{"../plugin/codingworkspace/project.go", "net/http"},
		{"../computer/projectcontainer/client.go", policyPackage + "/contract"},
	} {
		t.Run(mutant.dependency, func(t *testing.T) {
			path, err := filepath.Abs(mutant.file)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || strings.Count(string(raw), "import (") != 1 {
				t.Fatal("mutation no longer maps to one import boundary", err)
			}
			changed := strings.Replace(string(raw), "import (", "import (\n_ "+string(mustJSON(t, mutant.dependency)), 1)
			dir := t.TempDir()
			replacement := filepath.Join(dir, "source.go")
			if err := os.WriteFile(replacement, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(dir, "overlay.json")
			if err := os.WriteFile(overlay, mustJSON(t, struct{ Replace map[string]string }{map[string]string{path: replacement}}), 0600); err != nil {
				t.Fatal(err)
			}
			failures := codingBoundaryViolations(codingGraph(t, overlay))
			if len(failures) == 0 || !strings.Contains(strings.Join(failures, "\n"), mutant.dependency) {
				t.Fatal("compiler accepted a policy/authority dependency regression", failures)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
