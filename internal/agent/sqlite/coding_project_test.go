package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
	"golang.org/x/sys/unix"
)

func TestCodingProjectDocumentRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	directory := t.TempDir()
	recipe := filepath.Join(directory, "project.json")
	if err := unix.Mkfifo(recipe, 0600); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		_, err := projectDocument(directory, "project.json")
		read <- err
	}()
	select {
	case err := <-read:
		if !errors.Is(err, coding.ErrInvalid) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		// Opening both ends releases a reader blocked by the old recipe loader.
		f, err := os.OpenFile(recipe, os.O_RDWR|unix.O_NONBLOCK, 0)
		if err == nil {
			defer f.Close()
			<-read
		}
		t.Fatal("recipe loader waited for a FIFO writer", err)
	}
}

func queuedCodingProject(t *testing.T, codeMode ...bool) (*Store, *Runs, agent.Actor, environment.Config) {
	t.Helper()
	return queuedCodingProjectDocument(t, nil, codeMode...)
}

func queuedCodingProjectDocument(t *testing.T, custom *contract.ProjectDocument, codeMode ...bool) (*Store, *Runs, agent.Actor, environment.Config) {
	t.Helper()
	return queuedCodingProjectSource(t, custom, "", "", codeMode...)
}

func queuedCodingProjectSource(t *testing.T, custom *contract.ProjectDocument, source, exactRecipe string, codeMode ...bool) (*Store, *Runs, agent.Actor, environment.Config) {
	t.Helper()
	image := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE")
	socket := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET")
	if image == "" || socket == "" {
		t.Skip("requires explicit prepared local project container")
	}
	s, r, a, p, cfg := codingCheckFixture(t)
	if exactRecipe != "" {
		// The shared shell fixture seeded this private directory with a sample file.
		// An exact build imports only the independently captured source and the required Git marker.
		if err := os.Remove(filepath.Join(cfg.RepositorySource, "file.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if source != "" {
		err := filepath.WalkDir(source, func(name string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(source, name)
			if err != nil {
				return err
			}
			destination := filepath.Join(cfg.RepositorySource, relative)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0700)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return coding.ErrInvalid
			}
			raw, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, raw, info.Mode().Perm())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if custom == nil {
		installCodingPlugin(t, s, r)
	} else {
		artifactCodingComposition(t, s, r)
	}
	cfg.ProjectContainer = &projectcontainer.Config{Socket: socket, Image: image}
	r.ConfigureLocalEnvironment(cfg)
	model := modelInput()
	model.ContextWindow = 32768
	activateLoop(t, s, model)
	defs := []tool.Definition{coding.VerificationDefinition(cfg.ProjectContainer.ResourceHash(environment.ResourceHash(cfg)))}
	for _, spec := range a.Registry.Describe() {
		d, _ := a.Registry.Resolve(spec.Name)
		defs = append(defs, d)
	}
	registry, err := tool.NewRegistry(defs)
	if err != nil {
		t.Fatal(err)
	}
	a.Registry, r.registry = registry, registry
	cell := len(codeMode) > 0 && codeMode[0]
	if cell {
		defs = append(defs, codemode.Definition())
		registry, err = tool.NewRegistry(defs)
		if err != nil {
			t.Fatal(err)
		}
		a.Registry, r.registry = registry, registry
		r.ConfigureJavaScript(realCodeRuntime(t))
	}
	step := func(command string) contract.ProjectStep {
		return contract.ProjectStep{Command: command, TimeoutSeconds: 5}
	}
	document := contract.ProjectDocument{Version: 1, Name: "offline-fixture", Prepare: step(`mkdir -p .state; printf prepared > .state/prepared`), Doctor: step(`test -f .state/prepared`), Checks: []contract.ProjectCheck{{Name: "state", Requirement: "Independent prepared state is observable", ProjectStep: step(`test -f .state/prepared && printf verified`)}}, Cleanup: step(`rm .state/prepared; printf cleaned`)}
	if custom != nil {
		document = *custom
	}
	recipe := exactRecipe
	if recipe == "" {
		recipe = "project.json"
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.RepositorySource, recipe), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	statusArgs, _ := json.Marshal(map[string]string{"project_file": recipe, "check": document.Checks[0].Name})
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if !isRoot(req) {
			if isFollowup(req) || last.Role == "tool" {
				return llm.GenerationResult{Text: "task observed"}, nil
			}
			args, _ := json.Marshal(map[string]any{"operation": "create", "task": "Execute this exact project lifecycle", "artifactRefs": []string{}})
			return calls("builtin__task_manage", string(args)), nil
		}
		if last.Role != "tool" {
			if cell {
				source := "const s=await tools.builtin__coding_status(" + string(statusArgs) + ");return await tools.builtin__coding_verify({candidate_sha256:s.candidate_sha256,environment:s.project.environment,environment_sha256:s.environment.sha256,project_file:s.project.file,project_sha256:s.project.sha256,plan:s.project.plan});"
				input, _ := json.Marshal(map[string]string{"source": source})
				return calls(codemode.ToolName, string(input)), nil
			}
			return calls(coding.StatusTool, string(statusArgs)), nil
		}
		var status coding.CheckStatus
		if json.Unmarshal([]byte(last.Text), &status) == nil && status.Project != nil && status.Project.Plan != nil {
			args, _ := json.Marshal(coding.ProjectRequest{CandidateSHA256: status.CandidateSHA256, EnvironmentSHA256: status.Environment.SHA256, Environment: image, ProjectFile: status.Project.File, ProjectSHA256: status.Project.SHA256, Plan: *status.Project.Plan})
			return calls(coding.VerifyTool, string(args)), nil
		}
		return llm.GenerationResult{Text: "original lifecycle receipt observed"}, nil
	}
	if _, err := r.Submit(context.Background(), "project-lifecycle", []conversation.ContentPart{{Type: "text", Text: "Run independent verification in the exact prepared image"}}); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	return s, r, a, cfg
}

func TestCodingProjectRealCodeModeKeepsOneApprovedInvocationAndImage(t *testing.T) {
	s, r, a, cfg := queuedCodingProject(t, true)
	if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 0 {
		t.Fatal("cell bypassed concrete approval")
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if scalar(t, s, `SELECT count(*) FROM coding_project_plan WHERE image_id=?`, cfg.ProjectContainer.Image) != 1 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='succeeded' AND attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_project_resource WHERE released=1`) != 1 {
		t.Fatal("cell re-ran, rebound or failed to release its prepared environment")
	}
}

func TestCodingProjectExactApprovedLifecycleUsesFrozenImage(t *testing.T) {
	s, r, a, _ := queuedCodingProject(t)
	if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 0 {
		t.Fatal("lifecycle bypassed exact approval")
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	var resultJSON, dir string
	if err := s.db.QueryRow(`SELECT i.result_json,e.directory FROM tool_invocation i JOIN run r USING(run_id) JOIN task_code_environment e USING(task_id) WHERE i.tool_name='builtin__coding_verify'`).Scan(&resultJSON, &dir); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	var receipt coding.ProjectReceipt
	if json.Unmarshal([]byte(resultJSON), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &receipt) != nil || receipt.Outcome != "passed" || len(receipt.Phases) != 4 {
		t.Fatal("missing real lifecycle facts", resultJSON)
	}
	if receipt.EnvironmentSHA256 == "" || receipt.Runtime.Image == "" || receipt.ProjectSHA256 == "" || receipt.DocumentChanged {
		t.Fatal("input/method identity not bound", receipt)
	}
	for _, phase := range receipt.Phases {
		if phase.ExitCode == nil || *phase.ExitCode != 0 || phase.Status != "passed" {
			t.Fatal(phase)
		}
	}
	if scalar(t, s, `SELECT count(*) FROM coding_project_plan`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_task_environment`) != 1 {
		t.Fatal("approved basis not frozen")
	}
	if _, err := os.Stat(filepath.Join(dir, ".state", "prepared")); !os.IsNotExist(err) {
		t.Fatal("verification polluted author state", err)
	}
	var taskRef string
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&taskRef); err != nil {
		t.Fatal(err)
	}
	report, err := s.CodeReport(context.Background(), taskRef, true)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].Project == nil || report.Checks[0].ResourceCleanup != "confirmed" || report.Checks[0].EnvironmentSHA256 != receipt.EnvironmentSHA256 || report.Checks[0].CandidateMatches == nil || !*report.Checks[0].CandidateMatches {
		t.Fatal("project receipt missing from owner review", report, err)
	}
	for _, pattern := range []string{"candidate-*"} {
		if paths, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), pattern)); len(paths) != 0 {
			t.Fatal("lifecycle inputs not cleaned", paths)
		}
	}
}

func TestCodingProjectStatusSeparatesCurrentEnvironmentFromRetainedPass(t *testing.T) {
	s, r, a, cfg := queuedCodingProject(t)
	p := a.Provider.(*loopProvider)
	original := p.respond
	var before, changed, unavailable coding.CheckStatus
	stage := 0
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isRoot(req) && last.Role == "tool" {
			var receipt coding.ProjectReceipt
			if json.Unmarshal([]byte(last.Text), &receipt) == nil && receipt.Outcome == "passed" {
				return calls(coding.StatusTool, `{}`), nil
			}
			var status coding.CheckStatus
			if json.Unmarshal([]byte(last.Text), &status) == nil && len(status.Checks) == 1 && status.Environment != nil {
				switch stage {
				case 0:
					before = status
					// A valid-shaped but absent image exercises owner configuration
					// drift without pulling, mutating or deleting any daemon image.
					cfg.ProjectContainer = &projectcontainer.Config{Socket: cfg.ProjectContainer.Socket, Image: "sha256:" + strings.Repeat("b", 64)}
					r.ConfigureLocalEnvironment(cfg)
				case 1:
					changed = status
					cfg.ProjectContainer = &projectcontainer.Config{Socket: filepath.Join(t.TempDir(), "absent.sock"), Image: before.Environment.Image}
					r.ConfigureLocalEnvironment(cfg)
				case 2:
					unavailable = status
					stage++
					return llm.GenerationResult{Text: "Current inputs compared; original receipt retained"}, nil
				}
				stage++
				return calls(coding.StatusTool, `{}`), nil
			}
		}
		return original(req)
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	if stage != 3 || before.Environment == nil || changed.Environment == nil || unavailable.Environment == nil {
		t.Fatal("actual status flow was not reached", stage)
	}
	if before.Environment.TaskMatches == nil || !*before.Environment.TaskMatches || before.Environment.Runtime == nil || before.Environment.Runtime.Profile == "" {
		t.Fatal("current prepared basis missing", before)
	}
	for _, match := range []*bool{before.Checks[0].CandidateMatches, before.Checks[0].EnvironmentMatches, before.Checks[0].MethodMatches, before.Checks[0].DriverMatches} {
		if match == nil || !*match {
			t.Fatal("actual unchanged inputs were not identified", before)
		}
	}
	if changed.Environment.ErrorCode != "PROVIDER_UNAVAILABLE" || changed.Environment.TaskMatches == nil || *changed.Environment.TaskMatches || changed.Checks[0].EnvironmentMatches == nil || *changed.Checks[0].EnvironmentMatches || !*changed.Checks[0].CandidateMatches {
		t.Fatal("changed image reused a retained pass", changed)
	}
	if unavailable.Environment.ErrorCode != "PROVIDER_UNAVAILABLE" || unavailable.Environment.TaskMatches != nil || unavailable.Checks[0].EnvironmentMatches != nil {
		t.Fatal("unavailable runtime invented a comparison", unavailable)
	}
	if scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE tool_name='builtin__coding_verify' AND status='succeeded' AND attempt_count=1`) != 1 || scalar(t, s, `SELECT count(*) FROM coding_task_environment`) != 1 {
		t.Fatal("inspection changed original attempt or Task basis")
	}
	if changed.Checks[0].Status != "succeeded" || unavailable.Checks[0].Status != "succeeded" || string(before.Checks[0].Result) != string(changed.Checks[0].Result) || string(before.Checks[0].Result) != string(unavailable.Checks[0].Result) {
		t.Fatal("current applicability rewrote the retained receipt")
	}
}
