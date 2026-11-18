package projectcontainer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	socket, image := os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_SOCKET"), os.Getenv("STEWARD_TEST_PROJECT_CONTAINER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("requires explicitly prepared local container image")
	}
	c, err := New(Config{Socket: socket, Image: image})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func liveDirectory(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture")
	}
	return dir
}
func TestCodingContainerActualParallelPortsStateAndSourceIsolation(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if b, err := c.Basis(ctx); err != nil || b.OS != "linux" || b.Image != c.config.Image {
		t.Fatal(b, err)
	}
	type completed struct {
		terminal    Terminal
		err         error
		dir, marker string
	}
	done := make(chan completed, 2)
	var directories []string
	for index := 0; index < 2; index++ {
		dir := liveDirectory(t)
		directories = append(directories, dir)
		marker := fmt.Sprintf("task-%d", index)
		script := "#!/bin/sh\nprintf '" + marker + "'; cat >/dev/null\n"
		if err := os.WriteFile(filepath.Join(dir, "respond"), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		start := Step{Command: `while true; do nc -l -p "$PORT" -e /workspace/respond; done`, TimeoutSeconds: 20}
		commands := Commands{
			Prepare: Step{`mkdir -p .state/browser; printf '` + marker + `' > .state/db; printf '` + marker + `' > .state/browser/cookie; test ! -S /var/run/docker.sock; ! touch /etc/steward-write-test`, 5}, Start: &start,
			Doctor: Step{`i=0; until test "$(printf ping | nc -w 1 127.0.0.1 "$PORT")" = '` + marker + `'; do i=$((i+1)); test "$i" -lt 10 || exit 1; sleep .1; done`, 10},
			Verify: Step{`touch .state/verifying; while ! test -f .state/release; do sleep .01; done; test "$(cat .state/db)" = '` + marker + `' && test "$(cat .state/browser/cookie)" = '` + marker + `' && test "$(printf ping | nc -w 1 127.0.0.1 "$PORT")" = '` + marker + `' && test "$(wc -l < /proc/net/route)" -eq 1`, 5}, Cleanup: Step{`rm .state/db .state/browser/cookie`, 5},
		}
		token := fmt.Sprintf("live-ports-%d-%d", time.Now().UnixNano(), index)
		go func() {
			terminal, err := c.Run(ctx, token, dir, commands, nil)
			done <- completed{terminal, err, dir, marker}
		}()
	}
	received := 0
	defer func() {
		cancel()
		for received < 2 {
			<-done
			received++
		}
	}()
	for _, dir := range directories {
		for {
			if _, err := os.Stat(filepath.Join(dir, ".state/verifying")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("both independent containers must reach verify before release", ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	for _, dir := range directories {
		if err := os.WriteFile(filepath.Join(dir, ".state/release"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		result := <-done
		received++
		if result.err != nil || result.terminal.Outcome != "passed" || !result.terminal.CleanupConfirmed {
			t.Fatalf("real isolated lifecycle failed: %+v %v", result.terminal, result.err)
		}
		for _, p := range result.terminal.Phases {
			if p.Phase == "verify" && p.ExitCode != nil && *p.ExitCode != 0 {
				t.Fatal(p)
			}
		}
	}
}
func TestCodingContainerActualCancellationKillsDetachedDescendants(t *testing.T) {
	c := liveClient(t)
	dir := liveDirectory(t)
	token := fmt.Sprintf("live-cancel-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commands := Commands{Prepare: Step{`mkdir -p .state; setsid sh -c 'exec >/dev/null 2>&1; sleep 3; printf leaked > .state/late' & touch .state/started; sleep 20`, 30}, Doctor: Step{"true", 5}, Verify: Step{"true", 5}, Cleanup: Step{"touch MUST_NOT_RUN_AFTER_CANCEL", 5}}
	type completed struct {
		terminal Terminal
		err      error
	}
	done := make(chan completed, 1)
	go func() { terminal, err := c.Run(ctx, token, dir, commands, nil); done <- completed{terminal, err} }()
	deadline := time.After(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".state/started")); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("command did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	result := <-done
	if result.err == nil || !result.terminal.CleanupConfirmed {
		t.Fatal("cancellation did not confirm namespace cleanup", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "MUST_NOT_RUN_AFTER_CANCEL")); !os.IsNotExist(err) {
		t.Fatal("executed business cleanup after cancellation", err)
	}
	time.Sleep(3100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, ".state/late")); !os.IsNotExist(err) {
		t.Fatal("detached descendant survived container removal", err)
	}
	if err := c.owned(context.Background(), token); err != ErrNotFound {
		t.Fatal("owned resource survived", err)
	}
}

func TestCodingContainerActualNonzeroRetainsOriginalTerminalFacts(t *testing.T) {
	c := liveClient(t)
	dir := liveDirectory(t)
	step := Step{"true", 5}
	terminal, err := c.Run(context.Background(), fmt.Sprintf("live-failure-%d", time.Now().UnixNano()), dir, Commands{Prepare: step, Doctor: step, Verify: Step{"printf original-diagnostic; exit 7", 5}, Cleanup: step}, nil)
	if err != nil || terminal.Outcome != "verification_failed" || !terminal.CleanupConfirmed {
		t.Fatal(terminal, err)
	}
	found := false
	for _, p := range terminal.Phases {
		if p.Phase == "verify" {
			found = true
			if p.ExitCode == nil || *p.ExitCode != 7 || !strings.Contains(p.Output, "original-diagnostic") {
				t.Fatal(p)
			}
		}
	}
	if !found {
		t.Fatal("missing actual verification")
	}
}
