package coding

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitReadEnvironment(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func TestCodingCheckGitContentUsesRawExactObjects(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "source")
			if err = os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, dir, "init", "--object-format="+format)
			if err = os.Mkdir(filepath.Join(dir, "a"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, value := range map[string]string{"a/inside": "nested\x00content", "a.c": "sibling", "script": "#!/bin/sh\nexit 0\n"} {
				mode := os.FileMode(0600)
				if name == "script" {
					mode = 0700
				}
				if err = os.WriteFile(filepath.Join(dir, name), []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			gitFixture(t, dir, "add", ".")
			gitFixture(t, dir, "commit", "-m", "exact fixture")
			head := gitFixture(t, dir, "rev-parse", "HEAD")
			content, err := ReadGitContent(context.Background(), dir, head)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := CaptureCandidate(context.Background(), Workspace{Directory: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer candidate.Close()
			if content.TreeSHA256 != candidate.SHA256 || content.Head != head || content.GitVersion == "" || len(content.GitCommandSHA256) != 64 || !strings.HasPrefix(content.Driver, "steward-git-content/1:") {
				t.Fatal("raw committed content differs", content, candidate)
			}
			if err = os.WriteFile(filepath.Join(dir, "untracked"), []byte("extra content"), 0600); err != nil {
				t.Fatal(err)
			}
			dirty, err := CaptureCandidate(context.Background(), Workspace{Directory: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer dirty.Close()
			if dirty.SHA256 == content.TreeSHA256 {
				t.Fatal("untracked candidate content omitted")
			}
			if err = os.Remove(filepath.Join(dir, "untracked")); err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(filepath.Join(dir, "script"), 0600); err != nil {
				t.Fatal(err)
			}
			mode, err := CaptureCandidate(context.Background(), Workspace{Directory: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer mode.Close()
			if mode.SHA256 == content.TreeSHA256 {
				t.Fatal("execution bit omitted")
			}
			gitFixture(t, dir, "add", ".")
			gitFixture(t, dir, "commit", "-m", "replacement")
			replacement := gitFixture(t, dir, "rev-parse", "HEAD")
			gitFixture(t, dir, "replace", head, replacement)
			original, err := ReadGitContent(context.Background(), dir, head)
			if err != nil || original.TreeSHA256 != content.TreeSHA256 {
				t.Fatal("replacement ref forged remote content", original, err)
			}
			if _, err = ReadGitContent(context.Background(), dir, strings.Repeat("a", len(head))); err == nil {
				t.Fatal("missing object accepted")
			}
			marker := filepath.Join(parent, "MUST-NOT-EXECUTE")
			codingArgs := []string{"config", "remote.origin.promisor", "true"}
			gitFixture(t, dir, codingArgs...)
			gitFixture(t, dir, "config", "extensions.partialClone", "origin")
			gitFixture(t, dir, "config", "protocol.ext.allow", "always")
			helper := filepath.Join(parent, "remote-helper")
			if err = os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$(dirname \"$0\")/MUST-NOT-EXECUTE\"\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, dir, "config", "remote.origin.url", "ext::"+helper)
			baseline := exec.Command("git", "-C", dir, "cat-file", "--batch")
			for _, variable := range gitReadEnvironment() {
				if !strings.HasPrefix(variable, "GIT_ALLOW_PROTOCOL=") && !strings.HasPrefix(variable, "GIT_NO_LAZY_FETCH=") {
					baseline.Env = append(baseline.Env, variable)
				}
			}
			baseline.Stdin = strings.NewReader(strings.Repeat("a", len(head)) + "\n")
			_, _ = baseline.CombinedOutput()
			if _, err = os.Stat(marker); err != nil {
				t.Fatal("fixture did not exercise a real promisor helper", err)
			}
			if err = os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			// Exercise the transport barrier independently of the newer lazy
			// suppression variable, as required on older supported Git.
			denied := exec.Command("git", "-C", dir, "cat-file", "--batch")
			for _, variable := range gitReadEnvironment() {
				if !strings.HasPrefix(variable, "GIT_NO_LAZY_FETCH=") {
					denied.Env = append(denied.Env, variable)
				}
			}
			denied.Stdin = strings.NewReader(strings.Repeat("a", len(head)) + "\n")
			_, _ = denied.CombinedOutput()
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("transport-only guard allowed a helper", err)
			}
			if _, err = ReadGitContent(context.Background(), dir, strings.Repeat("a", len(head))); err == nil {
				t.Fatal("missing promisor object accepted")
			}
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("a remote helper ran", err)
			}
			gitFixture(t, dir, "config", "--unset", "extensions.partialClone")
			gitFixture(t, dir, "config", "--unset", "remote.origin.promisor")

			if err = os.Symlink("script", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, dir, "add", ".")
			gitFixture(t, dir, "commit", "-m", "unsupported link")
			if _, err = ReadGitContent(context.Background(), dir, gitFixture(t, dir, "rev-parse", "HEAD")); err == nil {
				t.Fatal("symbolic link treated as ordinary bytes")
			}
		})
	}
}

func TestCodingCheckGitRecordedOfficialProtocol(t *testing.T) {
	raw, err := os.ReadFile("../../tests/fixtures/git-cat-file.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version     string `json:"version"`
		CommandSHA  string `json:"command_sha256"`
		ResolvedSHA string `json:"resolved_tool_sha256"`
		Scenarios   []struct {
			Name    string   `json:"name"`
			Objects []string `json:"objects"`
			Stdout  string   `json:"stdout_hex"`
			Exit    int      `json:"exit_code"`
		} `json:"scenarios"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil || fixture.Version == "" || len(fixture.CommandSHA) != 64 || len(fixture.ResolvedSHA) != 64 || len(fixture.Scenarios) != 3 {
		t.Fatal("missing official artifact observations", err)
	}
	for _, scenario := range fixture.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			data, err := hex.DecodeString(scenario.Stdout)
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(bytes.NewReader(data))
			switch scenario.Name {
			case "success":
				for i, kind := range []string{"commit", "tree", "blob"} {
					if _, err = readGitObject(reader, io.Discard, scenario.Objects[i], kind, 20); err != nil {
						t.Fatal("recorded success rejected", err)
					}
				}
				if _, err = reader.ReadByte(); err != io.EOF {
					t.Fatal("trailing protocol data")
				}
			case "incompatible_tag":
				if _, err = readGitObject(reader, io.Discard, scenario.Objects[0], "commit", 20); err == nil {
					t.Fatal("tag silently treated as commit")
				}
			case "command_failure":
				if scenario.Exit == 0 {
					t.Fatal("failure fixture has no failed process")
				}
				if _, err = ReadGitContent(context.Background(), filepath.Join(t.TempDir(), "missing"), scenario.Objects[0]); err == nil {
					t.Fatal("command failure accepted")
				}
			default:
				t.Fatal("unknown observed scenario")
			}
		})
	}
}
