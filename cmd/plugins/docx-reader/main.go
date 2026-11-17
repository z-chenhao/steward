package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/docxreader"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, docxreader.Plugin()); err != nil {
		os.Exit(1)
	}
}
