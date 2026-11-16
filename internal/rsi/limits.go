package rsi

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// ExecPlugin is the trusted launcher inside the OS sandbox. Limits are applied
// to this disposable process, never to the running Host. Only cmd/steward and
// the process integration test call it before opening any application state.
func ExecPlugin(args []string) (bool, error) {
	if len(args) != 2 || args[0] != "-rsi-plugin-exec" {
		return false, nil
	}
	limits := []struct {
		resource int
		value    uint64
	}{{unix.RLIMIT_CPU, 30}, {unix.RLIMIT_FSIZE, 16 << 20}, {unix.RLIMIT_NOFILE, 128}}
	// Darwin aliases AS to the unsupported RSS setter. Do not silently claim a
	// hard memory ceiling there; VM-level quotas remain a separate platform gate.
	if runtime.GOOS == "linux" {
		limits = append(limits, struct {
			resource int
			value    uint64
		}{unix.RLIMIT_AS, 2 << 30})
	}
	for _, limit := range limits {
		if err := unix.Setrlimit(limit.resource, &unix.Rlimit{Cur: limit.value, Max: limit.value}); err != nil {
			return true, fmt.Errorf("plugin resource limit %d: %w", limit.resource, err)
		}
	}
	return true, syscall.Exec(args[1], []string{args[1]}, os.Environ())
}
