package main

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
	"io"
	"os"
)

func runParserResource(ctx context.Context, a *app.App, cfg config.Config, path string, out io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	module, err := io.ReadAll(io.LimitReader(file, parserresource.MaxModuleBytes+1))
	if err != nil {
		return err
	}
	binding, err := a.CaptureWebParser(ctx, cfg, module)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(binding)
}
