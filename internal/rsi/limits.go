package rsi

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	pluginCPUSeconds   = 30
	pluginFileBytes    = 16 << 20
	pluginOpenFiles    = 128
	pluginAddressBytes = 2 << 30
)

func pluginLimitLauncher() (string, error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	return exec.LookPath("/usr/bin/prlimit")
}

// ExecPlugin is the trusted launcher inside the OS sandbox. Limits are applied
// to this disposable process, never to the running Host. Only cmd/steward and
// the process integration test call it before opening any application state.
func ExecPlugin(args []string) (bool, error) {
	if len(args) != 2 || args[0] != "-rsi-plugin-exec" {
		return false, nil
	}
	launcher, err := pluginLimitLauncher()
	if err != nil {
		return true, fmt.Errorf("plugin limit launcher unavailable: %w", err)
	}
	if launcher != "" {
		// Set the same limits in a small native process before the candidate's
		// runtime starts. Lowering AS in a live Go runtime races its threads/GC.
		return true, syscall.Exec(launcher, []string{launcher,
			fmt.Sprintf("--cpu=%d", pluginCPUSeconds), fmt.Sprintf("--fsize=%d", pluginFileBytes),
			fmt.Sprintf("--nofile=%d", pluginOpenFiles), fmt.Sprintf("--as=%d", pluginAddressBytes),
			"--", args[1]}, os.Environ())
	}
	limits := []struct {
		resource int
		value    uint64
	}{{unix.RLIMIT_CPU, pluginCPUSeconds}, {unix.RLIMIT_FSIZE, pluginFileBytes}, {unix.RLIMIT_NOFILE, pluginOpenFiles}}
	// Darwin has no supported AS ceiling; retain its existing three limits.
	for _, limit := range limits {
		if err := unix.Setrlimit(limit.resource, &unix.Rlimit{Cur: limit.value, Max: limit.value}); err != nil {
			return true, fmt.Errorf("plugin resource limit %d: %w", limit.resource, err)
		}
	}
	return true, syscall.Exec(args[1], []string{args[1]}, os.Environ())
}
