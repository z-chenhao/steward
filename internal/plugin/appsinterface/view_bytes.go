package appsinterface

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readOwnViewBytes(name string) ([]byte, error) { return readOwnViewBytesLimit(name, 4096) }
func readOwnViewBytesLimit(name string, limit int) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(filepath.Dir(executable), name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("view resource exceeds limit")
	}
	return raw, nil
}
