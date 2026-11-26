package sandbox

import (
	"bufio"
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPythonDiscoveryIgnoresCallerEnvironment(t *testing.T) {
	ctx := context.Background()
	want, err := Python3(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVELOPER_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if got, err := Python3(ctx); err != nil || got != want {
		t.Fatal("caller environment redirected trusted discovery", got, err)
	}
	for _, path := range []string{"python3", "/usr/bin/python3", "/bin/bash", want + "\n/bin/bash"} {
		if _, err := developerPython(path); err == nil {
			t.Fatalf("accepted unsupported interpreter %q", path)
		}
	}
	for _, valid := range []string{
		"/Applications/Xcode.app/Contents/Developer",
		"/Applications/Xcode_16.4.app/Contents/Developer",
	} {
		if !isXcodeDeveloperRoot(valid) {
			t.Fatalf("rejected valid Xcode developer root %q", valid)
		}
	}
	for _, invalid := range []string{
		"/Applications/Evil.app/Contents/Developer",
		"/Applications/Xcode.app/sub/Contents/Developer",
		"/tmp/Xcode.app/Contents/Developer",
		"/Applications/Xcode_16.4.app/../Evil.app/Contents/Developer",
	} {
		if isXcodeDeveloperRoot(invalid) {
			t.Fatalf("accepted untrusted Xcode developer root %q", invalid)
		}
	}
}

func TestNativePythonSignalsOnlyItsOwnSandbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := func(cmd *exec.Cmd) {
		t.Helper()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	host := exec.CommandContext(ctx, "/bin/sleep", "15")
	start(host)
	other, err := PythonCommand(ctx, []string{"-I", "-c", "import time; print('ready', flush=True); time.sleep(15)"}, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	other.Env, other.Dir = []string{"PATH=/usr/bin:/bin"}, root
	stdout, err := other.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	start(other)
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("second sandbox did not start", line, err)
	}
	// Both targets are disposable processes owned by this test. The second
	// Python has the same rules in a separately initialized Seatbelt sandbox.
	const probe = `import os, signal, subprocess, sys
child = subprocess.Popen(['/bin/sleep', '2'], start_new_session=True)
try:
    os.killpg(child.pid, signal.SIGKILL)
finally:
    status = child.wait(timeout=3)
assert status == -signal.SIGKILL
for pid in sys.argv[1:]:
    try:
        os.kill(int(pid), signal.SIGTERM)
    except PermissionError:
        print('outside denied')
    else:
        raise AssertionError('outside signal allowed')`
	cmd, err := PythonCommand(ctx, []string{"-I", "-c", probe, strconv.Itoa(host.Process.Pid), strconv.Itoa(other.Process.Pid)}, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env, cmd.Dir = []string{"PATH=/usr/bin:/bin"}, root
	output := NewOutput(4096)
	cmd.Stdout = output
	if _, err := Run(cmd, syscall.SIGKILL); err != nil || output.Overflow() || strings.Count(string(output.Bytes()), "outside denied\n") != 2 {
		t.Fatal("signal isolation failed", string(output.Bytes()), err)
	}
}
