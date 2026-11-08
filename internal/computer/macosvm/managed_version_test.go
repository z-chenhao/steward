package macosvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer"
)

func TestManagedRuntimeAcceptsPinnedCLIVersionWithoutDownloading(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS runtime command fixture requires a Unix shell")
	}
	for _, tc := range []struct {
		name, output string
		exit         int
		valid        bool
	}{
		{"official", "0.6.1", 0, true},
		{"whitespace", " 0.6.1 ", 0, true},
		{"wrong-version", "0.6.2", 0, false},
		{"extra-output", "0.6.1 unexpected", 0, false},
		{"failed-command", "0.6.1", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "runtime", "lume.app", "Contents", "MacOS", "lume")
			if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\n[ \"$1\" = \"--version\" ] || exit 9\nprintf '%s\\n' '" + tc.output + "'\n"
			if tc.exit != 0 {
				script += "exit 1\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			m := &managedRuntime{root: root}
			err := m.installRuntime(context.Background())
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, computer.ErrCompatibility) {
				t.Fatal(err)
			}
			// Existing verified runtime inspection must not provision a VM.
			if _, err := os.Stat(filepath.Join(root, "seed")); !os.IsNotExist(err) {
				t.Fatal("version check prepared a desktop", err)
			}
		})
	}
}

func TestManagedPreparationFailureDoesNotInventDiskOrNetworkCause(t *testing.T) {
	for _, tc := range []struct {
		cause       error
		state, text string
	}{
		{context.Canceled, "automatic", "interrupted"},
		{context.DeadlineExceeded, "unavailable", "timed out"},
		{computer.ErrCompatibility, "unavailable", "does not match"},
		{computer.ErrIntegrity, "unavailable", "verification failed"},
		{os.ErrPermission, "unavailable", "permissions"},
		{syscall.ENOSPC, "unavailable", "not enough disk space"},
		{errors.New("PRIVATE_DIAGNOSTIC_CAUSE"), "unavailable", "preparation failed"},
	} {
		state, message := preparationFailure(tc.cause)
		if state != tc.state || !strings.Contains(message, tc.text) || strings.Contains(message, "PRIVATE_DIAGNOSTIC_CAUSE") || strings.Contains(message, "network") {
			t.Fatal(state, message)
		}
	}
}

func TestManagedStateExplainsLegacyFailureWithoutStartingPreparation(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("managed desktops require Apple silicon")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "status.json")
	raw, err := json.Marshal(map[string]string{"state": "unavailable", "message": legacyPreparationStopped})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	state, message := ManagedState(root)
	if state != "unavailable" || !strings.Contains(message, "previous desktop preparation failed") || strings.Contains(message, "network") {
		t.Fatal(state, message)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("settings read changed preparation state", err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 {
		t.Fatal("settings read initialized desktop", err)
	}
}
