// Package sandbox owns the kernel process boundary. The Host supplies scopes
// and mounts; admitted bash commands may come from the model after approval.
package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const developerRoot = "/Library/Developer"
const xcodeDeveloperRoot = "/Applications/Xcode.app/Contents/Developer"

// trustedDeveloperRoots returns the canonical Apple developer directories
// permitted to supply Python 3 and be readable inside Seatbelt. On hosts where
// /Applications/Xcode.app is a symlink (e.g. to /Applications/Xcode_16.x.app),
// include its resolved Xcode bundle developer root as well.
func trustedDeveloperRoots() []string {
	roots := []string{developerRoot, xcodeDeveloperRoot}
	if resolved, err := filepath.EvalSymlinks(xcodeDeveloperRoot); err == nil && isXcodeDeveloperRoot(resolved) && resolved != xcodeDeveloperRoot {
		roots = append(roots, resolved)
	}
	return roots
}

func isXcodeDeveloperRoot(path string) bool {
	if !strings.HasPrefix(path, "/Applications/Xcode") || !strings.HasSuffix(path, "/Contents/Developer") {
		return false
	}
	if strings.ContainsAny(path, "\r\n\x00") || filepath.Clean(path) != path {
		return false
	}
	rel := strings.TrimPrefix(path, "/Applications/")
	bundle := strings.TrimSuffix(rel, "/Contents/Developer")
	return strings.HasPrefix(bundle, "Xcode") && strings.HasSuffix(bundle, ".app") && !strings.Contains(bundle, "/")
}

func Check() error { _, err := os.Stat("/usr/bin/sandbox-exec"); return err }

// Python3 resolves the Xcode launcher before constructing the sandbox. Only
// the canonical interpreter under an existing developer read root can run.
func Python3(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "--no-cache", "--find", "python3")
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	output := NewOutput(4096)
	cmd.Stdout = output
	if _, err := Run(cmd, syscall.SIGKILL); err != nil || output.Overflow() {
		return "", errors.New("system Python 3 discovery failed")
	}
	return developerPython(strings.TrimSpace(string(output.Bytes())))
}

func developerPython(path string) (string, error) {
	return developerPythonWithin(path, trustedDeveloperRoots())
}

func developerPythonWithin(path string, roots []string) (string, error) {
	unavailable := errors.New("system Python 3 is outside supported developer roots or is not executable")
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
		return "", unavailable
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", unavailable
	}
	trusted := false
	for _, root := range roots {
		if strings.HasPrefix(resolved, root+"/") {
			trusted = true
			break
		}
	}
	if !trusted {
		return "", unavailable
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", unavailable
	}
	return resolved, nil
}

func Command(ctx context.Context, executable string, args []string, readRoots []string, writeRoot, workingDir string) (*exec.Cmd, error) {
	return command(ctx, executable, args, readRoots, writeRoot, false)
}

// PythonCommand is for the native file/command helper. Its timeout handler
// signals its own Bash session, whose descendants inherit this sandbox.
func PythonCommand(ctx context.Context, args []string, readRoots []string, root string) (*exec.Cmd, error) {
	interpreter, err := Python3(ctx)
	if err != nil {
		return nil, err
	}
	return command(ctx, interpreter, args, readRoots, root, true)
}

func command(ctx context.Context, executable string, args []string, readRoots []string, writeRoot string, childSignals bool) (*exec.Cmd, error) {
	q := strconv.Quote
	profile := `(version 1)(deny default)(allow process-fork)(allow process-exec)(allow sysctl-read)(allow file-read-metadata)(allow file-read* (literal "/") (subpath "/usr") (subpath "/bin") (subpath "/System")`
	for _, root := range trustedDeveloperRoots() {
		profile += `(subpath ` + q(root) + `)`
	}
	profile += ` (literal ` + q(executable) + `) (subpath ` + q(writeRoot) + `) (literal "/dev/null") (literal "/dev/urandom")`
	for _, root := range readRoots {
		profile += `(subpath ` + q(root) + `)`
	}
	profile += `)(allow file-write* (subpath ` + q(writeRoot) + `) (literal "/dev/null"))`
	if childSignals {
		profile += `(allow signal (target same-sandbox))`
	}
	return exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile, executable}, args...)...), nil
}
