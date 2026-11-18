package coding

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CandidateSnapshot is a Host-owned copy outside the writable Task mount.
// The digest covers every admitted path, its original bytes and executable bit.
// Consumers inspect this copy instead of pairing an earlier digest with a
// later read of mutable working files. A snapshot is not a verification result.
type CandidateSnapshot struct {
	Directory string
	SHA256    string
	retained  bool
}

func (s *CandidateSnapshot) Retain() { s.retained = true }
func (s *CandidateSnapshot) Close() error {
	if s.retained {
		return nil
	}
	return os.RemoveAll(s.Directory)
}

func CaptureCandidate(ctx context.Context, w Workspace) (*CandidateSnapshot, error) {
	if err := private(w.Directory); err != nil {
		return nil, err
	}
	guard, err := openDirectory(w.Directory)
	if err != nil {
		return nil, ErrInvalid
	}
	defer guard.Close()
	if unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, ErrInvalid
	}
	// Task parents are Host-only. Neither a model path nor a plugin-selected
	// destination can move the candidate into another Task's writable mount.
	parent := filepath.Dir(w.Directory)
	if err := private(parent); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(parent, "candidate-")
	if err != nil {
		return nil, ErrInvalid
	}
	out := &CandidateSnapshot{Directory: directory}
	keep := false
	defer func() {
		if !keep {
			_ = out.Close()
		}
	}()
	limits := NativeImportLimits()
	out.SHA256, err = scanTree(ctx, w.Directory, directory, nil, limits)
	if err != nil {
		return nil, err
	}
	actual, err := scanTree(ctx, directory, "", nil, limits)
	if err != nil || actual != out.SHA256 {
		return nil, ErrInvalid
	}
	current, err := scanTree(ctx, w.Directory, "", nil, limits)
	if err != nil || current != out.SHA256 {
		return nil, ErrInvalid
	}
	keep = true
	return out, nil
}
