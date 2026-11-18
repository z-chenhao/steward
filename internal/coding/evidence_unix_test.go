//go:build darwin || linux

package coding

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCodingProjectEvidenceFIFOIsRejectedBeforeBlockingOpen(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan []EvidenceFailure, 1)
	go func() {
		_, _, failures, _ := CaptureEvidence(context.Background(), dir, []string{"pipe"})
		done <- failures
	}()
	select {
	case failures := <-done:
		if len(failures) != 1 || failures[0].ErrorCode != "INVALID_ARGUMENT" {
			t.Fatal("FIFO accepted as evidence", failures)
		}
	case <-time.After(time.Second):
		// Release a regressed blocking open so the regression fails promptly,
		// rather than leaving a goroutine for the package alarm to terminate.
		file, err := os.OpenFile(name, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal("failed to unblock regressed FIFO open", err)
		}
		file.Close()
		<-done
		t.Fatal("evidence capture blocked on a special file")
	}
}
