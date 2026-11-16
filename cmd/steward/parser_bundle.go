package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
)

// Like plugin seal, this is an offline owner build operation. It never opens
// config, a data root, credentials or an execution environment.
func bundleParser(ctx context.Context, source, output, dependencies string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	dependencies, err = filepath.Abs(dependencies)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	module, err := parserbundle.Bundle(ctx, source, dependencies)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".web-parser-module-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(module); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), output)
}
