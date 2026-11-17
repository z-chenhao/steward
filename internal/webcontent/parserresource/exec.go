package parserresource

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// Exec handles a private launcher before Host state is opened. Limits affect
// only the disposable decoder and cannot be raised by candidate code.
func Exec(args []string) (bool, error) {
	if len(args) != 2 || args[0] != "-web-parser-exec" {
		return false, nil
	}
	if !filepath.IsAbs(args[1]) {
		return true, ErrInvalid
	}
	limits := []struct {
		resource int
		value    uint64
	}{{unix.RLIMIT_CPU, 20}, {unix.RLIMIT_FSIZE, 8 << 20}, {unix.RLIMIT_NOFILE, 128}}
	// Preserve the existing Linux plugin address-space ceiling; do not relax it
	// just to accommodate an interpreter. Darwin has no truthful AS/RSS setter.
	if runtime.GOOS == "linux" {
		limits = append(limits, struct {
			resource int
			value    uint64
		}{unix.RLIMIT_AS, 2 << 30})
	}
	for _, limit := range limits {
		if err := unix.Setrlimit(limit.resource, &unix.Rlimit{Cur: limit.value, Max: limit.value}); err != nil {
			return true, fmt.Errorf("parser resource limit: %w", err)
		}
	}
	return true, syscall.Exec(filepath.Join(args[1], "node"), []string{filepath.Join(args[1], "node"), filepath.Join(args[1], "parser.mjs")}, os.Environ())
}
